package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	channelv1 "micro-one-api/api/channel/v1"
	commonv1 "micro-one-api/api/common/v1"
	identityv1 "micro-one-api/api/identity/v1"
	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"

	"micro-one-api/platform/audit"
)

// extractAPIKey accepts the two credential headers used by OpenAI- and
// Anthropic-compatible clients. Anthropic clients prefer x-api-key when both
// headers are present, matching the Messages API behavior.
func extractAPIKey(r *http.Request) string {
	if key := strings.TrimSpace(r.Header.Get("x-api-key")); key != "" {
		return key
	}
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	}
	return ""
}

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
	reply, err := s.identityClient.GetAuthSnapshot(ctx, req)
	if err != nil {
		return nil, err
	}
	// Stamp the audit actor so the audit middleware records the real caller
	// instead of an empty actor. WithActor writes into the mutable
	// *actorHolder injected by the audit middleware (when present), or falls
	// back to an immutable context value — either way the returned context is
	// used so the fallback value is never dropped. The session id is a short
	// display prefix of the token, NOT the credential itself: a full session
	// token in audit logs would leak a usable credential to anyone who can
	// read the logs (audit records are long-lived).
	ctx = audit.WithActor(ctx, audit.ActorInfo{UserID: reply.GetUserId(), SessionID: auditSessionIDPrefix(token)})
	if s.tokenQuotaBlocker != nil && s.tokenQuotaBlocker.IsTokenQuotaBlocked(reply.GetTokenId()) {
		return nil, fmt.Errorf("token quota temporarily unavailable")
	}
	return reply, nil
}

// auditSessionIDPrefix returns a short display prefix of a session token for
// audit session correlation. The full token is a credential and must never be
// written to audit logs; the prefix is enough to correlate repeated calls from
// the same session while remaining unusable for authentication.
func auditSessionIDPrefix(token string) string {
	const maxLen = 8
	if len(token) <= maxLen {
		return token
	}
	return token[:maxLen]
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

func (s *HTTPServer) estimateTokens(req *relayprovider.ChatCompletionsRequest) int64 {
	// 简单的 token 估算逻辑
	// 实际应用中可以使用更精确的 tokenizer
	tokens := int64(0)

	// 估算输入 tokens
	for _, msg := range req.Messages {
		tokens += int64(len(msg.Content) / 4) // 假设平均每个 token 4 个字符
	}

	// 估算输出 tokens (优先使用 OpenAI 新字段 max_completion_tokens)
	maxTokens := req.MaxTokens
	if req.MaxCompletionTokens != nil {
		maxTokens = req.MaxCompletionTokens
	}
	if maxTokens != nil && *maxTokens > 0 {
		tokens += int64(*maxTokens)
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

// cacheCreationTokensFromProviderUsage extracts the 5m / 1h cache-creation
// buckets from a provider Usage object (docs/design/token-usage-semantics.md
// §3.3/§4.2). It checks both PromptTokensDetails and InputTokensDetails for
// the TTL-split fields. When only a cache_creation aggregate would be needed
// the caller can read the individual buckets directly; this helper keeps the
// same lookup pattern as cacheReadTokensFromProviderUsage so both details
// objects are consulted.
func cacheCreationTokensFromProviderUsage(usage relayprovider.Usage) (fiveM, oneH int64) {
	for _, details := range []relayprovider.UsageTokenDetails{
		usage.PromptTokensDetails,
		usage.InputTokensDetails,
	} {
		if details.CacheCreation5mTokens > 0 {
			fiveM = int64(details.CacheCreation5mTokens)
		}
		if details.CacheCreation1hTokens > 0 {
			oneH = int64(details.CacheCreation1hTokens)
		}
		if fiveM > 0 || oneH > 0 {
			return fiveM, oneH
		}
	}
	return 0, 0
}
