package biz

import relayprovider "micro-one-api/domain/upstream/provider"

// CanonicalUsage is the request-scoped, provider-agnostic token bucket view.
// It is produced at the relay edge (after upstream response parsing or raw
// JSON extraction) and consumed by billing, usage logging and observability.
//
// PromptExclusive travels with the usage so downstream code does not have to
// re-derive Anthropic/OpenAI bucket semantics from the channel type.
type CanonicalUsage struct {
	PromptTokens          int64
	CompletionTokens      int64
	CacheReadTokens       int64
	CacheCreation5mTokens int64
	CacheCreation1hTokens int64
	TotalTokens           int64
	PromptExclusive       bool
}

// IsEmpty reports whether no token bucket is populated.
func (u CanonicalUsage) IsEmpty() bool {
	return u.PromptTokens == 0 && u.CompletionTokens == 0 && u.CacheReadTokens == 0 &&
		u.CacheCreation5mTokens == 0 && u.CacheCreation1hTokens == 0 && u.TotalTokens == 0
}

// DerivedTotal returns the sum of the five canonical buckets. It is the
// billing-relevant total (ADR §2); when it differs from TotalTokens the bucket
// sum wins.
func (u CanonicalUsage) DerivedTotal() int64 {
	return u.PromptTokens + u.CompletionTokens + u.CacheReadTokens +
		u.CacheCreation5mTokens + u.CacheCreation1hTokens
}

// WithTotalFromBuckets fills TotalTokens from the bucket sum when the upstream
// omitted total_tokens or reported an inconsistent one. The bucket sum is the
// authoritative billing total.
func (u CanonicalUsage) WithTotalFromBuckets() CanonicalUsage {
	if derived := u.DerivedTotal(); derived > 0 {
		u.TotalTokens = derived
	}
	return u
}

// IsPromptExclusiveChannel reports whether the selected upstream uses
// mutually-exclusive prompt / cache_read / cache_creation token buckets
// (ADR §3.3). When true, the billing layer must NOT subtract cache_read from
// prompt_tokens because the upstream already returns them as separate,
// non-overlapping buckets. OpenAI-compatible upstreams use subset semantics
// where cached_tokens is part of prompt_tokens, so this returns false.
func IsPromptExclusiveChannel(plan *RelayPlan) bool {
	if plan == nil {
		return false
	}
	if plan.Account != nil {
		switch plan.Account.Platform {
		case "claude", "zhipu", "minimax", "kimi":
			return true
		}
		return false
	}
	if plan.Channel != nil {
		switch plan.Channel.Type {
		case relayprovider.ChannelTypeAnthropic,
			relayprovider.ChannelTypeClaude,
			relayprovider.ChannelTypeBedrock,
			relayprovider.ChannelTypeVertexAI,
			relayprovider.ChannelTypeClaudeOAuth,
			relayprovider.ChannelTypeZhipuPlan,
			relayprovider.ChannelTypeMinimaxPlan,
			relayprovider.ChannelTypeKimiOAuth:
			return true
		}
	}
	return false
}

// IsPromptExclusiveChannelType is the channel-type-only variant of
// IsPromptExclusiveChannel, used by code paths that have a channel type int32
// but not a full RelayPlan (e.g. the legacy one-api handler).
func IsPromptExclusiveChannelType(chType int32) bool {
	switch chType {
	case relayprovider.ChannelTypeAnthropic,
		relayprovider.ChannelTypeClaude,
		relayprovider.ChannelTypeBedrock,
		relayprovider.ChannelTypeVertexAI,
		relayprovider.ChannelTypeClaudeOAuth,
		relayprovider.ChannelTypeZhipuPlan,
		relayprovider.ChannelTypeMinimaxPlan,
		relayprovider.ChannelTypeKimiOAuth:
		return true
	}
	return false
}
