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
		{name: "openrouter", channelType: ChannelTypeOpenRouter, want: "https://openrouter.ai/api/v1"},
		{name: "siliconflow", channelType: ChannelTypeSiliconFlow, want: "https://api.siliconflow.cn/v1"},
		{name: "ollama", channelType: ChannelTypeOllama, want: "http://localhost:11434/v1"},
		{name: "doubao", channelType: ChannelTypeDoubao, want: "https://ark.cn-beijing.volces.com/api/v3"},
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

func TestProviderFactoryCreatesVoyageAIProvider(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")

	factory := NewProviderFactory(time.Second)
	provider, err := factory.CreateProvider(ChannelTypeVoyageAI, "https://api.voyageai.com/v1", "key")
	if err != nil {
		t.Fatalf("CreateProvider(VoyageAI) error = %v", err)
	}
	if _, ok := provider.(*VoyageAIProvider); !ok {
		t.Fatalf("provider = %T, want *VoyageAIProvider", provider)
	}
}

func TestProviderFactoryCreatesOpenAICompatibleProvidersForExpandedDefaults(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")

	factory := NewProviderFactory(time.Second)
	for _, channelType := range []int32{ChannelTypeOpenRouter, ChannelTypeSiliconFlow, ChannelTypeOllama, ChannelTypeDoubao} {
		provider, err := factory.CreateProvider(channelType, "", "key")
		if err != nil {
			t.Fatalf("CreateProvider(%d) error = %v", channelType, err)
		}
		if _, ok := provider.(*OpenAIProvider); !ok {
			t.Fatalf("provider for channel type %d = %T, want *OpenAIProvider", channelType, provider)
		}
	}
}

func TestProviderFactoryRejectsKnownNativeProvidersWithoutAdapters(t *testing.T) {
	factory := NewProviderFactory(time.Second)

	for _, channelType := range []int32{
		ChannelTypeHunyuan,
		ChannelTypeXingchen,
		ChannelTypeBedrock,
		ChannelTypeCloudflare,
		ChannelTypeVertexAI,
		ChannelTypeReplicate,
		ChannelTypeBaidu,
		ChannelTypeXunfei,
	} {
		if _, err := factory.CreateProvider(channelType, "", "key"); err == nil {
			t.Fatalf("CreateProvider(%d) error = nil, want unsupported provider error", channelType)
		}
	}
}

func TestProviderFactoryDefaultTimeoutMatchesLongResponsesCalls(t *testing.T) {
	factory := NewProviderFactory(0)
	if factory.defaultTimeout != time.Minute {
		t.Fatalf("default timeout = %v, want 1m", factory.defaultTimeout)
	}
}
