// Package usage provides provider-agnostic token-bucket extraction from raw
// upstream response JSON. It lives below package server so both the server
// handlers and the forwarder sub-package can use it without creating an
// import cycle.
//
// The parser proves the usage semantics from the response's field shape
// (token-usage-billing-semantics-remediation §4.2): it first identifies which
// protocol fields carried the buckets, then validates invariants, and only
// then normalizes into the five mutually-exclusive canonical buckets. It
// never accepts a route-derived promptExclusive bool: channel type,
// subscription platform and model name may only preflight parser selection,
// never override a verdict the field shape has proven (§2.5/§3.2).
package usage

import (
	"fmt"
	"math"

	"micro-one-api/pkg/jsonx"

	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/platform/metrics"
)

// ExtractEnvelopeFromJSON parses a raw upstream response body and returns the
// usage envelope: reported values, the parser verdict, and (when verified or
// estimated) the canonical five buckets. Ambiguous payloads carry both
// candidates instead of a fabricated canonical (§4.2).
func ExtractEnvelopeFromJSON(body []byte, fallback int64) relaybiz.UsageEnvelope {
	var payload any
	if err := jsonx.Unmarshal(body, &payload); err != nil {
		return EstimatedEnvelope(fallback)
	}
	return extractEnvelopeFromValue(payload, fallback)
}

// EstimatedEnvelope builds the estimator-only envelope: the canonical buckets
// contain just what the estimator can prove (uncached input), never
// fabricated cache buckets (§4.1).
func EstimatedEnvelope(fallback int64) relaybiz.UsageEnvelope {
	env := relaybiz.UsageEnvelope{
		ContractVersion: relaybiz.UsageContractVersionV1,
		ParseStatus:     relaybiz.UsageParseEstimated,
		Canonical:       &relaybiz.CanonicalUsage{},
	}
	if fallback > 0 {
		env.Canonical.UncachedInputTokens = fallback
	}
	return env
}

// usageExtraction is the raw parse result before semantic normalization: the
// reported buckets plus the field-shape signals the decision function needs.
type usageExtraction struct {
	reported relaybiz.ReportedUsage
	signals  FieldShapeSignals
}

// FieldShapeSignals records which protocol fields were present in the
// payload. Semantics are proven from these, never from routing identity
// (§4.2). The type is exported so the raw/SSE extraction path in package
// server can feed the SAME normalization helper (DecideEnvelope) instead of
// maintaining a parallel implementation (§7 http_raw_helpers.go row).
type FieldShapeSignals struct {
	HasAnthropicCacheRead     bool // cache_read_input_tokens
	HasAnthropicCacheCreation bool // cache_creation_input_tokens or cache_creation{...}
	HasOpenAICachedDetail     bool // *_details.cached_tokens or flat cached_tokens
	HasFlatCacheRead          bool // flat cache_read_tokens (provider-flattened)
	HasPromptTokens           bool // prompt_tokens (OpenAI Chat shape)
	HasInputTokens            bool // input_tokens (Responses / Anthropic shape)
	InvalidReason             string
}

// Merge ORs another observation's signals into s (streaming accumulation:
// message_start and message_delta each carry part of the shape).
func (s *FieldShapeSignals) Merge(other FieldShapeSignals) {
	s.HasAnthropicCacheRead = s.HasAnthropicCacheRead || other.HasAnthropicCacheRead
	s.HasAnthropicCacheCreation = s.HasAnthropicCacheCreation || other.HasAnthropicCacheCreation
	s.HasOpenAICachedDetail = s.HasOpenAICachedDetail || other.HasOpenAICachedDetail
	s.HasFlatCacheRead = s.HasFlatCacheRead || other.HasFlatCacheRead
	s.HasPromptTokens = s.HasPromptTokens || other.HasPromptTokens
	s.HasInputTokens = s.HasInputTokens || other.HasInputTokens
	if s.InvalidReason == "" {
		s.InvalidReason = other.InvalidReason
	}
}

