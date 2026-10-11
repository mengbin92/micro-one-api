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
	"micro-one-api/domain/authorization"
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

type managedProgressIAMRepo struct {
	adminHTTPLegacyIAMRepo
	readErr error
	reads   int
}

func (*managedProgressIAMRepo) ResourceMode(context.Context, string) (string, error) {
	return "iam", nil
}

func (r *managedProgressIAMRepo) ResourceAuthorization(_ context.Context, raw string, req authorization.ResourceRequest) (authorization.ResourceAuthorization, error) {
	if raw != "verified-admin-session" {
		return authorization.ResourceAuthorization{}, status.Error(codes.Unauthenticated, "user session required")
	}
	allow := authorization.ResourceAuthorization{Mode: "iam", Decision: &authorization.Decision{Allowed: true}, Query: authorization.QueryScope{ActorID: 7, Allow: []authorization.Scope{{Clauses: []authorization.Clause{{All: true}}}}}}
	if req.ExecutionPoint == "admin.console" && req.Operation == "admin.console.enter" {
		return allow, nil
	}
	if req.ExecutionPoint != "admin.subscription.user_subscriptions" || req.Operation != "subscription.user_subscription.read" {
		return authorization.ResourceAuthorization{}, status.Error(codes.PermissionDenied, "unbound operation")
	}
	r.reads++
	return allow, r.readErr
}

type managedProgressBillingClient struct {
	billingv1.BillingServiceClient
	calls  int
	err    error
	absent bool
}

func (c *managedProgressBillingClient) GetSubscriptionUsage(ctx context.Context, req *billingv1.GetSubscriptionUsageRequest, _ ...grpc.CallOption) (*billingv1.GetSubscriptionUsageResponse, error) {
	c.calls++
	md, _ := metadata.FromOutgoingContext(ctx)
	values := md.Get("x-operator-authorization")
	if len(values) != 1 || values[0] != "Bearer verified-admin-session" {
		return nil, status.Error(codes.Unauthenticated, "user session required")
	}
	if req.UserId != 42 || req.SelfRequest {
		return nil, status.Error(codes.PermissionDenied, "managed request required")
	}
	if c.err != nil {
		return nil, c.err
	}
	if c.absent {
		return &billingv1.GetSubscriptionUsageResponse{}, nil
	}
	return &billingv1.GetSubscriptionUsageResponse{Usage: &billingv1.SubscriptionUsage{Id: 3, Status: "active", UsageSource: "billing"}}, nil
}

func TestManagedSubscriptionProgressAuthorization(t *testing.T) {
	for _, tt := range []struct {
		name, token, query   string
		readErr, ownerErr    error
		absent               bool
		status, reads, calls int
	}{
		{name: "other user", token: "verified-admin-session", query: "?user_id=42", status: 200, reads: 1, calls: 1},
		{name: "no subscription", token: "verified-admin-session", query: "?user_id=42", absent: true, status: 200, reads: 1, calls: 1},
		{name: "missing session", query: "?user_id=42", status: 401},
		{name: "invalid session", token: "revoked-session", query: "?user_id=42", status: 401},
		{name: "missing user", token: "verified-admin-session", status: 400},
		{name: "invalid user", token: "verified-admin-session", query: "?user_id=abc", status: 400},
		{name: "negative user", token: "verified-admin-session", query: "?user_id=-1", status: 400},
		{name: "read denied", token: "verified-admin-session", query: "?user_id=42", readErr: status.Error(codes.PermissionDenied, "read denied"), status: 403, reads: 1},
		{name: "authorization unavailable", token: "verified-admin-session", query: "?user_id=42", readErr: status.Error(codes.Unavailable, "identity unavailable"), status: 503, reads: 1},
		{name: "owner denied", token: "verified-admin-session", query: "?user_id=42", ownerErr: status.Error(codes.PermissionDenied, "scope excludes owner"), status: 403, reads: 1, calls: 1},
		{name: "owner unavailable", token: "verified-admin-session", query: "?user_id=42", ownerErr: status.Error(codes.Unavailable, "billing unavailable"), status: 503, reads: 1, calls: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			iam := &managedProgressIAMRepo{readErr: tt.readErr}
			billing := &managedProgressBillingClient{err: tt.ownerErr, absent: tt.absent}
			svc := service.NewAdminService(billing, nil, nil, nil)
			svc.SetIAMService(service.NewIAMAdminService(adminbiz.NewIAMUsecase(iam)))
			svc.SetSubscriptionUsecases(subscriptionbiz.NewSubscriptionUsecase(nil, nil), nil, nil)
			server := NewHTTPServer(":0", svc, nil)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/subscriptions/progress"+tt.query, nil)
			if tt.token != "" {
				req.Header.Set("Authorization", "Bearer "+tt.token)
			}
			req = req.WithContext(metadata.NewOutgoingContext(req.Context(), metadata.Pairs("x-operator-authorization", "Bearer forged-session", "x-operator-authorization", "Bearer duplicate")))
			rec := httptest.NewRecorder()
			server.ServeHTTP(rec, req)
			require.Equal(t, tt.status, rec.Code, rec.Body.String())
			require.Equal(t, tt.reads, iam.reads)
			require.Equal(t, tt.calls, billing.calls)
			if tt.absent {
				require.Contains(t, rec.Body.String(), `"message":"subscription not found"`)
			} else if tt.status == 200 {
				require.Contains(t, rec.Body.String(), `"success":true`)
			}
		})
	}
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
