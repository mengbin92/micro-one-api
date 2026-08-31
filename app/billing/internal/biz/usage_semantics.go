package biz

import "math"

// Billing-side view of the v1 usage-semantics contract
// (token-usage-billing-semantics-remediation §4.1/§5). These mirror the
// relay's internal/biz types without importing the relay module.

// Parse statuses carried by UsageEnvelopeData.ParseStatus.
const (
	UsageParseStatusVerified  = "verified"
	UsageParseStatusEstimated = "estimated"
	UsageParseStatusAmbiguous = "ambiguous"
	UsageParseStatusLegacy    = "legacy"
)

// Usage semantics values carried by UsageEnvelopeData.Semantics.
const (
	UsageSemanticsOpenAISubset       = "openai_subset"
	UsageSemanticsAnthropicExclusive = "anthropic_exclusive"
)

// Decision reasons persisted on the ledger's usage_decision_reason column.
const (
	UsageReasonLegacyProducer  = "legacy_producer"
	UsageReasonV1ContractError = "v1_contract_error"
	UsageReasonNegativeBucket  = "negative_bucket"
	UsageReasonOverflow        = "overflow"
)

// Usage contract versions (§5.3).
const (
	UsageContractVersionLegacy int32 = 0
	UsageContractVersionV1     int32 = 1
)

// CanonicalBuckets is exactly the five mutually-exclusive billing buckets.
// UncachedInputTokens is already net of cache-read: the billing layer NEVER
// subtracts cache from prompt (§5.1).
type CanonicalBuckets struct {
	UncachedInputTokens   int64
	CacheReadTokens       int64
	CacheCreation5mTokens int64
	CacheCreation1hTokens int64
	OutputTokens          int64
}

// BillableTotal is the sum of the five buckets.
func (b CanonicalBuckets) BillableTotal() int64 {
	return b.UncachedInputTokens + b.CacheReadTokens +
		b.CacheCreation5mTokens + b.CacheCreation1hTokens + b.OutputTokens
}

// UsageEnvelopeData is the wire envelope delivered with CommitQuotaRequest.
type UsageEnvelopeData struct {
	ParseStatus    string
	Semantics      string
	Protocol       string
	FieldShape     string
	DecisionReason string
	// Reported values as the upstream sent them; never used for cost math.
	ReportedPromptTokens          int64
	ReportedOutputTokens          int64
	ReportedCacheReadTokens       int64
	ReportedCacheCreation5mTokens int64
	ReportedCacheCreation1hTokens int64
	ReportedTotalTokens           int64
	Canonical                     *CanonicalBuckets
	SubsetCandidate               *CanonicalBuckets
	ExclusiveCandidate            *CanonicalBuckets
}

// validateV1Envelope enforces the financial wire invariants at the billing
// boundary. Relay parsing is not a substitute for consumer-side validation:
// rolling upgrades, serialization bugs, and malformed callers must never
// turn an invalid v1 envelope into a trusted canonical charge.
func validateV1Envelope(env *UsageEnvelopeData) string {
	if env == nil {
		return UsageReasonV1ContractError
	}
	if hasNegative(
		env.ReportedPromptTokens, env.ReportedOutputTokens,
		env.ReportedCacheReadTokens, env.ReportedCacheCreation5mTokens,
		env.ReportedCacheCreation1hTokens, env.ReportedTotalTokens,
	) {
		return UsageReasonNegativeBucket
	}
	switch env.ParseStatus {
	case UsageParseStatusVerified:
		if env.Canonical == nil || env.SubsetCandidate != nil || env.ExclusiveCandidate != nil {
			return UsageReasonV1ContractError
		}
		if reason := canonicalInvalidReason(*env.Canonical); reason != "" {
			return reason
		}
		if hasCache(*env.Canonical) && env.Semantics != UsageSemanticsOpenAISubset && env.Semantics != UsageSemanticsAnthropicExclusive {
			return UsageReasonV1ContractError
		}
		if env.Semantics != "" && env.Semantics != UsageSemanticsOpenAISubset && env.Semantics != UsageSemanticsAnthropicExclusive {
			return UsageReasonV1ContractError
		}
	case UsageParseStatusEstimated:
		if env.Canonical == nil || env.SubsetCandidate != nil || env.ExclusiveCandidate != nil || hasCache(*env.Canonical) || env.Semantics != "" {
			return UsageReasonV1ContractError
		}
		if reason := canonicalInvalidReason(*env.Canonical); reason != "" {
			return reason
		}
	case UsageParseStatusAmbiguous:
		if env.Canonical != nil || env.SubsetCandidate == nil || env.ExclusiveCandidate == nil || env.DecisionReason == "" || env.Semantics != "" {
			return UsageReasonV1ContractError
		}
		if reason := canonicalInvalidReason(*env.SubsetCandidate); reason != "" {
			return reason
		}
		if reason := canonicalInvalidReason(*env.ExclusiveCandidate); reason != "" {
			return reason
		}
	default:
		return UsageReasonV1ContractError
	}
	return ""
}

func validCanonical(b CanonicalBuckets) bool {
	return canonicalInvalidReason(b) == ""
}

func canonicalInvalidReason(b CanonicalBuckets) string {
	if hasNegative(b.UncachedInputTokens, b.CacheReadTokens, b.CacheCreation5mTokens, b.CacheCreation1hTokens, b.OutputTokens) {
		return UsageReasonNegativeBucket
	}
	if _, overflow := safeBucketTotal(b); overflow {
		return UsageReasonOverflow
	}
	return ""
}

