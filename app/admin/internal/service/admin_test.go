package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	adminv1 "micro-one-api/api/admin/v1"
	billingv1 "micro-one-api/api/billing/v1"
	channelv1 "micro-one-api/api/channel/v1"
	commonv1 "micro-one-api/api/common/v1"
	relayprovider "micro-one-api/domain/upstream/provider"

	"google.golang.org/grpc"
)

func TestBalanceAdapterForChannelUsesProviderTypeDefaults(t *testing.T) {
	tests := []struct {
		name        string
		channelType int32
		want        string
	}{
		{name: "deepseek", channelType: channelTypeDeepSeek, want: "deepseek_balance"},
		{name: "openrouter", channelType: 23, want: "openrouter_credits"},
		{name: "siliconflow", channelType: 24, want: "siliconflow_user_info"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter := balanceAdapterForChannel(&commonv1.ChannelInfo{Type: tt.channelType})
			if adapter == nil {
				t.Fatal("adapter is nil")
			}
			if adapter.name != tt.want {
				t.Fatalf("adapter = %q, want %q", adapter.name, tt.want)
			}
		})
	}
}

func TestSubscriptionProtocolToString(t *testing.T) {
	tests := map[string]string{
		"codex":   "OpenAI",
		"claude":  "Anthropic",
		"zhipu":   "Anthropic",
		"minimax": "Anthropic",
		"kimi":    "Anthropic",
		"unknown": "Unknown",
	}
	for platform, want := range tests {
		if got := subscriptionProtocolToString(platform); got != want {
			t.Fatalf("subscriptionProtocolToString(%q) = %q, want %q", platform, got, want)
		}
	}
}

type adminServiceChannelClient struct {
	channelv1.ChannelServiceClient
	channel   *commonv1.ChannelInfo
	healthReq *channelv1.RecordChannelHealthRequest
}

func (c *adminServiceChannelClient) GetChannel(ctx context.Context, req *channelv1.GetChannelRequest, opts ...grpc.CallOption) (*channelv1.GetChannelReply, error) {
	return &channelv1.GetChannelReply{Channel: c.channel}, nil
}

func (c *adminServiceChannelClient) RecordChannelHealth(ctx context.Context, req *channelv1.RecordChannelHealthRequest, opts ...grpc.CallOption) (*channelv1.RecordChannelHealthResponse, error) {
	c.healthReq = req
	return &channelv1.RecordChannelHealthResponse{Success: true, Message: "ok"}, nil
}

func TestAdminService_TestChannelRecordsHealthSuccess(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %q, want /v1/models", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	defer upstream.Close()

	channelClient := &adminServiceChannelClient{channel: &commonv1.ChannelInfo{
		Id:      9,
		Type:    1,
		Name:    "openai",
		BaseUrl: upstream.URL + "/v1",
		Key:     "sk-test",
		Status:  1,
	}}
	svc := NewAdminService(nil, nil, channelClient, nil)
	result, err := svc.TestChannel(context.Background(), 9)
	if err != nil {
		t.Fatalf("TestChannel() error = %v", err)
	}
	if result["success"] != true {
		t.Fatalf("success = %v", result["success"])
	}
	if channelClient.healthReq == nil || !channelClient.healthReq.Success || channelClient.healthReq.ChannelId != 9 {
		t.Fatalf("health request mismatch: %+v", channelClient.healthReq)
	}
}

func TestAdminService_TestChannelRecordsHealthFailure(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}))
	defer upstream.Close()

	channelClient := &adminServiceChannelClient{channel: &commonv1.ChannelInfo{
		Id:      9,
		Type:    1,
		Name:    "openai",
		BaseUrl: upstream.URL + "/v1",
		Key:     "sk-test",
		Status:  1,
	}}
	svc := NewAdminService(nil, nil, channelClient, nil)
	_, err := svc.TestChannel(context.Background(), 9)
	if err == nil {
		t.Fatal("expected probe error")
	}
	if channelClient.healthReq == nil || channelClient.healthReq.Success || channelClient.healthReq.ChannelId != 9 {
		t.Fatalf("health request mismatch: %+v", channelClient.healthReq)
	}
}

