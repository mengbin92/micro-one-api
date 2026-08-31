// Package usage defines the single authoritative projection between the five
// mutually-exclusive canonical billing buckets and the inclusive
// OpenAI-compatible usage shape returned to clients
// (docs/design/token-usage-billing-semantics-remediation-2026-08-31.md §4.3).
//
// Both the provider layer (domain/upstream/provider) and the protocol
// compatibility layer (internal/apicompat) MUST render their client-facing
// usage through these helpers so the two paths cannot drift apart again: the
// original defect was provider Chat projecting total=input+output while
// apicompat Responses correctly added the cache buckets.
package usage

import "math"

// Buckets is the five mutually-exclusive canonical billing buckets (§4.1).
// UncachedInputTokens is already net of cache-read: any subtraction happens
// in the parser, never after projection.
type Buckets struct {
	UncachedInputTokens   int64
	CacheReadTokens       int64
	CacheCreation5mTokens int64
	CacheCreation1hTokens int64
	OutputTokens          int64
}

// BillableTotal is the sum of the five mutually-exclusive buckets: the only
// total financial code and displays may derive from the buckets.
func (b Buckets) BillableTotal() int64 {
	return addClamped(b.UncachedInputTokens, b.CacheReadTokens,
		b.CacheCreation5mTokens, b.CacheCreation1hTokens, b.OutputTokens)
}

// InclusiveInputTokens is the OpenAI-compatible prompt/input count: the
// uncached input plus every cache bucket (§4.3 projection matrix).
func (b Buckets) InclusiveInputTokens() int64 {
	return addClamped(b.UncachedInputTokens, b.CacheReadTokens,
		b.CacheCreation5mTokens, b.CacheCreation1hTokens)
}

// InclusiveTotalTokens is the OpenAI-compatible total: the inclusive input
// plus the output tokens.
func (b Buckets) InclusiveTotalTokens() int64 {
	return addClamped(b.InclusiveInputTokens(), b.OutputTokens)
}

// OpenAIProjection is the inclusive usage projection of Buckets (§4.3):
// prompt/input includes every cache bucket, cached_tokens carries cache-read
// only, and total = prompt + output.
type OpenAIProjection struct {
	PromptTokens          int64
	OutputTokens          int64
	TotalTokens           int64
	CachedTokens          int64
	CacheReadTokens       int64
	CacheCreation5mTokens int64
	CacheCreation1hTokens int64
}

// ProjectOpenAI renders the inclusive OpenAI-compatible projection of the
// canonical buckets. It is pure: reported totals, semantics and routing
// identity never participate.
func ProjectOpenAI(b Buckets) OpenAIProjection {
	return OpenAIProjection{
		PromptTokens:          b.InclusiveInputTokens(),
		OutputTokens:          b.OutputTokens,
		TotalTokens:           b.InclusiveTotalTokens(),
		CachedTokens:          b.CacheReadTokens,
		CacheReadTokens:       b.CacheReadTokens,
		CacheCreation5mTokens: b.CacheCreation5mTokens,
		CacheCreation1hTokens: b.CacheCreation1hTokens,
	}
}

// SplitInclusive is the inverse projection: given an OpenAI-compatible
// inclusive input count with its cache breakdown, recover the exclusive
// buckets an Anthropic-shaped usage object needs. A cache breakdown larger
// than the inclusive input clamps the uncached bucket to zero — callers that
// must treat that shape as an anomaly decide so in the parser; this helper
// never returns a negative bucket.
func SplitInclusive(inclusiveInput, cacheRead, cacheCreation5m, cacheCreation1h, output int64) Buckets {
	cacheTotal := addClamped(cacheRead, cacheCreation5m, cacheCreation1h)
	uncached := max(inclusiveInput-cacheTotal, 0)
	return Buckets{
		UncachedInputTokens:   uncached,
		CacheReadTokens:       cacheRead,
		CacheCreation5mTokens: cacheCreation5m,
		CacheCreation1hTokens: cacheCreation1h,
		OutputTokens:          output,
	}
}

// addClamped sums non-negative token counts, saturating at MaxInt64 instead
// of wrapping. Negative inputs (a parser-level anomaly) pass through
// unchanged so they remain visible to callers.
func addClamped(values ...int64) int64 {
	var total int64
	for _, value := range values {
		if value > math.MaxInt64-total {
			return math.MaxInt64
		}
		total += value
	}
	return total
}
