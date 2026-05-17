package provider

import (
	"fmt"
	"time"
)

// ProviderFactory creates provider instances based on channel type
type ProviderFactory struct {
	defaultTimeout time.Duration
}

type ProviderConfig struct {
	APIVersion string
}

// NewProviderFactory creates a new provider factory
func NewProviderFactory(defaultTimeout time.Duration) *ProviderFactory {
	if defaultTimeout == 0 {
		defaultTimeout = 30 * time.Second
	}
	return &ProviderFactory{
		defaultTimeout: defaultTimeout,
	}
}

// CreateProvider creates a provider based on channel type
func (f *ProviderFactory) CreateProvider(channelType int32, baseURL, apiKey string) (Provider, error) {
	return f.CreateProviderWithConfig(channelType, baseURL, apiKey, ProviderConfig{})
}

func (f *ProviderFactory) CreateProviderWithConfig(channelType int32, baseURL, apiKey string, config ProviderConfig) (Provider, error) {
	switch channelType {
	case ChannelTypeAnthropic: // Anthropic Claude
		return NewAnthropicProvider(baseURL, apiKey, f.defaultTimeout), nil
	case ChannelTypeGemini: // Google Gemini
		return NewGeminiProvider(baseURL, apiKey, f.defaultTimeout), nil
	case ChannelTypeAzure:
		if baseURL == "" {
			return nil, fmt.Errorf("azure channel requires base_url")
		}
		return NewAzureProvider(baseURL, apiKey, config.APIVersion, f.defaultTimeout)
	case ChannelTypeVoyageAI:
		return NewVoyageAIProvider(resolveOpenAICompatibleBaseURL(channelType, baseURL), apiKey, f.defaultTimeout)
	case ChannelTypeHunyuan,
		ChannelTypeXingchen,
		ChannelTypeBedrock,
		ChannelTypeCloudflare,
		ChannelTypeVertexAI,
		ChannelTypeReplicate,
		ChannelTypeBaidu,
		ChannelTypeXunfei:
		return nil, fmt.Errorf("channel type %d requires a native provider adapter", channelType)
	case ChannelTypeOpenAI,
		ChannelTypeDeepSeek,
		ChannelTypeMistral,
		ChannelTypeMoonshot,
		ChannelTypeGroq,
		ChannelTypeCohere,
		ChannelTypeBaichuan,
		ChannelTypeZhipu,
		ChannelTypeTongyi,
		ChannelTypeMinimax,
		ChannelTypeTogether,
		ChannelTypeFireworks,
		ChannelTypePerplexity,
		ChannelTypeNovita,
		ChannelTypeOpenRouter,
		ChannelTypeSiliconFlow,
		ChannelTypeOllama,
		ChannelTypeDoubao:
		return NewOpenAIProvider(resolveOpenAICompatibleBaseURL(channelType, baseURL), apiKey, f.defaultTimeout)
	default:
		// Default to OpenAI-compatible for unknown types
		return NewOpenAIProvider(resolveOpenAICompatibleBaseURL(channelType, baseURL), apiKey, f.defaultTimeout)
	}
}

func resolveOpenAICompatibleBaseURL(channelType int32, baseURL string) string {
	if baseURL != "" {
		return baseURL
	}
	switch channelType {
	case ChannelTypeOpenAI:
		return "https://api.openai.com/v1"
	case ChannelTypeDeepSeek:
		return "https://api.deepseek.com/v1"
	case ChannelTypeMistral:
		return "https://api.mistral.ai/v1"
	case ChannelTypeMoonshot:
		return "https://api.moonshot.cn/v1"
	case ChannelTypeGroq:
		return "https://api.groq.com/openai/v1"
	case ChannelTypeCohere:
		return "https://api.cohere.com/compatibility/v1"
	case ChannelTypeBaichuan:
		return "https://api.baichuan-ai.com/v1"
	case ChannelTypeZhipu:
		return "https://open.bigmodel.cn/api/paas/v4"
	case ChannelTypeTongyi:
		return "https://dashscope.aliyuncs.com/compatible-mode/v1"
	case ChannelTypeMinimax:
		return "https://api.minimax.chat/v1"
	case ChannelTypeTogether:
		return "https://api.together.xyz/v1"
	case ChannelTypeFireworks:
		return "https://api.fireworks.ai/inference/v1"
	case ChannelTypePerplexity:
		return "https://api.perplexity.ai"
	case ChannelTypeNovita:
		return "https://api.novita.ai/v3/openai"
	case ChannelTypeVoyageAI:
		return "https://api.voyageai.com/v1"
	case ChannelTypeOpenRouter:
		return "https://openrouter.ai/api/v1"
	case ChannelTypeSiliconFlow:
		return "https://api.siliconflow.cn/v1"
	case ChannelTypeOllama:
		return "http://localhost:11434/v1"
	case ChannelTypeDoubao:
		return "https://ark.cn-beijing.volces.com/api/v3"
	default:
		return "https://api.openai.com/v1"
	}
}

// Common channel types (these should align with one-api channel types)
const (
	ChannelTypeOpenAI      int32 = 1
	ChannelTypeAnthropic   int32 = 2
	ChannelTypeGemini      int32 = 3
	ChannelTypeClaude      int32 = 4
	ChannelTypeAzure       int32 = 5
	ChannelTypeDeepSeek    int32 = 6
	ChannelTypeMistral     int32 = 7
	ChannelTypeZhipu       int32 = 8
	ChannelTypeMoonshot    int32 = 9
	ChannelTypeGroq        int32 = 10
	ChannelTypeCohere      int32 = 11
	ChannelTypeBaichuan    int32 = 12
	ChannelTypeTongyi      int32 = 13
	ChannelTypeHunyuan     int32 = 14
	ChannelTypeMinimax     int32 = 15
	ChannelTypeXingchen    int32 = 16
	ChannelTypeBedrock     int32 = 17
	ChannelTypeTogether    int32 = 18
	ChannelTypeFireworks   int32 = 19
	ChannelTypePerplexity  int32 = 20
	ChannelTypeNovita      int32 = 21
	ChannelTypeVoyageAI    int32 = 22
	ChannelTypeOpenRouter  int32 = 23
	ChannelTypeSiliconFlow int32 = 24
	ChannelTypeOllama      int32 = 25
	ChannelTypeCloudflare  int32 = 26
	ChannelTypeVertexAI    int32 = 27
	ChannelTypeReplicate   int32 = 28
	ChannelTypeBaidu       int32 = 29
	ChannelTypeXunfei      int32 = 30
	ChannelTypeDoubao      int32 = 31
)
