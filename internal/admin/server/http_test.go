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
	createdUser   *identityv1.CreateUserRequest
	updatedUser   *identityv1.UpdateUserRequest
	userStatuses  []int32
}

func (c *adminHTTPIdentityClient) GetUser(ctx context.Context, req *identityv1.GetUserRequest, opts ...grpc.CallOption) (*identityv1.GetUserReply, error) {
	return &identityv1.GetUserReply{
		User: &commonv1.UserInfo{
			Id:          req.UserId,
			Username:    "alice",
			DisplayName: "Alice",
			Email:       "alice@example.com",
			Group:       "default",
			Status:      1,
			Quota:       500,
		},
	}, nil
}

func (c *adminHTTPIdentityClient) ListUsers(ctx context.Context, req *identityv1.ListUsersRequest, opts ...grpc.CallOption) (*identityv1.ListUsersResponse, error) {
	username := "alice"
	if req.Keyword != "" {
		username = req.Keyword
	}
	return &identityv1.ListUsersResponse{
		Users: []*commonv1.UserInfo{
			{Id: 42, Username: username, DisplayName: "Alice", Email: "alice@example.com", Group: "default", Status: 1},
		},
		Total: 1,
	}, nil
}

func (c *adminHTTPIdentityClient) CreateUser(ctx context.Context, req *identityv1.CreateUserRequest, opts ...grpc.CallOption) (*identityv1.CreateUserResponse, error) {
	c.createdUser = req
	return &identityv1.CreateUserResponse{Success: true, Message: "created", UserId: 42}, nil
}

func (c *adminHTTPIdentityClient) UpdateUser(ctx context.Context, req *identityv1.UpdateUserRequest, opts ...grpc.CallOption) (*identityv1.UpdateUserResponse, error) {
	c.updatedUser = req
	if req.Status != 0 {
		c.userStatuses = append(c.userStatuses, req.Status)
	}
	return &identityv1.UpdateUserResponse{Success: true, Message: "updated"}, nil
}

func (c *adminHTTPIdentityClient) DeleteUser(ctx context.Context, req *identityv1.DeleteUserRequest, opts ...grpc.CallOption) (*identityv1.DeleteUserResponse, error) {
	c.deletedUserID = req.UserId
	return &identityv1.DeleteUserResponse{Success: true, Message: "deleted"}, nil
}

