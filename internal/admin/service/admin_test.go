package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	channelv1 "micro-one-api/api/channel/v1"
	commonv1 "micro-one-api/api/common/v1"

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
