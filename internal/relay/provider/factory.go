package provider

import (
	"time"
)

// ProviderFactory creates provider instances based on channel type
type ProviderFactory struct {
	defaultTimeout time.Duration
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
	switch channelType {
	case ChannelTypeAnthropic: // Anthropic Claude
		return NewAnthropicProvider(baseURL, apiKey, f.defaultTimeout), nil
	case ChannelTypeGemini: // Google Gemini
		return NewGeminiProvider(baseURL, apiKey, f.defaultTimeout), nil
	default:
		// Default to OpenAI-compatible for unknown types
		return NewOpenAIProvider(baseURL, apiKey, f.defaultTimeout)
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
	ChannelTypeMinimax      int32 = 15
	ChannelTypeXingchen    int32 = 16
	ChannelTypeBedrock     int32 = 17
	ChannelTypeTogether    int32 = 18
	ChannelTypeFireworks   int32 = 19
	ChannelTypePerplexity  int32 = 20
	ChannelTypeNovita      int32 = 21
	ChannelTypeVoyageAI    int32 = 22
)