func TestAdminService_TestChannelProbesAnthropicMessages(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
			t.Fatalf("request = %s %s, want POST /v1/messages", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "sk-test" {
			t.Fatalf("x-api-key = %q, want sk-test", got)
		}
		var body struct {
			Model     string `json:"model"`
			MaxTokens int    `json:"max_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body.Model != "Kimi-K2.7-Code" || body.MaxTokens != 1 {
			t.Fatalf("request body = %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_test","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"Kimi-K2.7-Code","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer upstream.Close()

	channelClient := &adminServiceChannelClient{channel: &commonv1.ChannelInfo{
		Id:      9,
		Type:    relayprovider.ChannelTypeAnthropic,
		Name:    "kimi",
		BaseUrl: upstream.URL,
		Key:     "sk-test",
		Models:  "Kimi-K2.7-Code,Kimi-K3",
		Status:  1,
	}}
	svc := NewAdminService(nil, nil, channelClient, nil)
	result, err := svc.TestChannel(context.Background(), 9)
	if err != nil {
		t.Fatalf("TestChannel() error = %v", err)
	}
	if result["skipped"] == true {
		t.Fatalf("anthropic probe was skipped: %+v", result)
	}
	if channelClient.healthReq == nil || !channelClient.healthReq.Success || channelClient.healthReq.ChannelId != 9 {
		t.Fatalf("health request mismatch: %+v", channelClient.healthReq)
	}
}

func TestBalanceEndpointForChannelUsesProviderDefaults(t *testing.T) {
	tests := []struct {
		name        string
		channel     *commonv1.ChannelInfo
		endpointFor func(*commonv1.ChannelInfo) string
		want        string
	}{
		{
			name:        "openai default",
			channel:     &commonv1.ChannelInfo{Type: channelTypeOpenAI},
			endpointFor: openAIDashboardBalanceEndpoint,
			want:        "https://api.openai.com/dashboard/billing/credit_grants",
		},
		{
			name:        "deepseek default",
			channel:     &commonv1.ChannelInfo{Type: channelTypeDeepSeek},
			endpointFor: deepSeekBalanceEndpoint,
			want:        "https://api.deepseek.com/user/balance",
		},
		{
			name:        "openrouter default",
			channel:     &commonv1.ChannelInfo{Type: channelTypeOpenRouter},
			endpointFor: openRouterBalanceEndpoint,
			want:        "https://openrouter.ai/api/v1/credits",
		},
		{
			name:        "openrouter explicit openai-compatible base",
			channel:     &commonv1.ChannelInfo{Type: channelTypeOpenRouter, BaseUrl: "https://openrouter.ai/api/v1"},
			endpointFor: openRouterBalanceEndpoint,
			want:        "https://openrouter.ai/api/v1/credits",
		},
		{
			name:        "siliconflow default",
			channel:     &commonv1.ChannelInfo{Type: channelTypeSiliconFlow},
			endpointFor: siliconFlowBalanceEndpoint,
			want:        "https://api.siliconflow.cn/v1/user/info",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.endpointFor(tt.channel); got != tt.want {
				t.Fatalf("endpoint = %q, want %q", got, tt.want)
			}
		})
	}
}

type aggregateUsageBillingClient struct {
	billingv1.BillingServiceClient
	request *billingv1.AggregateUsageRequest
	buckets []*billingv1.UsageBucket
}

type listLedgerBillingClient struct {
	billingv1.BillingServiceClient
	entry *commonv1.LedgerEntry
}

func (c *listLedgerBillingClient) ListLedger(_ context.Context, _ *billingv1.ListLedgerRequest, _ ...grpc.CallOption) (*billingv1.ListLedgerResponse, error) {
	return &billingv1.ListLedgerResponse{Entries: []*commonv1.LedgerEntry{c.entry}, Total: 1}, nil
}

func TestListLedgerEntriesPreservesUsageAuditContract(t *testing.T) {
	client := &listLedgerBillingClient{entry: &commonv1.LedgerEntry{
		Id:                     "42",
		UsageFieldShape:        "openai_prompt_cached",
		UsageParseStatus:       "ambiguous",
		UsageContractVersion:   1,
		CanonicalPresent:       false,
		SubsetCandidateCost:    123,
		ExclusiveCandidateCost: 456,
		PricingConfigHash:      "hash",
	}}
	svc := NewAdminService(client, nil, nil, nil)

	entries, total, err := svc.ListLedgerEntries(context.Background(), &adminv1.ListLogsRequest{})
	if err != nil {
		t.Fatalf("ListLedgerEntries() error = %v", err)
	}
	if total != 1 || len(entries) != 1 {
		t.Fatalf("ListLedgerEntries() count = %d/%d, want 1/1", total, len(entries))
	}
	got := entries[0]
	checks := map[string]any{
		"usageFieldShape":        "openai_prompt_cached",
		"usageContractVersion":   int32(1),
		"canonicalPresent":       false,
		"subsetCandidateCost":    int64(123),
		"exclusiveCandidateCost": int64(456),
		"pricingConfigHash":      "hash",
	}
	for key, want := range checks {
		if !reflect.DeepEqual(got[key], want) {
			t.Errorf("entry[%q] = %#v, want %#v", key, got[key], want)
		}
	}
}

func (c *aggregateUsageBillingClient) AggregateUsage(_ context.Context, req *billingv1.AggregateUsageRequest, _ ...grpc.CallOption) (*billingv1.AggregateUsageResponse, error) {
	c.request = req
	if c.buckets != nil {
		return &billingv1.AggregateUsageResponse{Buckets: c.buckets}, nil
	}
	return &billingv1.AggregateUsageResponse{Buckets: []*billingv1.UsageBucket{
		{ChannelId: 4, SubscriptionAccountId: 4, Quota: 400},
		{ChannelId: 6, Quota: 100},
		{ChannelId: 5, SubscriptionAccountId: 5, Quota: 200},
		{ChannelId: 1, Quota: 300},
	}}, nil
}

func TestAggregateUsageTopNChannelsExcludesSubscriptionAccounts(t *testing.T) {
	billingClient := &aggregateUsageBillingClient{}
	svc := NewAdminService(billingClient, nil, nil, nil)

	items, err := svc.AggregateUsageTopN(context.Background(), "channel", 5)
	if err != nil {
		t.Fatalf("AggregateUsageTopN() error = %v", err)
	}
	if billingClient.request == nil {
		t.Fatal("AggregateUsage was not called")
	}
	if got, want := billingClient.request.GetGroupBy(), []string{"channel", "subscription_account"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("group_by = %v, want %v", got, want)
	}
	if got := billingClient.request.GetLimit(); got != 0 {
		t.Fatalf("request limit = %d, want 0 so filtering happens before Top-N", got)
	}
	if len(items) != 2 || items[0].ChannelID != 1 || items[1].ChannelID != 6 {
		t.Fatalf("items = %+v, want only regular channels 1 and 6", items)
	}
}

func TestAggregateUsageTopNSubscriptionAccountsSortsByQuota(t *testing.T) {
	billingClient := &aggregateUsageBillingClient{}
	billingClient.buckets = []*billingv1.UsageBucket{
		{SubscriptionAccountId: 2, Quota: 100},
		{SubscriptionAccountId: 5, Quota: 400},
		{SubscriptionAccountId: 4, Quota: 200},
	}
	svc := NewAdminService(billingClient, nil, nil, nil)

	items, err := svc.AggregateUsageTopN(context.Background(), "subscription_account", 5)
	if err != nil {
		t.Fatalf("AggregateUsageTopN() error = %v", err)
	}
	if got := []int64{items[0].SubscriptionAccountID, items[1].SubscriptionAccountID, items[2].SubscriptionAccountID}; !reflect.DeepEqual(got, []int64{5, 4, 2}) {
		t.Fatalf("subscription account order = %v, want [5 4 2]", got)
	}
}
