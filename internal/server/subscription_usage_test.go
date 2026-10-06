package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	billingv1 "micro-one-api/api/billing/v1"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/authz"
	grpcauth "micro-one-api/platform/grpc"
	"micro-one-api/platform/grpc/xgrpc"
	"micro-one-api/platform/security/serviceidentity"
)

type usageBillingClient struct {
	billingv1.BillingServiceClient
	usage *billingv1.SubscriptionUsage
	err   error
}

type subscriptionUsageBillingServer struct {
	billingv1.UnimplementedBillingServiceServer
}

func (subscriptionUsageBillingServer) GetSubscriptionUsage(_ context.Context, req *billingv1.GetSubscriptionUsageRequest) (*billingv1.GetSubscriptionUsageResponse, error) {
	if req.UserId != 42 {
		return nil, errors.New("wrong user")
	}
	return &billingv1.GetSubscriptionUsageResponse{Usage: &billingv1.SubscriptionUsage{Id: 1, Status: "active", UsageSource: "billing"}}, nil
}

// Exercise the receiver's real authentication chain: a plain fake billing
// client cannot detect a missing relay capability in the RPC registry.
func TestSubscriptionUsageWithRelayServiceIdentity(t *testing.T) {
	verifier, err := serviceidentity.NewVerifier(map[string]string{"relay": "relay-private", "monitor": "monitor-private"}, "legacy")
	require.NoError(t, err)
	listener := bufconn.Listen(1024 * 1024)
	t.Cleanup(func() { _ = listener.Close() })
	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(
		xgrpc.ServiceIdentityUnaryInterceptor(verifier),
		authz.OperatorUnaryInterceptor(),
		authz.CoverageUnaryInterceptor(authz.NewClient("billing", nil), "billing.accounts.read", []string{billingv1.BillingService_GetSubscriptionUsage_FullMethodName}),
	))
	billingv1.RegisterBillingServiceServer(srv, subscriptionUsageBillingServer{})
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(srv.Stop)
	dial := func(token string) billingv1.BillingServiceClient {
		conn, err := grpc.NewClient("passthrough:///billing", grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
			grpc.WithPerRPCCredentials(grpcauth.NewInsecureTokenAuth(token)),
		)
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		return billingv1.NewBillingServiceClient(conn)
	}
	client := dial("relay-private")
	s := NewHTTPServer(rawIdentityClient{}, nil, client, nil, nil)
	s.subscriptionUsecase = subscriptionbiz.NewSubscriptionUsecase(failingSubscriptionRepo{err: errors.New("local reads forbidden")}, nil)
	// A client-supplied user ID cannot change the authenticated user's query.
	req := httptest.NewRequest(http.MethodGet, "/v1/subscription/usage?user_id=99", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	rec := httptest.NewRecorder()
	s.handleSubscriptionUsage(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var response struct {
		Success bool                                  `json:"success"`
		Data    *subscriptionbiz.SubscriptionProgress `json:"data"`
	}
	require.NoError(t, jsonx.Unmarshal(rec.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.Equal(t, "billing", response.Data.UsageSource)
	_, err = dial("monitor-private").GetSubscriptionUsage(context.Background(), &billingv1.GetSubscriptionUsageRequest{UserId: 42})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	_, err = client.ResetAccountBalance(context.Background(), &billingv1.ResetAccountBalanceRequest{})
	require.Equal(t, codes.PermissionDenied, status.Code(err), "usage reads do not grant account writes")
}

func (c usageBillingClient) GetSubscriptionUsage(_ context.Context, req *billingv1.GetSubscriptionUsageRequest, _ ...grpc.CallOption) (*billingv1.GetSubscriptionUsageResponse, error) {
	if req.UserId != 42 {
		return nil, errors.New("wrong user")
	}
	return &billingv1.GetSubscriptionUsageResponse{Usage: c.usage}, c.err
}

func TestSubscriptionUsageUsesBillingAndFailsClosed(t *testing.T) {
	for _, tt := range []struct {
		name    string
		client  usageBillingClient
		status  int
		success bool
	}{
		{"authoritative frozen", usageBillingClient{usage: &billingv1.SubscriptionUsage{Id: 1, Status: "active", UsageSource: "billing", RateMultiplier: 2, DailyUsed: &billingv1.SubscriptionUsageDimension{Used: 1, Settled: 1, Frozen: new(2.0), Limit: new(5.0), Available: new(2.0), Remaining: 4}}}, 200, true},
		{"no active subscription", usageBillingClient{}, 200, false},
		{"authority unavailable", usageBillingClient{err: errors.New("billing unavailable")}, 502, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := NewHTTPServer(rawIdentityClient{}, nil, tt.client, nil, nil)
			s.subscriptionUsecase = subscriptionbiz.NewSubscriptionUsecase(failingSubscriptionRepo{err: errors.New("local reads forbidden")}, nil)
			req := httptest.NewRequest(http.MethodGet, "/v1/subscription/usage", nil)
			req.Header.Set("Authorization", "Bearer test-key")
			rec := httptest.NewRecorder()
			s.handleSubscriptionUsage(rec, req)
			require.Equal(t, tt.status, rec.Code, rec.Body.String())
			if tt.status != 200 {
				return
			}
			var response struct {
				Success bool                                  `json:"success"`
				Data    *subscriptionbiz.SubscriptionProgress `json:"data"`
			}
			require.NoError(t, jsonx.Unmarshal(rec.Body.Bytes(), &response))
			require.Equal(t, tt.success, response.Success)
			if tt.success {
				require.Equal(t, "billing", response.Data.UsageSource)
				require.Equal(t, 2.0, *response.Data.DailyUsed.Frozen)
				require.Equal(t, 2.0, *response.Data.DailyUsed.Available)
				require.Equal(t, 4.0, response.Data.DailyUsed.Remaining)
			}
		})
	}
}
