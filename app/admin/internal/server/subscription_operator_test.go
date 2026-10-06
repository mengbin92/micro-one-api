package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	billingv1 "micro-one-api/api/billing/v1"
	adminbiz "micro-one-api/app/admin/internal/biz"
	"micro-one-api/app/admin/internal/service"
	"micro-one-api/domain/authorization/management"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
)

type selfProgressIAMRepo struct{ adminHTTPLegacyIAMRepo }

func (selfProgressIAMRepo) Execute(_ context.Context, raw, method string, _ management.Request) (management.Response, error) {
	if raw != "verified-self-session" || method != "GetSessionAuthorization" {
		return management.Response{}, status.Error(codes.Unauthenticated, "user session required")
	}
	return management.Response{Session: &management.Session{UserID: 42}}, nil
}

type selfProgressBillingClient struct {
	billingv1.BillingServiceClient
	calls int
}

func (c *selfProgressBillingClient) GetSubscriptionUsage(ctx context.Context, req *billingv1.GetSubscriptionUsageRequest, _ ...grpc.CallOption) (*billingv1.GetSubscriptionUsageResponse, error) {
	c.calls++
	md, _ := metadata.FromOutgoingContext(ctx)
	values := md.Get("x-operator-authorization")
	if len(values) != 1 || values[0] != "Bearer verified-self-session" {
		return nil, status.Error(codes.Unauthenticated, "user session required")
	}
	if req.UserId != 42 || !req.SelfRequest {
		return nil, status.Error(codes.PermissionDenied, "self ownership required")
	}
	return &billingv1.GetSubscriptionUsageResponse{Usage: &billingv1.SubscriptionUsage{Id: 1, Status: "active", UsageSource: "billing"}}, nil
}

func TestSelfSubscriptionProgressForwardsVerifiedSession(t *testing.T) {
	for _, tt := range []struct {
		name, token, query string
		status, calls      int
	}{
		{"self", "verified-self-session", "?user_id=42", 200, 1},
		{"implicit self", "verified-self-session", "", 200, 1},
		{"foreign user", "verified-self-session", "?user_id=99", 403, 0},
		{"missing session", "", "", 401, 0},
		{"invalid session", "revoked-session", "", 401, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			billing := &selfProgressBillingClient{}
			svc := service.NewAdminService(billing, nil, nil, nil)
			svc.SetIAMService(service.NewIAMAdminService(adminbiz.NewIAMUsecase(selfProgressIAMRepo{})))
			svc.SetSubscriptionUsecases(subscriptionbiz.NewSubscriptionUsecase(nil, nil), nil, nil)
			server := NewHTTPServer(":0", svc, nil)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/subscriptions/progress"+tt.query, nil)
			if tt.token != "" {
				req.Header.Set("Authorization", "Bearer "+tt.token)
			}
			req = req.WithContext(metadata.NewOutgoingContext(req.Context(), metadata.Pairs("x-operator-authorization", "Bearer forged-session", "x-operator-authorization", "Bearer duplicate")))
			rec := httptest.NewRecorder()
			server.ServeHTTP(rec, req)
			require.Equal(t, tt.status, rec.Code, rec.Body.String())
			require.Equal(t, tt.calls, billing.calls)
			if tt.status == 200 {
				require.Contains(t, rec.Body.String(), `"success":true`)
			}
		})
	}
}