// FieldShape returns a compact audit descriptor of the observed fields.
func (s FieldShapeSignals) FieldShape() string {
	shape := ""
	switch {
	case s.HasPromptTokens:
		shape = "prompt_tokens"
	case s.HasInputTokens:
		shape = "input_tokens"
	}
	switch {
	case s.HasAnthropicCacheRead:
		shape += "+cache_read_input_tokens"
	case s.HasOpenAICachedDetail:
		shape += "+details.cached_tokens"
	case s.HasFlatCacheRead:
		shape += "+cache_read_tokens"
	}
	if s.HasAnthropicCacheCreation {
		shape += "+cache_creation"
	}
	return shape
}

// SourceProtocol names the protocol the field shape proves.
func (s FieldShapeSignals) SourceProtocol() string {
	switch {
	case s.HasAnthropicCacheRead || s.HasAnthropicCacheCreation:
		return "anthropic_messages"
	case s.HasPromptTokens:
		return "openai_chat"
	case s.HasInputTokens:
		return "responses"
	default:
		return ""
	}
}

// DecideEnvelope implements the §4.2 decision table: prove semantics from the
// field shape, validate invariants BEFORE normalizing, and never let a
// max(prompt-cache, 0) clamp silently launder a conflict into "verified".
func DecideEnvelope(reported relaybiz.ReportedUsage, s FieldShapeSignals, fallback int64) relaybiz.UsageEnvelope {
	r := reported
	r.SourceProtocol = s.SourceProtocol()
	r.FieldShape = s.FieldShape()
	env := relaybiz.UsageEnvelope{
		ContractVersion: relaybiz.UsageContractVersionV1,
		Reported:        r,
	}
	if s.InvalidReason != "" {
		recordAnomaly(s.InvalidReason)
		return AmbiguousEnvelope(env, s.InvalidReason)
	}
	cacheTotal, overflow := sumNonNegative(r.CacheReadTokens, r.CacheCreation5mTokens, r.CacheCreation1hTokens)
	if overflow {
		recordAnomaly(relaybiz.UsageReasonOverflow)
		return AmbiguousEnvelope(env, relaybiz.UsageReasonOverflow)
	}
	if _, overflow := sumNonNegative(r.PromptTokens, r.OutputTokens, r.CacheReadTokens, r.CacheCreation5mTokens, r.CacheCreation1hTokens); overflow {
		recordAnomaly(relaybiz.UsageReasonOverflow)
		return AmbiguousEnvelope(env, relaybiz.UsageReasonOverflow)
	}

	switch {
	case (s.HasAnthropicCacheRead || s.HasAnthropicCacheCreation) && s.HasOpenAICachedDetail:
		// Conflicting protocol fields in one payload: no single canonical.
		recordAnomaly(relaybiz.UsageReasonProtocolFieldConflict)
		return AmbiguousEnvelope(env, relaybiz.UsageReasonProtocolFieldConflict)
	case s.HasAnthropicCacheRead || s.HasAnthropicCacheCreation:
		// Anthropic Messages: buckets are mutually exclusive; uncached =
		// input_tokens and nothing is subtracted (§2.4).
		env.Semantics = relaybiz.UsageSemanticsAnthropicExclusive
		env.ParseStatus = relaybiz.UsageParseVerified
		env.Canonical = &relaybiz.CanonicalUsage{
			UncachedInputTokens:   r.PromptTokens,
			CacheReadTokens:       r.CacheReadTokens,
			CacheCreation5mTokens: r.CacheCreation5mTokens,
			CacheCreation1hTokens: r.CacheCreation1hTokens,
			OutputTokens:          r.OutputTokens,
		}
	case cacheTotal > 0 && r.CacheReadTokens > r.PromptTokens:
		// cached exceeds the reported prompt/input: subset is impossible and
		// clamping would hide the conflict. Keep both candidates (§4.2).
		recordAnomaly(relaybiz.UsageReasonCachedExceedsReportedPrompt)
		return AmbiguousEnvelope(env, relaybiz.UsageReasonCachedExceedsReportedPrompt)
	default:
		// OpenAI Chat / Responses subset shape (or no cache at all): cached
		// tokens are part of the reported prompt/input.
		if cacheTotal > 0 {
			env.Semantics = relaybiz.UsageSemanticsOpenAISubset
		}
		env.ParseStatus = relaybiz.UsageParseVerified
		uncached := r.PromptTokens - r.CacheReadTokens
		if uncached < 0 {
			uncached = 0
		}
		env.Canonical = &relaybiz.CanonicalUsage{
			UncachedInputTokens:   uncached,
			CacheReadTokens:       r.CacheReadTokens,
			CacheCreation5mTokens: r.CacheCreation5mTokens,
			CacheCreation1hTokens: r.CacheCreation1hTokens,
			OutputTokens:          r.OutputTokens,
		}
	}
	if env.Canonical != nil && env.Canonical.IsEmpty() {
		// No bucket detail the parser can prove: downgrade to estimated. The
		// reported values stay on the envelope for audit, and a reported-total-
		// only payload estimates uncached input from that total (the estimator
		// still never fabricates cache buckets).
		est := EstimatedEnvelope(fallback)
		est.Reported = env.Reported
		if est.Canonical.UncachedInputTokens == 0 && r.TotalTokens > 0 {
			est.Canonical.UncachedInputTokens = r.TotalTokens
		}
		return est
	}
	return env
}

