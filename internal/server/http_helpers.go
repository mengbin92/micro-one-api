package server

import (
	"context"
	"os"
	"strconv"
	"strings"

	channelv1 "micro-one-api/api/channel/v1"
	commonv1 "micro-one-api/api/common/v1"
	identityv1 "micro-one-api/api/identity/v1"
	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
)

func isSubscriptionChannel(t int32) bool {
	switch t {
	case relayprovider.ChannelTypeCodexOAuth, relayprovider.ChannelTypeClaudeOAuth,
		relayprovider.ChannelTypeZhipuPlan, relayprovider.ChannelTypeMinimaxPlan, relayprovider.ChannelTypeKimiOAuth:
		return true
	default:
		return false
	}
}

func isAnthropicAPIKeyChannel(ch *relaybiz.Channel) bool {
	return ch != nil && ch.Type == relayprovider.ChannelTypeAnthropic
}

func providerConfigFromChannelInfo(channel *commonv1.ChannelInfo) relayprovider.ProviderConfig {
	if channel == nil || channel.Config == nil {
		return relayprovider.ProviderConfig{}
	}
	return relayprovider.ProviderConfig{APIVersion: channel.Config.ApiVersion}
}

func (s *HTTPServer) getAuthSnapshot(ctx context.Context, token string) (*identityv1.GetAuthSnapshotReply, error) {
	req := &identityv1.GetAuthSnapshotRequest{
		Token: token,
	}
	return s.identityClient.GetAuthSnapshot(ctx, req)
}

func (s *HTTPServer) listAvailableModels(ctx context.Context, group string) (*channelv1.ListAvailableModelsReply, error) {
	req := &channelv1.ListAvailableModelsRequest{
		Group: group,
	}
	return s.channelClient.ListAvailableModels(ctx, req)
}

func (s *HTTPServer) applyModelWhitelist(availableModels []string, allowedModels []string) []string {
	if len(allowedModels) == 0 {
		return availableModels
	}

	allowedSet := make(map[string]bool)
	for _, model := range allowedModels {
		allowedSet[model] = true
	}

	filtered := make([]string, 0, len(availableModels))
	for _, model := range availableModels {
		if allowedSet[model] {
			filtered = append(filtered, model)
		}
	}

	return filtered
}

func amountUnitsToUSD(amount int64) float64 {
	return float64(amount) / float64(amountUnitsPerUSD)
}

func quotaPerUSDFromEnv() int64 {
	raw := strings.TrimSpace(os.Getenv("PAYMENT_QUOTA_PER_UNIT"))
	if raw == "" {
		return defaultQuotaPerUSD
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return defaultQuotaPerUSD
	}
	return value
}

func (s *HTTPServer) estimateTokens(req *relayprovider.ChatCompletionsRequest) int64 {
	// 简单的 token 估算逻辑
	// 实际应用中可以使用更精确的 tokenizer
	tokens := int64(0)

	// 估算输入 tokens
	for _, msg := range req.Messages {
		tokens += int64(len(msg.Content) / 4) // 假设平均每个 token 4 个字符
	}

	// 估算输出 tokens (基于 max_tokens 或默认值)
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		tokens += int64(*req.MaxTokens)
	} else {
		tokens += 1000 // 默认输出 tokens
	}

	return tokens
}

func (s *HTTPServer) calculateActualTokens(resp *relayprovider.ChatCompletionsResponse) int64 {
	// resp.Usage 不是指针，是值类型
	return int64(resp.Usage.TotalTokens)
}

func cacheReadTokensFromProviderUsage(usage relayprovider.Usage) int64 {
	for _, value := range []int{
		usage.PromptTokensDetails.CacheReadTokens,
		usage.PromptTokensDetails.CachedTokens,
		usage.InputTokensDetails.CacheReadTokens,
		usage.InputTokensDetails.CachedTokens,
	} {
		if value > 0 {
			return int64(value)
		}
	}
	return 0
}
