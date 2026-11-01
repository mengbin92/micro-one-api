package billing

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
	ParserRevision   string   `json:",omitempty"`
	ContractRevision string   `json:",omitempty"`
	PresentPaths     []string `json:",omitempty"`
	ParseStatus      string
	Semantics        string
	Protocol         string
	FieldShape       string
	DecisionReason   string
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

type LedgerUsage struct {
	ActualServiceTier string `json:",omitempty"`
	ServiceTierStatus string `json:",omitempty"`
	// FrozenServiceTier is resolved by the Billing owner from its reservation.
	// It is never part of the producer payload or settlement digest.
	FrozenServiceTier     *ServiceTierConfig `json:"-"`
	FrozenPricingTimeBand string             `json:"-"`
	TokenName             string
	Endpoint              string
	PromptTokens          int64
	CompletionTokens      int64
	CacheReadTokens       int64
	CacheCreation5mTokens int64
	CacheCreation1hTokens int64
	UpstreamCost          int64
	ElapsedTime           int64
	IsStream              bool
	SubscriptionAccountID int64 // optional override; 0 = use the reservation's account

	// PromptExclusive (v0.11.0 Phase 0/1, ADR §3.3): when true, prompt_tokens
	// and cache_read_tokens are mutually exclusive buckets (Anthropic / GLM
	// Messages API). The pricing function must NOT subtract cacheRead from
	// prompt because prompt already excludes cached tokens. When false
	// (default, OpenAI subset semantics), cacheRead IS a subset of prompt and
	// the classic input = prompt - cacheRead subtraction applies.
	//
	// Deprecated (token-usage-billing-semantics-remediation §5.3): only the
	// legacy (UsageContractVersion=0) branch may consult it. v1 requests are
	// priced from Envelope canonical buckets exclusively.
	PromptExclusive bool

	// UsageContractVersion selects the usage contract the producer honors:
	// 0 = legacy flat fields above; 1 = Envelope is authoritative (§5.3).
	UsageContractVersion int32
	// Envelope carries the parser verdict and canonical buckets for v1
	// producers. Nil for legacy producers. A v1 request with a nil/invalid
	// envelope is a producer contract error and is settled via the ambiguous
	// conservative policy (NEVER via a silent legacy fallback).
	Envelope *UsageEnvelopeData

	// v0.11.0 Phase 2 §2.2: stable upstream cost-key inputs. UpstreamModelID
	// is the exact identifier the selected upstream requires (e.g.
	// "z-ai/glm-5.2"); SourceKind is CostSourceChannel or CostSourceSubscription
	// so the cost-key prefix is unambiguous. When either is empty, billing
	// falls back to the legacy <channel_id>:<public_model_id> key.
	UpstreamModelID string
	SourceKind      string
}