// AmbiguousEnvelope keeps the reported usage and both interpretations; the
// billing layer settles the user at the lower candidate cost (§5.2).
func AmbiguousEnvelope(env relaybiz.UsageEnvelope, reason string) relaybiz.UsageEnvelope {
	r := env.Reported
	env.ParseStatus = relaybiz.UsageParseAmbiguous
	env.DecisionReason = reason
	prompt := maxInt64(r.PromptTokens, 0)
	cacheRead := maxInt64(r.CacheReadTokens, 0)
	creation5m := maxInt64(r.CacheCreation5mTokens, 0)
	creation1h := maxInt64(r.CacheCreation1hTokens, 0)
	output := maxInt64(r.OutputTokens, 0)
	subsetUncached := prompt - cacheRead
	if subsetUncached < 0 {
		subsetUncached = 0
	}
	env.SubsetCandidate = &relaybiz.CanonicalUsage{
		UncachedInputTokens:   subsetUncached,
		CacheReadTokens:       cacheRead,
		CacheCreation5mTokens: creation5m,
		CacheCreation1hTokens: creation1h,
		OutputTokens:          output,
	}
	env.ExclusiveCandidate = &relaybiz.CanonicalUsage{
		UncachedInputTokens:   prompt,
		CacheReadTokens:       cacheRead,
		CacheCreation5mTokens: creation5m,
		CacheCreation1hTokens: creation1h,
		OutputTokens:          output,
	}
	return env
}

func extractEnvelopeFromValue(value any, fallback int64) relaybiz.UsageEnvelope {
	switch typed := value.(type) {
	case map[string]any:
		if nested, ok := typed["usage"]; ok {
			if ext, ok := extractUsageMap(nested); ok {
				return DecideEnvelope(ext.reported, ext.signals, fallback)
			}
		}
		if ext, ok := extractUsageMap(typed); ok {
			return DecideEnvelope(ext.reported, ext.signals, fallback)
		}
		for _, nested := range typed {
			if env := extractEnvelopeFromValue(nested, fallback); env.ParseStatus != relaybiz.UsageParseEstimated || env.CanonicalOrZero().UncachedInputTokens > 0 {
				return env
			}
		}
	case []any:
		for _, item := range typed {
			if env := extractEnvelopeFromValue(item, fallback); env.ParseStatus != relaybiz.UsageParseEstimated || env.CanonicalOrZero().UncachedInputTokens > 0 {
				return env
			}
		}
	}
	return EstimatedEnvelope(fallback)
}

