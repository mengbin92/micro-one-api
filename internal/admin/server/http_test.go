package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	billingv1 "micro-one-api/api/billing/v1"
	channelv1 "micro-one-api/api/channel/v1"
	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/internal/admin/service"

	"google.golang.org/grpc"
)

type adminHTTPIdentityClient struct {
	identityv1.IdentityServiceClient
	deletedUserID int64
}

func (c *adminHTTPIdentityClient) DeleteUser(ctx context.Context, req *identityv1.DeleteUserRequest, opts ...grpc.CallOption) (*identityv1.DeleteUserResponse, error) {
	c.deletedUserID = req.UserId
	return &identityv1.DeleteUserResponse{Success: true, Message: "deleted"}, nil
}

type adminHTTPChannelClient struct {
	channelv1.ChannelServiceClient
	createdName string
}

func (c *adminHTTPChannelClient) CreateChannel(ctx context.Context, req *channelv1.CreateChannelRequest, opts ...grpc.CallOption) (*channelv1.CreateChannelResponse, error) {
	c.createdName = req.Name
	return &channelv1.CreateChannelResponse{Success: true, Message: "created", ChannelId: 101}, nil
}

type adminHTTPBillingClient struct {
	billingv1.BillingServiceClient
	topupUserID string
}

func (c *adminHTTPBillingClient) TopUpQuota(ctx context.Context, req *billingv1.TopUpQuotaRequest, opts ...grpc.CallOption) (*billingv1.TopUpQuotaResponse, error) {
	c.topupUserID = req.UserId
	return &billingv1.TopUpQuotaResponse{Success: true, NewQuota: req.Amount}, nil
}

func newAdminHTTPTestServer(identity identityv1.IdentityServiceClient, channel channelv1.ChannelServiceClient, billing billingv1.BillingServiceClient) http.Handler {
	adminSvc := service.NewAdminService(billing, identity, channel, nil)
	return NewHTTPServer(":0", adminSvc)
}

func TestAdminHTTPStatusIsUnauthenticated(t *testing.T) {
	srv := NewHTTPServer(":0", nil)
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"success":true`) {
		t.Fatalf("status response missing success: %s", rec.Body.String())
	}
}

func TestAdminHTTPCreateChannel(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	channelClient := &adminHTTPChannelClient{}
	srv := newAdminHTTPTestServer(&adminHTTPIdentityClient{}, channelClient, &adminHTTPBillingClient{})
	req := httptest.NewRequest(http.MethodPost, "/v1/channels", strings.NewReader(`{"name":"openai","type":1,"base_url":"https://api.example.com/v1","key":"sk-test","models":"gpt-4o","group":"default","priority":1}`))
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if channelClient.createdName != "openai" {
		t.Fatalf("created channel name = %q", channelClient.createdName)
	}
	if !strings.Contains(rec.Body.String(), `"channel_id":101`) {
		t.Fatalf("create response missing channel id: %s", rec.Body.String())
	}
}

func TestAdminHTTPDeleteUserByPathID(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	identityClient := &adminHTTPIdentityClient{}
	srv := newAdminHTTPTestServer(identityClient, &adminHTTPChannelClient{}, &adminHTTPBillingClient{})
	req := httptest.NewRequest(http.MethodDelete, "/v1/users/42", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if identityClient.deletedUserID != 42 {
		t.Fatalf("deleted user id = %d, want 42", identityClient.deletedUserID)
	}
}

func TestAdminHTTPTopUpCompatRoute(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	billingClient := &adminHTTPBillingClient{}
	srv := newAdminHTTPTestServer(&adminHTTPIdentityClient{}, &adminHTTPChannelClient{}, billingClient)
	req := httptest.NewRequest(http.MethodPost, "/api/topup", strings.NewReader(`{"user_id":"42","amount":1000,"operator_id":"root","remark":"manual"}`))
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if billingClient.topupUserID != "42" {
		t.Fatalf("topup user id = %q, want 42", billingClient.topupUserID)
	}
}