func safeBucketTotal(b CanonicalBuckets) (int64, bool) {
	var total int64
	for _, value := range []int64{b.UncachedInputTokens, b.CacheReadTokens, b.CacheCreation5mTokens, b.CacheCreation1hTokens, b.OutputTokens} {
		if value < 0 || value > math.MaxInt64-total {
			return 0, true
		}
		total += value
	}
	return total, false
}

func hasNegative(values ...int64) bool {
	for _, value := range values {
		if value < 0 {
			return true
		}
	}
	return false
}

func hasCache(b CanonicalBuckets) bool {
	return b.CacheReadTokens > 0 || b.CacheCreation5mTokens > 0 || b.CacheCreation1hTokens > 0
}

// reportedCandidateBuckets reconstructs both conservative interpretations
// from the v1 reported values (falling back to the legacy dual-write when
// the envelope or its reported buckets are missing). It deliberately ignores
// PromptExclusive: a contract error must compare both meanings.
func reportedCandidateBuckets(usage LedgerUsage) (CanonicalBuckets, CanonicalBuckets) {
	prompt := usage.PromptTokens
	output := usage.CompletionTokens
	cacheRead := usage.CacheReadTokens
	creation5m := usage.CacheCreation5mTokens
	creation1h := usage.CacheCreation1hTokens
	if env := usage.Envelope; env != nil && (env.ReportedPromptTokens != 0 || env.ReportedOutputTokens != 0 ||
		env.ReportedCacheReadTokens != 0 || env.ReportedCacheCreation5mTokens != 0 || env.ReportedCacheCreation1hTokens != 0) {
		prompt = env.ReportedPromptTokens
		output = env.ReportedOutputTokens
		cacheRead = env.ReportedCacheReadTokens
		creation5m = env.ReportedCacheCreation5mTokens
		creation1h = env.ReportedCacheCreation1hTokens
	}
	prompt = maxInt64(prompt, 0)
	output = maxInt64(output, 0)
	cacheRead = maxInt64(cacheRead, 0)
	creation5m = maxInt64(creation5m, 0)
	creation1h = maxInt64(creation1h, 0)
	uncached := maxInt64(prompt-cacheRead, 0)
	subset := CanonicalBuckets{
		UncachedInputTokens: uncached, CacheReadTokens: cacheRead,
		CacheCreation5mTokens: creation5m, CacheCreation1hTokens: creation1h,
		OutputTokens: output,
	}
	exclusive := subset
	exclusive.UncachedInputTokens = prompt
	return subset, exclusive
}

// ledgerUsageAudit is the per-request usage-semantics audit trail persisted
// onto the ledger row (migration 085). It is produced by the cost resolver
// and consumed identically by the sync and async commit pipelines.
type ledgerUsageAudit struct {
	UncachedInputTokens    int64
	ReportedPromptTokens   int64
	ReportedTotalTokens    int64
	BillableTotalTokens    int64
	UsageSemantics         string
	UsageProtocol          string
	UsageFieldShape        string
	UsageParseStatus       string
	UsageContractVersion   int32
	CanonicalPresent       bool
	UsageDecisionReason    string
	SubsetCandidateCost    int64
	ExclusiveCandidateCost int64
}

// legacyCanonicalBuckets derives the five buckets from the legacy flat fields
// with the OLD semantics (§5.3 dual-write contract): prompt_exclusive=true
// means the flat prompt is already uncached; otherwise cache-read is a subset
// of prompt and the subtraction happens HERE (the only place it still
// exists), never inside calculateCanonicalCost.
func legacyCanonicalBuckets(usage LedgerUsage, actualTokens int64) CanonicalBuckets {
	prompt := usage.PromptTokens
	completion := usage.CompletionTokens
	cacheRead := usage.CacheReadTokens
	if prompt <= 0 && completion <= 0 && cacheRead <= 0 {
		prompt = actualTokens
	}
	uncached := prompt
	if !usage.PromptExclusive {
		if cacheRead < prompt {
			uncached = prompt - cacheRead
		} else {
			uncached = 0
		}
	}
	if uncached < 0 {
		uncached = 0
	}
	return CanonicalBuckets{
		UncachedInputTokens:   uncached,
		CacheReadTokens:       cacheRead,
		CacheCreation5mTokens: usage.CacheCreation5mTokens,
		CacheCreation1hTokens: usage.CacheCreation1hTokens,
		OutputTokens:          completion,
	}
}

// applyTo copies the audit trail onto a consume ledger row.
func (a ledgerUsageAudit) applyTo(l *Ledger) {
	l.UncachedInputTokens = a.UncachedInputTokens
	l.ReportedPromptTokens = a.ReportedPromptTokens
	l.ReportedTotalTokens = a.ReportedTotalTokens
	l.BillableTotalTokens = a.BillableTotalTokens
	l.UsageSemantics = a.UsageSemantics
	l.UsageProtocol = a.UsageProtocol
	l.UsageFieldShape = a.UsageFieldShape
	l.UsageParseStatus = a.UsageParseStatus
	l.UsageContractVersion = a.UsageContractVersion
	l.CanonicalPresent = a.CanonicalPresent
	l.UsageDecisionReason = a.UsageDecisionReason
	l.SubsetCandidateCost = a.SubsetCandidateCost
	l.ExclusiveCandidateCost = a.ExclusiveCandidateCost
}