// extractUsageMap reads the reported buckets and shape signals from a single
// usage-shaped map. ok is false when the map carries no recognizable usage.
func extractUsageMap(value any) (usageExtraction, bool) {
	m, ok := value.(map[string]any)
	if !ok {
		return usageExtraction{}, false
	}
	var ext usageExtraction
	prompt, hasPrompt := numberFieldPresent(m, "prompt_tokens")
	input, hasInput := numberFieldPresent(m, "input_tokens")
	ext.signals.HasPromptTokens = hasPrompt
	ext.signals.HasInputTokens = hasInput
	promptVal := prompt
	if !hasPrompt {
		promptVal = input
	}
	if promptVal < 0 {
		ext.signals.InvalidReason = relaybiz.UsageReasonNegativeBucket
	}
	completion := numberField(m, "completion_tokens", "output_tokens")
	if completion < 0 {
		ext.signals.InvalidReason = relaybiz.UsageReasonNegativeBucket
	}
	cacheRead := cacheReadTokensFromMap(m, &ext.signals)
	scanCacheShapeSignals(m, &ext.signals)
	fiveM, oneH, _, _ := cacheCreationDetailTokens(m, &ext.signals)
	if _, ok := m["cache_creation_input_tokens"]; ok {
		ext.signals.HasAnthropicCacheCreation = true
	}
	if _, isNested := m["cache_creation"].(map[string]any); isNested {
		ext.signals.HasAnthropicCacheCreation = true
	}
	if !hasPrompt && !hasInput && completion == 0 && cacheRead == 0 && fiveM == 0 && oneH == 0 {
		return usageExtraction{}, false
	}
	ext.reported = relaybiz.ReportedUsage{
		PromptTokens:          promptVal,
		OutputTokens:          completion,
		CacheReadTokens:       cacheRead,
		CacheCreation5mTokens: fiveM,
		CacheCreation1hTokens: oneH,
		TotalTokens:           numberField(m, "total_tokens"),
	}
	return ext, true
}

// scanCacheShapeSignals marks every cache-related field PRESENT in the map,
// independent of which field won the value extraction. Conflicting protocol
// markers in one payload (e.g. cache_read_input_tokens together with
// details.cached_tokens) must surface as protocol_field_conflict (§4.2), and
// they can only be detected by looking past the first match.
func scanCacheShapeSignals(m map[string]any, signals *FieldShapeSignals) {
	if _, ok := m["cache_read_input_tokens"]; ok {
		signals.HasAnthropicCacheRead = true
	}
	if _, ok := m["cache_read_tokens"]; ok {
		signals.HasFlatCacheRead = true
	}
	if _, ok := m["cached_tokens"]; ok {
		signals.HasOpenAICachedDetail = true
	}
	for _, key := range []string{"prompt_tokens_details", "input_tokens_details"} {
		details, ok := m[key].(map[string]any)
		if !ok {
			continue
		}
		if _, ok := details["cached_tokens"]; ok {
			signals.HasOpenAICachedDetail = true
		}
		if _, ok := details["cache_read_tokens"]; ok {
			signals.HasFlatCacheRead = true
		}
	}
}

// cacheReadTokensFromMap extracts cache-read tokens from a usage map,
// recording which field shape carried them.
func cacheReadTokensFromMap(m map[string]any, signals *FieldShapeSignals) int64 {
	if value, ok := numberFieldPresent(m, "cache_read_input_tokens"); ok {
		signals.HasAnthropicCacheRead = true
		if value < 0 {
			signals.InvalidReason = relaybiz.UsageReasonNegativeBucket
		}
		return value
	}
	if value, ok := numberFieldPresent(m, "cache_read_tokens"); ok {
		signals.HasFlatCacheRead = true
		if value < 0 {
			signals.InvalidReason = relaybiz.UsageReasonNegativeBucket
		}
		return value
	}
	if value, ok := numberFieldPresent(m, "cached_tokens"); ok {
		signals.HasOpenAICachedDetail = true
		if value < 0 {
			signals.InvalidReason = relaybiz.UsageReasonNegativeBucket
		}
		return value
	}
	for _, key := range []string{"prompt_tokens_details", "input_tokens_details"} {
		details, ok := m[key].(map[string]any)
		if !ok {
			continue
		}
		if value, ok := numberFieldPresent(details, "cached_tokens"); ok {
			signals.HasOpenAICachedDetail = true
			if value < 0 {
				signals.InvalidReason = relaybiz.UsageReasonNegativeBucket
			}
			return value
		}
		if value, ok := numberFieldPresent(details, "cache_read_tokens"); ok {
			signals.HasFlatCacheRead = true
			if value < 0 {
				signals.InvalidReason = relaybiz.UsageReasonNegativeBucket
			}
			return value
		}
	}
	return 0
}

