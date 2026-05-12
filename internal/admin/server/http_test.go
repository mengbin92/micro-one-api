package server

import (
	"context"
	"encoding/json"
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
	created     *channelv1.CreateChannelRequest
	updated     *channelv1.UpdateChannelRequest
}

func (c *adminHTTPChannelClient) CreateChannel(ctx context.Context, req *channelv1.CreateChannelRequest, opts ...grpc.CallOption) (*channelv1.CreateChannelResponse, error) {
	c.createdName = req.Name
	c.created = req
	return &channelv1.CreateChannelResponse{Success: true, Message: "created", ChannelId: 101}, nil
}

func (c *adminHTTPChannelClient) UpdateChannel(ctx context.Context, req *channelv1.UpdateChannelRequest, opts ...grpc.CallOption) (*channelv1.UpdateChannelResponse, error) {
	c.updated = req
	return &channelv1.UpdateChannelResponse{Success: true, Message: "updated"}, nil
}

func (c *adminHTTPChannelClient) GetChannel(ctx context.Context, req *channelv1.GetChannelRequest, opts ...grpc.CallOption) (*channelv1.GetChannelReply, error) {
	return &channelv1.GetChannelReply{
		Channel: &commonv1.ChannelInfo{
			Id:                 req.ChannelId,
			Name:               "openai",
			Type:               1,
			Status:             1,
			Group:              "default",
			Models:             "gpt-4o",
			BaseUrl:            "https://api.example.com/v1",
			Weight:             3,
			TestTime:           1710000000,
			ResponseTime:       245,
			Balance:            12.5,
			BalanceUpdatedTime: 1710000100,
			UsedQuota:          900,
			ModelMapping:       `{"gpt-4o":"gpt-4o-mini"}`,
			SystemPrompt:       "be concise",
		},
	}, nil
}

func (c *adminHTTPChannelClient) ListChannels(ctx context.Context, req *channelv1.ListChannelsRequest, opts ...grpc.CallOption) (*channelv1.ListChannelsResponse, error) {
	return &channelv1.ListChannelsResponse{
		Channels: []*commonv1.ChannelSummary{
			{
				Id:                 101,
				Name:               "openai",
				Type:               1,
				Status:             1,
				Group:              "default",
				Models:             "gpt-4o",
				Weight:             3,
				TestTime:           1710000000,
				ResponseTime:       245,
				Balance:            12.5,
				BalanceUpdatedTime: 1710000100,
				UsedQuota:          900,
			},
		},
		Total: 1,
	}, nil
}