type adminHTTPChannelClient struct {
	channelv1.ChannelServiceClient
	createdName            string
	created                *channelv1.CreateChannelRequest
	updated                *channelv1.UpdateChannelRequest
	deletedID              int64
	deletedIDs             []int64
	baseURL                string
	chType                 int32
	statuses               []int32
	existingFailureCount   int32
	existingLastError      string
	existingLastSuccess    int64
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

func (c *adminHTTPChannelClient) DeleteChannel(ctx context.Context, req *channelv1.DeleteChannelRequest, opts ...grpc.CallOption) (*channelv1.DeleteChannelResponse, error) {
	c.deletedID = req.ChannelId
	c.deletedIDs = append(c.deletedIDs, req.ChannelId)
	return &channelv1.DeleteChannelResponse{Success: true, Message: "deleted"}, nil
}

func (c *adminHTTPChannelClient) ChangeChannelStatus(ctx context.Context, req *channelv1.ChangeChannelStatusRequest, opts ...grpc.CallOption) (*channelv1.ChangeChannelStatusResponse, error) {
	c.statuses = append(c.statuses, req.Status)
	return &channelv1.ChangeChannelStatusResponse{Success: true, Message: "updated"}, nil
}

func (c *adminHTTPChannelClient) GetChannel(ctx context.Context, req *channelv1.GetChannelRequest, opts ...grpc.CallOption) (*channelv1.GetChannelReply, error) {
	baseURL := c.baseURL
	if baseURL == "" {
		baseURL = "https://api.example.com/v1"
	}
	chType := c.chType
	if chType == 0 {
		chType = 1
	}
	return &channelv1.GetChannelReply{
		Channel: &commonv1.ChannelInfo{
			Id:                                req.ChannelId,
			Name:                              "openai",
			Type:                              chType,
			Status:                            1,
			Group:                             "default",
			Models:                            "gpt-4o",
			BaseUrl:                           baseURL,
			Key:                               "sk-test",
			Weight:                            3,
			TestTime:                          1710000000,
			ResponseTime:                      245,
			Balance:                           12.5,
			BalanceUpdatedTime:                1710000100,
			UsedQuota:                         900,
			ModelMapping:                      `{"gpt-4o":"gpt-4o-mini"}`,
			SystemPrompt:                      "be concise",
			BalanceRefreshLastError:           c.existingLastError,
			BalanceRefreshLastSuccessTime:     c.existingLastSuccess,
			ConsecutiveBalanceRefreshFailures: c.existingFailureCount,
		},
	}, nil
}

func (c *adminHTTPChannelClient) ListChannels(ctx context.Context, req *channelv1.ListChannelsRequest, opts ...grpc.CallOption) (*channelv1.ListChannelsResponse, error) {
	active := &commonv1.ChannelSummary{
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
	}
	disabled := &commonv1.ChannelSummary{
		Id:     102,
		Name:   "disabled",
		Type:   1,
		Status: 2,
		Group:  "default",
		Models: "gpt-4o",
	}
	channels := []*commonv1.ChannelSummary{active}
	if req.Status == 2 {
		channels = []*commonv1.ChannelSummary{disabled}
	} else if req.Status != 0 && req.Status != 1 {
		channels = nil
	}
	return &channelv1.ListChannelsResponse{
		Channels: channels,
		Total:    int64(len(channels)),
	}, nil
}

type adminHTTPBillingClient struct {
	billingv1.BillingServiceClient
	topupUserID         string
	topupAmount         int64
	batchCreated        bool
	deletedRedeemCode   string
	reconRuns           []*billingv1.ReconciliationRun
	reconRunsByID       map[int64]*billingv1.ReconciliationRun
	reconListErr        error
	reconGetErr         error
	reconListLastReq    *billingv1.ListReconciliationRunsRequest
	reconGetLastRunID   int64
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

func (c *adminHTTPBillingClient) ListReconciliationRuns(ctx context.Context, req *billingv1.ListReconciliationRunsRequest, opts ...grpc.CallOption) (*billingv1.ListReconciliationRunsResponse, error) {
	c.reconListLastReq = req
	if c.reconListErr != nil {
		return nil, c.reconListErr
	}
	return &billingv1.ListReconciliationRunsResponse{Runs: c.reconRuns, Total: int64(len(c.reconRuns))}, nil
}

func (c *adminHTTPBillingClient) GetReconciliationRun(ctx context.Context, req *billingv1.GetReconciliationRunRequest, opts ...grpc.CallOption) (*billingv1.GetReconciliationRunResponse, error) {
	c.reconGetLastRunID = req.GetRunId()
	if c.reconGetErr != nil {
		return nil, c.reconGetErr
	}
	run, ok := c.reconRunsByID[req.GetRunId()]
	if !ok {
		return &billingv1.GetReconciliationRunResponse{}, nil
	}
	return &billingv1.GetReconciliationRunResponse{Run: run}, nil
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

func newAdminHTTPTestServerWithOptions(identity identityv1.IdentityServiceClient, channel channelv1.ChannelServiceClient, billing billingv1.BillingServiceClient, store service.SystemOptionsStore) http.Handler {
	adminSvc := service.NewAdminService(billing, identity, channel, store)
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
	body := rec.Body.String()
	for _, want := range []string{
		`<!doctype html`,
		`<div id="root">`,
		`/assets/`,
	} {
		if !strings.Contains(strings.ToLower(body), strings.ToLower(want)) {
			t.Fatalf("admin SPA shell missing %q: %s", want, body)
		}
	}
}

func TestAdminHTTPPageSPARouteFallback(t *testing.T) {
	srv := NewHTTPServer(":0", nil)
	for _, path := range []string{"/", "/login", "/dashboard", "/tokens"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("path %s status = %d, want 200, body=%s", path, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `<div id="root">`) {
			t.Fatalf("path %s did not fall back to SPA shell: %s", path, rec.Body.String())
		}
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

func TestAdminHTTPContentRoutesExposeAndManageContent(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	store := &adminHTTPSystemOptionsStore{values: map[string]string{
		"Notice":          "hello users",
		"About":           "about text",
		"HomePageContent": "home content",
	}}
	srv := newAdminHTTPOptionTestServer(store)

	req := httptest.NewRequest(http.MethodGet, "/api/notice", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"data":"hello users"`) {
		t.Fatalf("notice get mismatch: status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPut, "/api/notice", strings.NewReader(`{"content":"updated notice"}`))
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated notice update status = %d, want 401, body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPut, "/api/notice", strings.NewReader(`{"content":"updated notice"}`))
	req.Header.Set("Authorization", "Bearer admin-token")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"success":true`) {
		t.Fatalf("notice update mismatch: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.values["Notice"] != "updated notice" {
		t.Fatalf("Notice was not updated: %+v", store.values)
	}

	for path, want := range map[string]string{
		"/api/about":             `"data":"about text"`,
		"/api/home_page_content": `"data":"home content"`,
	} {
		req = httptest.NewRequest(http.MethodGet, path, nil)
		rec = httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("%s get mismatch: status=%d body=%s", path, rec.Code, rec.Body.String())
		}
	}
}

func TestAdminHTTPGroupManagementUsesGroupRatioOption(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	store := &adminHTTPSystemOptionsStore{values: map[string]string{
		"GroupRatio": `{"default":1,"vip":2}`,
	}}
	srv := newAdminHTTPOptionTestServer(store)

	req := httptest.NewRequest(http.MethodGet, "/api/group/", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var groupList struct {
		Success bool     `json:"success"`
		Data    []string `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &groupList); err != nil {
		t.Fatalf("decode group list: %v, body=%s", err, rec.Body.String())
	}
	if !groupList.Success || !stringSliceContains(groupList.Data, "default") || !stringSliceContains(groupList.Data, "vip") {
		t.Fatalf("group list response mismatch: %+v body=%s", groupList, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/group/?with_ratio=true", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("ratio status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{`"success":true`, `"group":"default"`, `"ratio":1`, `"group":"vip"`, `"ratio":2`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("ratio group list response missing %s: %s", want, rec.Body.String())
		}
	}

	req = httptest.NewRequest(http.MethodPost, "/api/group", strings.NewReader(`{"group":"vip","ratio":2}`))
	req.Header.Set("Authorization", "Bearer admin-token")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"success":true`) {
		t.Fatalf("group create/update response mismatch: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.values["GroupRatio"] != `{"default":1,"vip":2}` {
		t.Fatalf("GroupRatio after post = %s", store.values["GroupRatio"])
	}

	req = httptest.NewRequest(http.MethodPut, "/api/group", strings.NewReader(`{"group":"vip","ratio":3.5}`))
	req.Header.Set("Authorization", "Bearer admin-token")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"success":true`) {
		t.Fatalf("group update response mismatch: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.values["GroupRatio"] != `{"default":1,"vip":3.5}` {
		t.Fatalf("GroupRatio after put = %s", store.values["GroupRatio"])
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/group?group=vip", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"success":true`) {
		t.Fatalf("group delete response mismatch: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.values["GroupRatio"] != `{"default":1}` {
		t.Fatalf("GroupRatio after delete = %s", store.values["GroupRatio"])
	}
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
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

func TestAdminHTTPOneAPIChannelRoutes(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	channelClient := &adminHTTPChannelClient{}
	srv := newAdminHTTPTestServer(&adminHTTPIdentityClient{}, channelClient, &adminHTTPBillingClient{})

	req := httptest.NewRequest(http.MethodGet, "/api/channel/?p=0", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"success":true`) || !strings.Contains(rec.Body.String(), `"data"`) || !strings.Contains(rec.Body.String(), `"weight":3`) {
		t.Fatalf("list response is not one-api shaped: %s", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/channel/search?keyword=openai", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"success":true`) || !strings.Contains(rec.Body.String(), `"openai"`) {
		t.Fatalf("search response mismatch: status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/channel/101", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"model_mapping":"{\"gpt-4o\":\"gpt-4o-mini\"}"`) {
		t.Fatalf("get response mismatch: status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/channel/", strings.NewReader(`{"name":"openai","type":1,"base_url":"https://api.example.com/v1","key":"sk-test","models":"gpt-4o","group":"default","priority":1}`))
	req.Header.Set("Authorization", "Bearer admin-token")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || channelClient.createdName != "openai" || !strings.Contains(rec.Body.String(), `"success":true`) {
		t.Fatalf("create response mismatch: status=%d created=%q body=%s", rec.Code, channelClient.createdName, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPut, "/api/channel/", strings.NewReader(`{"id":101,"name":"openai-updated","models":"gpt-4o"}`))
	req.Header.Set("Authorization", "Bearer admin-token")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || channelClient.updated == nil || channelClient.updated.ChannelId != 101 {
		t.Fatalf("update response mismatch: status=%d updated=%+v body=%s", rec.Code, channelClient.updated, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/channel/101", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || channelClient.deletedID != 101 {
		t.Fatalf("delete response mismatch: status=%d deleted=%d body=%s", rec.Code, channelClient.deletedID, rec.Body.String())
	}

	for _, tc := range []struct {
		path       string
		wantStatus int32
	}{
		{"/api/channel/disable/101", 2},
		{"/api/channel/enable/101", 1},
	} {
		req = httptest.NewRequest(http.MethodPost, tc.path, nil)
		req.Header.Set("Authorization", "Bearer admin-token")
		rec = httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"success":true`) {
			t.Fatalf("%s response mismatch: status=%d body=%s", tc.path, rec.Code, rec.Body.String())
		}
		if got := channelClient.statuses[len(channelClient.statuses)-1]; got != tc.wantStatus {
			t.Fatalf("%s status = %d, want %d", tc.path, got, tc.wantStatus)
		}
	}
}

func TestAdminHTTPOneAPIChannelBulkCompatibilityRoutes(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	channelClient := &adminHTTPChannelClient{}
	srv := newAdminHTTPTestServer(&adminHTTPIdentityClient{}, channelClient, &adminHTTPBillingClient{})

	req := httptest.NewRequest(http.MethodDelete, "/api/channel/disabled", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"success":true`) || !strings.Contains(rec.Body.String(), `"data":1`) {
		t.Fatalf("disabled cleanup response mismatch: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(channelClient.deletedIDs) != 1 || channelClient.deletedIDs[0] != 102 {
		t.Fatalf("disabled cleanup deleted IDs = %+v, want [102]", channelClient.deletedIDs)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/channel/batch", strings.NewReader(`{"ids":[101,103]}`))
	req.Header.Set("Authorization", "Bearer admin-token")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"success":true`) || !strings.Contains(rec.Body.String(), `"data":2`) {
		t.Fatalf("batch delete response mismatch: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(channelClient.deletedIDs) != 3 || channelClient.deletedIDs[1] != 101 || channelClient.deletedIDs[2] != 103 {
		t.Fatalf("batch delete deleted IDs = %+v, want suffix [101 103]", channelClient.deletedIDs)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/channel/fix", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"success":true`) || !strings.Contains(rec.Body.String(), `"data":0`) {
		t.Fatalf("fix response mismatch: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminHTTPOneAPIChannelModelsReturnsModelCatalog(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	srv := newAdminHTTPTestServer(&adminHTTPIdentityClient{}, &adminHTTPChannelClient{}, &adminHTTPBillingClient{})

	req := httptest.NewRequest(http.MethodGet, "/api/channel/models", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Object string `json:"object"`
		Data   []struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			OwnedBy string `json:"owned_by"`
			Root    string `json:"root"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v, body=%s", err, rec.Body.String())
	}
	if body.Object != "list" {
		t.Fatalf("object = %q, want list; body=%s", body.Object, rec.Body.String())
	}
	found := false
	for _, model := range body.Data {
		if model.ID == "gpt-4o-mini" && model.Object == "model" && model.OwnedBy != "" && model.Root == model.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("model catalog missing gpt-4o-mini model object: %s", rec.Body.String())
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

func TestAdminHTTPOneAPIUserRoutes(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	identityClient := &adminHTTPIdentityClient{}
	srv := newAdminHTTPTestServer(identityClient, &adminHTTPChannelClient{}, &adminHTTPBillingClient{})

	req := httptest.NewRequest(http.MethodGet, "/api/user/?p=0", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"success":true`) || !strings.Contains(rec.Body.String(), `"username":"alice"`) {
		t.Fatalf("list response mismatch: status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/user/search?keyword=bob", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"username":"bob"`) {
		t.Fatalf("search response mismatch: status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/user/42", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"email":"alice@example.com"`) {
		t.Fatalf("get response mismatch: status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/user/", strings.NewReader(`{"username":"alice","display_name":"Alice","email":"alice@example.com","password":"secret","group":"default","quota":500}`))
	req.Header.Set("Authorization", "Bearer admin-token")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || identityClient.createdUser == nil || identityClient.createdUser.Username != "alice" {
		t.Fatalf("create response mismatch: status=%d created=%+v body=%s", rec.Code, identityClient.createdUser, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPut, "/api/user/", strings.NewReader(`{"id":42,"display_name":"Alice Updated","email":"alice@example.com","group":"default","status":1}`))
	req.Header.Set("Authorization", "Bearer admin-token")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || identityClient.updatedUser == nil || identityClient.updatedUser.UserId != 42 {
		t.Fatalf("update response mismatch: status=%d updated=%+v body=%s", rec.Code, identityClient.updatedUser, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/user/42", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || identityClient.deletedUserID != 42 {
		t.Fatalf("delete response mismatch: status=%d deleted=%d body=%s", rec.Code, identityClient.deletedUserID, rec.Body.String())
	}

	for _, tc := range []struct {
		path       string
		wantStatus int32
	}{
		{"/api/user/disable/42", 2},
		{"/api/user/enable/42", 1},
	} {
		req = httptest.NewRequest(http.MethodPost, tc.path, nil)
		req.Header.Set("Authorization", "Bearer admin-token")
		rec = httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"success":true`) {
			t.Fatalf("%s response mismatch: status=%d body=%s", tc.path, rec.Code, rec.Body.String())
		}
		if got := identityClient.userStatuses[len(identityClient.userStatuses)-1]; got != tc.wantStatus {
			t.Fatalf("%s status = %d, want %d", tc.path, got, tc.wantStatus)
		}
	}
}

func TestAdminHTTPOneAPIUserManageRoute(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	identityClient := &adminHTTPIdentityClient{}
	srv := newAdminHTTPTestServer(identityClient, &adminHTTPChannelClient{}, &adminHTTPBillingClient{})

	for _, tc := range []struct {
		action     string
		wantStatus int32
	}{
		{"disable", 2},
		{"enable", 1},
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/user/manage", strings.NewReader(`{"username":"alice","action":"`+tc.action+`"}`))
		req.Header.Set("Authorization", "Bearer admin-token")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"success":true`) {
			t.Fatalf("%s response mismatch: status=%d body=%s", tc.action, rec.Code, rec.Body.String())
		}
		if got := identityClient.userStatuses[len(identityClient.userStatuses)-1]; got != tc.wantStatus {
			t.Fatalf("%s status = %d, want %d", tc.action, got, tc.wantStatus)
		}
		for _, want := range []string{`"status":`, `"role":`} {
			if !strings.Contains(rec.Body.String(), want) {
				t.Fatalf("%s response missing %s: %s", tc.action, want, rec.Body.String())
			}
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/api/user/manage", strings.NewReader(`{"username":"alice","action":"delete"}`))
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || identityClient.deletedUserID != 42 || !strings.Contains(rec.Body.String(), `"success":true`) {
		t.Fatalf("delete response mismatch: status=%d deleted=%d body=%s", rec.Code, identityClient.deletedUserID, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/user/manage", strings.NewReader(`{"username":"alice","action":"promote"}`))
	req.Header.Set("Authorization", "Bearer admin-token")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"success":false`) {
		t.Fatalf("unsupported action response mismatch: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminHTTPOneAPIRootAliasesAcceptNoTrailingSlash(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	srv := newAdminHTTPTestServer(&adminHTTPIdentityClient{}, &adminHTTPChannelClient{}, &adminHTTPBillingClient{})

	for _, path := range []string{
		"/api/user",
		"/api/channel",
		"/api/option",
		"/api/log",
		"/api/redemption",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer admin-token")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)

		if rec.Code == http.StatusNotFound {
			t.Fatalf("%s returned 404, body=%s", path, rec.Body.String())
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200, body=%s", path, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"success":true`) {
			t.Fatalf("%s response is not one-api shaped: %s", path, rec.Body.String())
		}
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

func TestAdminHTTPOneAPILogRoutes(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	srv := newAdminHTTPTestServer(&adminHTTPIdentityClient{}, &adminHTTPChannelClient{}, &adminHTTPBillingClient{})

	req := httptest.NewRequest(http.MethodGet, "/api/log/?p=0&type=topup", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"success":true`) || !strings.Contains(rec.Body.String(), `"type":"topup"`) {
		t.Fatalf("log list response mismatch: status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/log/search?user_id=42&type=consume", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"success":true`) || !strings.Contains(rec.Body.String(), `"type":"consume"`) {
		t.Fatalf("log search response mismatch: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminHTTPOneAPILogDeleteProxiesToLogService(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	t.Setenv("SERVICE_TOKEN", "service-token")

	var gotAuth string
	var gotQuery string
	logService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/v1/logs" {
			t.Fatalf("unexpected log-service request: %s %s", r.Method, r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"deleted":2}`))
	}))
	defer logService.Close()
	t.Setenv("LOG_HTTP_ENDPOINT", logService.URL)

	srv := newAdminHTTPTestServer(&adminHTTPIdentityClient{}, &adminHTTPChannelClient{}, &adminHTTPBillingClient{})
	req := httptest.NewRequest(http.MethodDelete, "/api/log/?type=info&user_id=42&start_time=1710000000&end_time=1710000100", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if gotAuth != "Bearer service-token" {
		t.Fatalf("auth = %q", gotAuth)
	}
	for _, want := range []string{"type=info", "user_id=42", "start_time=1710000000", "end_time=1710000100"} {
		if !strings.Contains(gotQuery, want) {
			t.Fatalf("log-service query missing %s: %s", want, gotQuery)
		}
	}
	if !strings.Contains(rec.Body.String(), `"deleted":2`) {
		t.Fatalf("delete response mismatch: %s", rec.Body.String())
	}
}

func TestAdminHTTPOneAPILogDeleteRequiresEndTime(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	t.Setenv("SERVICE_TOKEN", "service-token")

	called := false
	logService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer logService.Close()
	t.Setenv("LOG_HTTP_ENDPOINT", logService.URL)

	srv := newAdminHTTPTestServer(&adminHTTPIdentityClient{}, &adminHTTPChannelClient{}, &adminHTTPBillingClient{})
	req := httptest.NewRequest(http.MethodDelete, "/api/log/?type=info", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
	if called {
		t.Fatal("log-service should not be called without end_time")
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

func TestAdminHTTPUpdateChannelBalanceRefreshesSupportedChannel(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/dashboard/billing/credit_grants" {
			t.Fatalf("upstream path = %q, want /dashboard/billing/credit_grants", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Fatalf("authorization header = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"total_available":42.5}`))
	}))
	defer upstream.Close()

	channelClient := &adminHTTPChannelClient{baseURL: upstream.URL + "/v1"}
	srv := newAdminHTTPTestServer(&adminHTTPIdentityClient{}, channelClient, &adminHTTPBillingClient{})
	req := httptest.NewRequest(http.MethodGet, "/api/channel/update_balance/101", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if channelClient.updated == nil || channelClient.updated.ChannelId != 101 {
		t.Fatalf("channel update was not called: %+v", channelClient.updated)
	}
	for _, want := range []string{`"success":true`, `"channel_id":101`, `"balance":42.5`, `"provider":"openai_dashboard"`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("balance response missing %s: %s", want, rec.Body.String())
		}
	}
}

func TestAdminHTTPUpdateChannelBalanceUnsupportedProviderStaysEnabled(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	// type 99 has no balance adapter registered.
	channelClient := &adminHTTPChannelClient{chType: 99}
	srv := newAdminHTTPTestServer(&adminHTTPIdentityClient{}, channelClient, &adminHTTPBillingClient{})

	req := httptest.NewRequest(http.MethodGet, "/api/channel/update_balance/101", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if channelClient.updated != nil {
		t.Fatalf("UpdateChannel should not be called for unsupported provider, got %+v", channelClient.updated)
	}
	if len(channelClient.statuses) != 0 {
		t.Fatalf("ChangeChannelStatus should not be called for unsupported provider, got %v", channelClient.statuses)
	}
	for _, want := range []string{`"success":true`, `"skipped":true`, "balance refresh not supported"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("unsupported response missing %s: %s", want, rec.Body.String())
		}
	}
}

func TestAdminHTTPUpdateChannelBalanceTransientErrorIncrementsCounterWithoutDisabling(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream is unavailable", http.StatusServiceUnavailable)
	}))
	defer upstream.Close()

	channelClient := &adminHTTPChannelClient{baseURL: upstream.URL + "/v1"}
	// Auto-disable disabled by default: no system options store wired.
	srv := newAdminHTTPTestServer(&adminHTTPIdentityClient{}, channelClient, &adminHTTPBillingClient{})

	req := httptest.NewRequest(http.MethodGet, "/api/channel/update_balance/101", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if channelClient.updated == nil {
		t.Fatal("UpdateChannel should be called to record the failure")
	}
	if !channelClient.updated.SetBalanceRefreshFields {
		t.Fatal("UpdateChannel must opt into balance-refresh field writes")
	}
	if channelClient.updated.BalanceRefreshLastError == "" {
		t.Fatal("balance_refresh_last_error should be set on transient failure")
	}
	if channelClient.updated.ConsecutiveBalanceRefreshFailures != 1 {
		t.Fatalf("consecutive failures = %d, want 1", channelClient.updated.ConsecutiveBalanceRefreshFailures)
	}
	if len(channelClient.statuses) != 0 {
		t.Fatalf("ChangeChannelStatus should not be called when auto-disable is off: %v", channelClient.statuses)
	}
}

func TestAdminHTTPUpdateChannelBalancePersistentErrorTriggersAutoDisable(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream is unavailable", http.StatusServiceUnavailable)
	}))
	defer upstream.Close()

	channelClient := &adminHTTPChannelClient{
		baseURL:              upstream.URL + "/v1",
		existingFailureCount: 2,
	}
	store := &adminHTTPSystemOptionsStore{values: map[string]string{
		"AutomaticDisableChannelEnabled": "true",
		"ChannelDisableThreshold":        "3",
	}}
	srv := newAdminHTTPTestServerWithOptions(&adminHTTPIdentityClient{}, channelClient, &adminHTTPBillingClient{}, store)

	req := httptest.NewRequest(http.MethodGet, "/api/channel/update_balance/101", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if channelClient.updated == nil {
		t.Fatal("UpdateChannel should be called to record the failure")
	}
	if channelClient.updated.ConsecutiveBalanceRefreshFailures != 3 {
		t.Fatalf("consecutive failures = %d, want 3", channelClient.updated.ConsecutiveBalanceRefreshFailures)
	}
	if len(channelClient.statuses) != 1 || channelClient.statuses[0] != 2 {
		t.Fatalf("expected one ChangeChannelStatus(status=2), got %v", channelClient.statuses)
	}
	if !strings.Contains(rec.Body.String(), `"disabled":true`) {
		t.Fatalf("response should report disabled: %s", rec.Body.String())
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

func TestAdminHTTPReconciliationRunsRequiresAdminAuth(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	srv := newAdminHTTPTestServer(&adminHTTPIdentityClient{}, &adminHTTPChannelClient{}, &adminHTTPBillingClient{})

	req := httptest.NewRequest(http.MethodGet, "/api/reconciliation", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 without admin token, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminHTTPReconciliationRunsListEmpty(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	billingClient := &adminHTTPBillingClient{}
	srv := newAdminHTTPTestServer(&adminHTTPIdentityClient{}, &adminHTTPChannelClient{}, billingClient)

	req := httptest.NewRequest(http.MethodGet, "/api/reconciliation", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{`"success":true`, `"runs":[]`, `"total":0`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("empty list missing %s: %s", want, rec.Body.String())
		}
	}
}

func TestAdminHTTPReconciliationRunsListMixed(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	billingClient := &adminHTTPBillingClient{
		reconRuns: []*billingv1.ReconciliationRun{
			{RunId: 2, RunAt: 1715000000, ExpiredCleaned: 0, TotalAccounts: 10, DiscrepancyCount: 0},
			{
				RunId: 1, RunAt: 1714900000, ExpiredCleaned: 3, TotalAccounts: 10, DiscrepancyCount: 1,
				Discrepancies: []*billingv1.ReconciliationDiscrepancy{
					{UserId: "42", ExpectedQuota: 1000, ActualQuota: 750, LedgerNetAmount: 1000, FrozenQuota: 0},
				},
			},
		},
	}
	srv := newAdminHTTPTestServer(&adminHTTPIdentityClient{}, &adminHTTPChannelClient{}, billingClient)

	req := httptest.NewRequest(http.MethodGet, "/api/reconciliation?page=1&page_size=20", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if billingClient.reconListLastReq == nil || billingClient.reconListLastReq.GetPage() != 1 || billingClient.reconListLastReq.GetPageSize() != 20 {
		t.Fatalf("ListReconciliationRuns called with %+v", billingClient.reconListLastReq)
	}
	for _, want := range []string{`"success":true`, `"run_id":2`, `"run_id":1`, `"discrepancy_count":1`, `"user_id":"42"`, `"expected_quota":1000`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("mixed list missing %s: %s", want, rec.Body.String())
		}
	}
}

func TestAdminHTTPReconciliationRunByIDReturnsDiscrepancies(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	billingClient := &adminHTTPBillingClient{
		reconRunsByID: map[int64]*billingv1.ReconciliationRun{
			7: {
				RunId: 7, RunAt: 1714800000, ExpiredCleaned: 1, TotalAccounts: 5, TotalReservations: 2, DiscrepancyCount: 1,
				Discrepancies: []*billingv1.ReconciliationDiscrepancy{
					{UserId: "9", ExpectedQuota: 200, ActualQuota: 50, LedgerNetAmount: 200, FrozenQuota: 0},
				},
			},
		},
	}
	srv := newAdminHTTPTestServer(&adminHTTPIdentityClient{}, &adminHTTPChannelClient{}, billingClient)

	req := httptest.NewRequest(http.MethodGet, "/api/reconciliation/7", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if billingClient.reconGetLastRunID != 7 {
		t.Fatalf("GetReconciliationRun called with run_id=%d, want 7", billingClient.reconGetLastRunID)
	}
	for _, want := range []string{`"run_id":7`, `"discrepancy_count":1`, `"user_id":"9"`, `"actual_quota":50`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("drill-down response missing %s: %s", want, rec.Body.String())
		}
	}
}

func TestAdminHTTPReconciliationRunByIDNotFound(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	billingClient := &adminHTTPBillingClient{reconRunsByID: map[int64]*billingv1.ReconciliationRun{}}
	srv := newAdminHTTPTestServer(&adminHTTPIdentityClient{}, &adminHTTPChannelClient{}, billingClient)

	req := httptest.NewRequest(http.MethodGet, "/api/reconciliation/999", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", rec.Code, rec.Body.String())
	}
}
