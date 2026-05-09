package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	billingv1 "micro-one-api/api/billing/v1"
	channelv1 "micro-one-api/api/channel/v1"
	commonv1 "micro-one-api/api/common/v1"
	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/internal/admin/service"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
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

func (c *adminHTTPChannelClient) GetChannel(ctx context.Context, req *channelv1.GetChannelRequest, opts ...grpc.CallOption) (*channelv1.GetChannelReply, error) {
	return &channelv1.GetChannelReply{
		Channel: &commonv1.ChannelInfo{
			Id:      req.ChannelId,
			Name:    "openai",
			Type:    1,
			Status:  1,
			Group:   "default",
			Models:  "gpt-4o",
			BaseUrl: "https://api.example.com/v1",
		},
	}, nil
}

func (c *adminHTTPChannelClient) ListChannels(ctx context.Context, req *channelv1.ListChannelsRequest, opts ...grpc.CallOption) (*channelv1.ListChannelsResponse, error) {
	return &channelv1.ListChannelsResponse{
		Channels: []*commonv1.ChannelSummary{
			{Id: 101, Name: "openai", Type: 1, Status: 1, Group: "default", Models: "gpt-4o"},
		},
		Total: 1,
	}, nil
}

type adminHTTPBillingClient struct {
	billingv1.BillingServiceClient
	topupUserID  string
	topupAmount  int64
	batchCreated bool
}

func (c *adminHTTPBillingClient) TopUpQuota(ctx context.Context, req *billingv1.TopUpQuotaRequest, opts ...grpc.CallOption) (*billingv1.TopUpQuotaResponse, error) {
	c.topupUserID = req.UserId
	c.topupAmount = req.Amount
	return &billingv1.TopUpQuotaResponse{Success: true, NewQuota: req.Amount}, nil
}

func (c *adminHTTPBillingClient) GetAccountSnapshot(ctx context.Context, req *billingv1.GetAccountSnapshotRequest, opts ...grpc.CallOption) (*billingv1.GetAccountSnapshotResponse, error) {
	return &billingv1.GetAccountSnapshotResponse{
		Snapshot: &commonv1.AccountSnapshot{
			UserId: req.UserId,
			Quota:  500,
		},
	}, nil
}

func (c *adminHTTPBillingClient) CreateRedeemCodesBatch(ctx context.Context, req *billingv1.CreateRedeemCodesBatchRequest, opts ...grpc.CallOption) (*billingv1.CreateRedeemCodesBatchResponse, error) {
	c.batchCreated = true
	return &billingv1.CreateRedeemCodesBatchResponse{Success: true, Codes: []string{"code-a", "code-b"}}, nil
}

func (c *adminHTTPBillingClient) ListLedger(ctx context.Context, req *billingv1.ListLedgerRequest, opts ...grpc.CallOption) (*billingv1.ListLedgerResponse, error) {
	return &billingv1.ListLedgerResponse{
		Entries: []*commonv1.LedgerEntry{
			{UserId: "42", Type: "topup", Amount: 100, BalanceAfter: 600, CreatedAt: timestamppb.Now()},
			{UserId: "42", Type: "consume", Amount: -25, BalanceAfter: 575, CreatedAt: timestamppb.Now()},
		},
		Total: 2,
	}, nil
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

func TestAdminHTTPPageIsServed(t *testing.T) {
	srv := NewHTTPServer(":0", nil)
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "micro-one-api admin") {
		t.Fatalf("admin page was not served: %s", rec.Body.String())
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

func TestAdminHTTPCreateRedeemCodesBatch(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	billingClient := &adminHTTPBillingClient{}
	srv := newAdminHTTPTestServer(&adminHTTPIdentityClient{}, &adminHTTPChannelClient{}, billingClient)
	req := httptest.NewRequest(http.MethodPost, "/v1/redeem-codes/batch", strings.NewReader(`{"name":"batch","amount":100,"count":1,"batch_size":2,"operator_id":"root"}`))
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if !billingClient.batchCreated {
		t.Fatal("batch create was not called")
	}
	if !strings.Contains(rec.Body.String(), `"code-a"`) {
		t.Fatalf("batch response missing codes: %s", rec.Body.String())
	}
}

func TestAdminHTTPLogStats(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	srv := newAdminHTTPTestServer(&adminHTTPIdentityClient{}, &adminHTTPChannelClient{}, &adminHTTPBillingClient{})
	req := httptest.NewRequest(http.MethodGet, "/api/log/stat", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"total_amount":75`) {
		t.Fatalf("log stats mismatch: %s", rec.Body.String())
	}
}

func TestAdminHTTPTestChannel(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	srv := newAdminHTTPTestServer(&adminHTTPIdentityClient{}, &adminHTTPChannelClient{}, &adminHTTPBillingClient{})
	req := httptest.NewRequest(http.MethodGet, "/api/channel/test/101", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"channel_id":101`) {
		t.Fatalf("channel test response mismatch: %s", rec.Body.String())
	}
}

func TestAdminHTTPResetUserQuotaUsesDelta(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	billingClient := &adminHTTPBillingClient{}
	srv := newAdminHTTPTestServer(&adminHTTPIdentityClient{}, &adminHTTPChannelClient{}, billingClient)
	req := httptest.NewRequest(http.MethodPost, "/v1/users/reset-quota", strings.NewReader(`{"user_id":42,"new_quota":800,"operator_id":"root","remark":"reset"}`))
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if billingClient.topupAmount != 300 {
		t.Fatalf("topup delta = %d, want 300", billingClient.topupAmount)
	}
}
