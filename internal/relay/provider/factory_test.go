package provider

import (
	"testing"
	"time"
)

func TestResolveOpenAICompatibleBaseURL(t *testing.T) {
	tests := []struct {
		name        string
		channelType int32
		baseURL     string
		want        string
	}{
		{name: "explicit", channelType: ChannelTypeDeepSeek, baseURL: "https://custom.example/v1", want: "https://custom.example/v1"},
		{name: "openai", channelType: ChannelTypeOpenAI, want: "https://api.openai.com/v1"},
		{name: "deepseek", channelType: ChannelTypeDeepSeek, want: "https://api.deepseek.com/v1"},
		{name: "mistral", channelType: ChannelTypeMistral, want: "https://api.mistral.ai/v1"},
		{name: "moonshot", channelType: ChannelTypeMoonshot, want: "https://api.moonshot.cn/v1"},
		{name: "groq", channelType: ChannelTypeGroq, want: "https://api.groq.com/openai/v1"},
		{name: "cohere", channelType: ChannelTypeCohere, want: "https://api.cohere.com/compatibility/v1"},
		{name: "zhipu", channelType: ChannelTypeZhipu, want: "https://open.bigmodel.cn/api/paas/v4"},
		{name: "tongyi", channelType: ChannelTypeTongyi, want: "https://dashscope.aliyuncs.com/compatible-mode/v1"},
		{name: "voyageai", channelType: ChannelTypeVoyageAI, want: "https://api.voyageai.com/v1"},
		{name: "unknown", channelType: 999, want: "https://api.openai.com/v1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveOpenAICompatibleBaseURL(tt.channelType, tt.baseURL); got != tt.want {
				t.Fatalf("base url = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProviderFactoryAzureRequiresBaseURL(t *testing.T) {
	factory := NewProviderFactory(time.Second)
	if _, err := factory.CreateProvider(ChannelTypeAzure, "", "key"); err == nil {
		t.Fatal("expected azure provider without base_url to fail")
	}
}