// cacheCreationDetailTokens reads the cache-creation buckets from a usage map.
// It recognizes both the Anthropic nested detail shape
// (cache_creation.ephemeral_5m_input_tokens / ephemeral_1h_input_tokens) and
// the provider-level flattened shape (cache_creation_5m_tokens /
// cache_creation_1h_tokens), either at the usage top level or inside
// prompt_tokens_details / input_tokens_details.
func cacheCreationDetailTokens(m map[string]any, signals *FieldShapeSignals) (fiveM, oneH, flatTotal int64, hadDetail bool) {
	if raw := numberField(m, "cache_creation_input_tokens"); raw != 0 {
		flatTotal = raw
		if raw < 0 {
			signals.InvalidReason = relaybiz.UsageReasonNegativeBucket
		}
	}
	// Provider-level flattened buckets at top level or inside details.
	for _, key := range []string{"cache_creation_5m_tokens", "cache_creation_1h_tokens"} {
		if raw, present := numberFieldPresent(m, key); present {
			hadDetail = true
			if raw < 0 {
				signals.InvalidReason = relaybiz.UsageReasonNegativeBucket
			}
			if key == "cache_creation_5m_tokens" {
				fiveM = raw
			} else {
				oneH = raw
			}
		}
	}
	for _, detailsKey := range []string{"prompt_tokens_details", "input_tokens_details"} {
		details, ok := m[detailsKey].(map[string]any)
		if !ok {
			continue
		}
		if raw, present := numberFieldPresent(details, "cache_creation_5m_tokens"); present {
			hadDetail = true
			if raw < 0 {
				signals.InvalidReason = relaybiz.UsageReasonNegativeBucket
			}
			fiveM = raw
		}
		if raw, present := numberFieldPresent(details, "cache_creation_1h_tokens"); present {
			hadDetail = true
			if raw < 0 {
				signals.InvalidReason = relaybiz.UsageReasonNegativeBucket
			}
			oneH = raw
		}
	}
	// Anthropic nested detail shape.
	nested, _ := m["cache_creation"].(map[string]any)
	if nested != nil {
		if raw := numberField(nested, "ephemeral_5m_input_tokens"); raw != 0 {
			hadDetail = true
			if raw < 0 {
				signals.InvalidReason = relaybiz.UsageReasonNegativeBucket
			}
			fiveM = raw
		}
		if raw := numberField(nested, "ephemeral_1h_input_tokens"); raw != 0 {
			hadDetail = true
			if raw < 0 {
				signals.InvalidReason = relaybiz.UsageReasonNegativeBucket
			}
			oneH = raw
		}
	}
	if !hadDetail && flatTotal > 0 {
		// No TTL detail: default the flat total into the 5m bucket (ADR §4.2).
		fiveM = flatTotal
	}
	if hadDetail && flatTotal > 0 && fiveM+oneH > flatTotal {
		signals.InvalidReason = relaybiz.UsageReasonProtocolFieldConflict
	}
	return fiveM, oneH, flatTotal, hadDetail
}

func sumNonNegative(values ...int64) (int64, bool) {
	var total int64
	for _, value := range values {
		if value < 0 || value > math.MaxInt64-total {
			return 0, true
		}
		total += value
	}
	return total, false
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func recordAnomaly(reason string) {
	if metrics.TokenUsageParseAnomaly != nil {
		metrics.TokenUsageParseAnomaly.WithLabelValues(reason).Inc()
	}
}

func numberField(m map[string]any, keys ...string) int64 {
	for _, key := range keys {
		if value, ok := m[key]; ok {
			if number := int64Value(value); number != 0 {
				return number
			}
		}
	}
	return 0
}

// numberFieldPresent returns the value and whether the key existed at all, so
// shape signals can distinguish "absent" from "present but zero".
func numberFieldPresent(m map[string]any, key string) (int64, bool) {
	value, ok := m[key]
	if !ok {
		return 0, false
	}
	return int64Value(value), true
}

func int64Value(value any) int64 {
	switch v := value.(type) {
	case float64:
		return int64(v)
	case float32:
		return int64(v)
	case int:
		return int64(v)
	case int64:
		return v
	case int32:
		return int64(v)
	case jsonx.Number:
		n, _ := v.Int64()
		return n
	default:
		return 0
	}
}

// String keeps FieldShapeSignals debuggable in structured logs.
func (s FieldShapeSignals) String() string {
	return fmt.Sprintf("shape{%s}", s.FieldShape())
}
