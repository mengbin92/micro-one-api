package biz

import relayprovider "micro-one-api/domain/upstream/provider"

// Usage contract versions for the CommitQuota wire contract
// (token-usage-billing-semantics-remediation §5.3). Version 0 (proto default)
// means a legacy producer that only sends the flat token fields; version 1
// means the producer sends a UsageEnvelope whose canonical buckets are
// authoritative for billing.
const (
	UsageContractVersionLegacy int32 = 0
	UsageContractVersionV1     int32 = 1
)

// UsageSemantics identifies how the upstream-reported prompt/input and cache
// buckets relate. It is decided by the parser from the response's field
// shape, NEVER from the channel type, subscription platform, model name, or
// the numeric relationship between total/prompt/output (§2.5).
type UsageSemantics string

const (
	// UsageSemanticsOpenAISubset: the reported prompt/input tokens INCLUDE the
	// cached tokens (OpenAI Chat / Responses shape). uncached = prompt - cached.
	UsageSemanticsOpenAISubset UsageSemantics = "openai_subset"
	// UsageSemanticsAnthropicExclusive: input/cache_read/cache_creation/output
	// are mutually exclusive buckets (Anthropic Messages shape). uncached =
	// input_tokens; no subtraction may happen.
	UsageSemanticsAnthropicExclusive UsageSemantics = "anthropic_exclusive"
)

// UsageParseStatus is the confidence verdict of the usage parser. It is a
// trust state, not a bucket semantic (§4.1).
type UsageParseStatus string

const (
	// UsageParseVerified: exactly one Canonical exists; when any cache bucket
	// is non-zero the Semantics is proven from the field shape.
	UsageParseVerified UsageParseStatus = "verified"
	// UsageParseEstimated: no upstream usage was available; a local estimator
	// produced the canonical buckets. Estimators MUST NOT fabricate cache.
	UsageParseEstimated UsageParseStatus = "estimated"
	// UsageParseAmbiguous: the payload contradicts itself (e.g. cache_read
	// exceeds the reported prompt, or conflicting protocol fields). No single
	// Canonical exists; SubsetCandidate/ExclusiveCandidate carry both
	// interpretations for conservative settlement (§5.2).
	UsageParseAmbiguous UsageParseStatus = "ambiguous"
	// UsageParseLegacy: the producer did not send the v1 contract. Reserved
	// for rolling-upgrade compatibility; new producers MUST NOT emit it for
	// parse failures (those are ambiguous).
	UsageParseLegacy UsageParseStatus = "legacy"
)

// Invariant / decision reasons recorded on the envelope and persisted to the
// ledger's usage_decision_reason column (§9).
const (
	UsageReasonCachedExceedsReportedPrompt  = "cached_exceeds_reported_prompt"
	UsageReasonReportedTotalMismatch        = "reported_total_mismatch"
	UsageReasonProtocolFieldConflict        = "protocol_field_conflict"
	UsageReasonFinalAttemptSemanticsMissing = "final_attempt_semantics_missing"
	UsageReasonNegativeBucket               = "negative_bucket"
	UsageReasonOverflow                     = "overflow"
	UsageReasonStreamUsageMissing           = "stream_usage_missing"
	UsageReasonLegacyProducer               = "legacy_producer"
	UsageReasonV1ContractError              = "v1_contract_error"
)

// ReportedUsage preserves the raw upstream-reported values for audit and
// display. Reported totals NEVER participate in cost computation (§4.1).
type ReportedUsage struct {
	PromptTokens          int64
	OutputTokens          int64
	CacheReadTokens       int64
	CacheCreation5mTokens int64
	CacheCreation1hTokens int64
	TotalTokens           int64
	// SourceProtocol identifies the protocol the parser proved from the field
	// shape (e.g. "openai_chat", "responses", "anthropic_messages"); empty
	// when unknown.
	SourceProtocol string
	// FieldShape records which raw field names carried the buckets, for audit.
	FieldShape string
}

// CanonicalUsage expresses exactly the five mutually-exclusive billing
// buckets (§4.1). UncachedInputTokens is already net of cache-read: any
// subtraction happens in the parser, never in the billing layer.
type CanonicalUsage struct {
	UncachedInputTokens   int64
	CacheReadTokens       int64
	CacheCreation5mTokens int64
	CacheCreation1hTokens int64
	OutputTokens          int64
}

// IsEmpty reports whether no canonical bucket is populated.
func (u CanonicalUsage) IsEmpty() bool {
	return u.UncachedInputTokens == 0 && u.CacheReadTokens == 0 &&
		u.CacheCreation5mTokens == 0 && u.CacheCreation1hTokens == 0 && u.OutputTokens == 0
}

// BillableTotal is the sum of the five mutually-exclusive buckets: the only
// total the billing layer and financial displays may use (§4.3).
func (u CanonicalUsage) BillableTotal() int64 {
	return u.UncachedInputTokens + u.CacheReadTokens +
		u.CacheCreation5mTokens + u.CacheCreation1hTokens + u.OutputTokens
}

// UsageEnvelope separates the reported usage from the canonical billing
// buckets and carries the parser verdict. The invariants are:
//   - verified: exactly one Canonical; cache present => Semantics set;
//   - estimated: Canonical present, cache buckets are never fabricated;
//   - ambiguous: Canonical MUST be nil, both candidates present when cache
//     tokens exist;
//   - legacy: only for old producers on the wire, never for new parse failures.
type UsageEnvelope struct {
	ContractVersion    int32
	Reported           ReportedUsage
	Canonical          *CanonicalUsage
	Semantics          UsageSemantics
	ParseStatus        UsageParseStatus
	SubsetCandidate    *CanonicalUsage
	ExclusiveCandidate *CanonicalUsage
	DecisionReason     string
}

// CanonicalOrZero returns the canonical buckets or the zero value, so
// consumers that only run on verified/estimated paths stay nil-safe.
func (e *UsageEnvelope) CanonicalOrZero() CanonicalUsage {
	if e == nil || e.Canonical == nil {
		return CanonicalUsage{}
	}
	return *e.Canonical
}

// BillableTotal returns the canonical billable total (0 for ambiguous).
func (e *UsageEnvelope) BillableTotal() int64 {
	return e.CanonicalOrZero().BillableTotal()
}

// ---------------------------------------------------------------------------
// Legacy prompt-exclusive helpers (deprecated compat window, §5.3).
//
// These remain ONLY for writing the legacy prompt_exclusive wire field while
// old billing consumers still exist, and for parser preflight hints. New code
// MUST NOT use them to decide billing semantics: the parser's verdict on the
// final attempt's envelope is authoritative.
// ---------------------------------------------------------------------------

// IsPromptExclusiveChannel reports whether the selected upstream uses
// mutually-exclusive prompt / cache_read / cache_creation token buckets
// (ADR §3.3). When true, the billing layer must NOT subtract cache_read from
// prompt_tokens because the upstream already returns them as separate,
// non-overlapping buckets. OpenAI-compatible upstreams use subset semantics
// where cached_tokens is part of prompt_tokens, so this returns false.
//
// Deprecated for billing decisions: kept for legacy wire dual-write only.
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
//
// Deprecated for billing decisions: kept for legacy wire dual-write only.
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