type adminHTTPBillingClient struct {
	billingv1.BillingServiceClient
	topupUserID       string
	topupAmount       int64
	batchCreated      bool
	deletedRedeemCode string
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

func (c *adminHTTPBillingClient) ListRedeemCodes(ctx context.Context, req *billingv1.ListRedeemCodesRequest, opts ...grpc.CallOption) (*billingv1.ListRedeemCodesResponse, error) {
	return &billingv1.ListRedeemCodesResponse{
		Codes: []*commonv1.RedeemCode{
			{Code: "code-a", Name: "alpha", Amount: 100, Count: 1, Status: 1, CreatedBy: "root", CreatedAt: timestamppb.Now()},
		},
		Total: 1,
	}, nil
}

func (c *adminHTTPBillingClient) SearchRedeemCodes(ctx context.Context, req *billingv1.SearchRedeemCodesRequest, opts ...grpc.CallOption) (*billingv1.SearchRedeemCodesResponse, error) {
	return &billingv1.SearchRedeemCodesResponse{
		Codes: []*commonv1.RedeemCode{
			{Code: req.Keyword + "-code", Name: "matched", Amount: 200, Count: 1, Status: 1, CreatedBy: "root", CreatedAt: timestamppb.Now()},
		},
	}, nil
}

func (c *adminHTTPBillingClient) DeleteRedeemCode(ctx context.Context, req *billingv1.DeleteRedeemCodeRequest, opts ...grpc.CallOption) (*billingv1.DeleteRedeemCodeResponse, error) {
	c.deletedRedeemCode = req.Code
	return &billingv1.DeleteRedeemCodeResponse{Success: true}, nil
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

type adminHTTPSystemOptionsStore struct {
	values map[string]string
}

func (s *adminHTTPSystemOptionsStore) Get(key string) (string, error) {
	return s.values[key], nil
}

func (s *adminHTTPSystemOptionsStore) Set(key, value string) error {
	s.values[key] = value
	return nil
}

func newAdminHTTPTestServer(identity identityv1.IdentityServiceClient, channel channelv1.ChannelServiceClient, billing billingv1.BillingServiceClient) http.Handler {
	adminSvc := service.NewAdminService(billing, identity, channel, nil)
	return NewHTTPServer(":0", adminSvc)
}

func newAdminHTTPOptionTestServer(store service.SystemOptionsStore) http.Handler {
	adminSvc := service.NewAdminService(nil, nil, nil, store)
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

func TestAdminHTTPOptionRequiresAuth(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	srv := newAdminHTTPOptionTestServer(&adminHTTPSystemOptionsStore{values: map[string]string{}})
	req := httptest.NewRequest(http.MethodGet, "/api/option/", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminHTTPOptionGetReturnsOneAPIShape(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	srv := newAdminHTTPOptionTestServer(&adminHTTPSystemOptionsStore{values: map[string]string{
		"site_title":           "Test API",
		"registration_enabled": "false",
	}})
	req := httptest.NewRequest(http.MethodGet, "/api/option/", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"success":true`, `"site_title":"Test API"`, `"registration_enabled":false`} {
		if !strings.Contains(body, want) {
			t.Fatalf("option response missing %s: %s", want, body)
		}
	}
}

func TestAdminHTTPOptionPutAcceptsFlatBody(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	store := &adminHTTPSystemOptionsStore{values: map[string]string{}}
	srv := newAdminHTTPOptionTestServer(store)
	req := httptest.NewRequest(http.MethodPut, "/api/option/", strings.NewReader(`{"site_title":"Updated API","registration_enabled":false}`))
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"success":true`) {
		t.Fatalf("option update response mismatch: %s", rec.Body.String())
	}
	if store.values["site_title"] != "Updated API" || store.values["registration_enabled"] != "false" {
		t.Fatalf("stored values mismatch: %+v", store.values)
	}
}

func TestAdminHTTPOptionOneAPIListAndUpdate(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	store := &adminHTTPSystemOptionsStore{values: map[string]string{
		"SystemName":      "Compat API",
		"RegisterEnabled": "false",
		"Theme":           "default",
		"SMTPToken":       "hidden",
	}}
	srv := newAdminHTTPOptionTestServer(store)
	req := httptest.NewRequest(http.MethodGet, "/api/option/", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var listResp struct {
		Success bool `json:"success"`
		Data    []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode option response: %v, body=%s", err, rec.Body.String())
	}
	options := map[string]string{}
	for _, item := range listResp.Data {
		options[item.Key] = item.Value
	}
	for key, want := range map[string]string{
		"SystemName":      "Compat API",
		"RegisterEnabled": "false",
		"Theme":           "default",
	} {
		if options[key] != want {
			t.Fatalf("option %s = %q, want %q; body=%s", key, options[key], want, rec.Body.String())
		}
	}
	if _, ok := options["SMTPToken"]; ok {
		t.Fatalf("secret option was exposed: %s", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPut, "/api/option/", strings.NewReader(`{"key":"Theme","value":"dark"}`))
	req.Header.Set("Authorization", "Bearer admin-token")
	rec = httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if store.values["Theme"] != "dark" {
		t.Fatalf("Theme was not updated: %+v", store.values)
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

func TestAdminHTTPChannelOneAPIFields(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	channelClient := &adminHTTPChannelClient{}
	srv := newAdminHTTPTestServer(&adminHTTPIdentityClient{}, channelClient, &adminHTTPBillingClient{})
	createBody := `{"name":"openai","type":1,"base_url":"https://api.example.com/v1","key":"sk-test","models":"gpt-4o","group":"default","priority":1,"weight":3,"model_mapping":"{\"gpt-4o\":\"gpt-4o-mini\"}","system_prompt":"be concise"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/channels", strings.NewReader(createBody))
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if channelClient.created == nil {
		t.Fatal("create channel was not called")
	}
	if channelClient.created.Weight != 3 || channelClient.created.ModelMapping != `{"gpt-4o":"gpt-4o-mini"}` || channelClient.created.SystemPrompt != "be concise" {
		t.Fatalf("create request one-api fields mismatch: %+v", channelClient.created)
	}

	updateBody := `{"weight":5,"model_mapping":"{\"gpt-4o\":\"gpt-4o\"}","system_prompt":"updated prompt"}`
	req = httptest.NewRequest(http.MethodPut, "/v1/channels/101", strings.NewReader(updateBody))
	req.Header.Set("Authorization", "Bearer admin-token")
	rec = httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if channelClient.updated == nil {
		t.Fatal("update channel was not called")
	}
	if channelClient.updated.ChannelId != 101 || channelClient.updated.Weight != 5 || channelClient.updated.ModelMapping != `{"gpt-4o":"gpt-4o"}` || channelClient.updated.SystemPrompt != "updated prompt" {
		t.Fatalf("update request one-api fields mismatch: %+v", channelClient.updated)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/channels", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec = httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var list struct {
		Channels []map[string]any `json:"channels"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list response: %v, body=%s", err, rec.Body.String())
	}
	if len(list.Channels) != 1 {
		t.Fatalf("channels length = %d, want 1", len(list.Channels))
	}
	channel := list.Channels[0]
	for _, key := range []string{"weight", "test_time", "response_time", "balance", "balance_updated_time", "used_quota"} {
		if _, ok := channel[key]; !ok {
			t.Fatalf("list channel missing %s: %s", key, rec.Body.String())
		}
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

func TestAdminHTTPRedemptionRequiresAuth(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	srv := newAdminHTTPTestServer(&adminHTTPIdentityClient{}, &adminHTTPChannelClient{}, &adminHTTPBillingClient{})
	req := httptest.NewRequest(http.MethodGet, "/api/redemption/", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminHTTPRedemptionListRoute(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	srv := newAdminHTTPTestServer(&adminHTTPIdentityClient{}, &adminHTTPChannelClient{}, &adminHTTPBillingClient{})
	req := httptest.NewRequest(http.MethodGet, "/api/redemption/", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"code-a"`) {
		t.Fatalf("redemption list response mismatch: %s", rec.Body.String())
	}
}

func TestAdminHTTPRedemptionSearchRoute(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	srv := newAdminHTTPTestServer(&adminHTTPIdentityClient{}, &adminHTTPChannelClient{}, &adminHTTPBillingClient{})
	req := httptest.NewRequest(http.MethodGet, "/api/redemption/search?keyword=alpha", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"alpha-code"`) {
		t.Fatalf("redemption search response mismatch: %s", rec.Body.String())
	}
}

func TestAdminHTTPRedemptionDeleteRoute(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	billingClient := &adminHTTPBillingClient{}
	srv := newAdminHTTPTestServer(&adminHTTPIdentityClient{}, &adminHTTPChannelClient{}, billingClient)
	req := httptest.NewRequest(http.MethodDelete, "/api/redemption/code-a", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if billingClient.deletedRedeemCode != "code-a" {
		t.Fatalf("deleted code = %q, want code-a", billingClient.deletedRedeemCode)
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
