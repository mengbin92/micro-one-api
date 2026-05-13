package service

import (
	"testing"

	commonv1 "micro-one-api/api/common/v1"
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
