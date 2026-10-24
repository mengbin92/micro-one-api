package evidence

import (
	"fmt"
	"math"

	"micro-one-api/pkg/jsonx"
)

const DeepSeekCandidateParser = "candidate-deepseek-chat-2026-10-14.1"
const DeepSeekCandidateContract = "candidate-deepseek-official-cache-alias-2026-10-14.1"

// ParseDeepSeekCandidate is an explicitly vendor-bound parser algorithm used by
// offline replay and independent contract proof. Parse and charging stay unchanged.
// The official API defines flat cache hit/miss as a partition of prompt_tokens
// and cached_tokens as the same hit count. Unknown dimensions stay unsupported.
func ParseDeepSeekCandidate(body []byte) Observation {
	original := Parse(body, "openai_chat")
	original.ParserRevision, original.ContractRevision = DeepSeekCandidateParser, DeepSeekCandidateContract
	if original.Status == "unobserved" {
		return original
	}
	root, err := decodeUnique(body)
	if err != nil {
		return original
	}
	m, ok := root.(map[string]any)
	if !ok {
		return original
	}
	raw, present := m["usage"]
	if !present {
		return original
	}
	if raw == nil {
		if m["object"] == "chat.completion.chunk" && original.Terminal == "" {
			original.Status, original.Reason = "missing", "usage_absent"
		} else {
			original.Status, original.Reason = "invalid", "usage_not_object"
		}
		return original
	}
	usage, ok := raw.(map[string]any)
	if !ok {
		return original
	}
	fail := func(status, reason string) Observation {
		original.Status, original.Reason, original.Buckets = status, reason, nil
		return original
	}
	count := func(key string) (int64, bool, error) {
		v, exists := usage[key]
		if !exists {
			return 0, false, nil
		}
		n, ok := v.(jsonx.Number)
		if !ok {
			return 0, true, fmt.Errorf("token_type")
		}
		value, err := n.Int64()
		if err != nil || value < 0 {
			return 0, true, fmt.Errorf("token_integer_or_range")
		}
		return value, true, nil
	}
	hit, hasHit, e1 := count("prompt_cache_hit_tokens")
	miss, hasMiss, e2 := count("prompt_cache_miss_tokens")
	prompt, hasPrompt, e3 := count("prompt_tokens")
	if e1 != nil || e2 != nil || e3 != nil {
		return fail("invalid", "deepseek_cache_alias_type_or_range")
	}
	if !hasHit || !hasMiss || !hasPrompt {
		return fail("missing", "deepseek_cache_partition_absent")
	}
	if hit > math.MaxInt64-miss {
		return fail("invalid", "deepseek_cache_partition_overflow")
	}
	if hit+miss != prompt {
		return fail("ambiguous", "deepseek_cache_partition_conflict")
	}
	total, hasTotal, e4 := count("total_tokens")
	output, hasOutput, e5 := count("completion_tokens")
	if e4 != nil || e5 != nil {
		return fail("invalid", "deepseek_total_type_or_range")
	}
	if hasOutput && hasTotal {
		if prompt > math.MaxInt64-output {
			return fail("invalid", "deepseek_total_overflow")
		}
		if prompt+output != total {
			return fail("ambiguous", "deepseek_total_conflict")
		}
	}
	details := map[string]any{}
	if d, exists := usage["prompt_tokens_details"]; exists {
		var ok bool
		details, ok = d.(map[string]any)
		if !ok {
			return fail("invalid", "detail_not_object")
		}
		if cached, exists := details["cached_tokens"]; exists {
			n, ok := cached.(jsonx.Number)
			if !ok {
				return fail("invalid", "token_type")
			}
			value, err := n.Int64()
			if err != nil || value < 0 {
				return fail("invalid", "token_integer_or_range")
			}
			if value != hit {
				return fail("ambiguous", "deepseek_cache_alias_conflict")
			}
		}
	}
	// Normalize only the independently defined equivalent fields for the core
	// five-bucket verifier. Return the original raw evidence, never the projection.
	details["cached_tokens"] = jsonx.Number(fmt.Sprint(hit))
	usage["prompt_tokens_details"] = details
	delete(usage, "prompt_cache_hit_tokens")
	delete(usage, "prompt_cache_miss_tokens")
	normalized, err := jsonx.Marshal(map[string]any{"usage": usage})
	if err != nil {
		return fail("invalid", "normalization_failed")
	}
	candidate := Parse(normalized, "openai_chat")
	original.Status, original.Reason, original.Buckets, original.Semantics = candidate.Status, candidate.Reason, candidate.Buckets, candidate.Semantics
	return original
}
func NewDeepSeekCandidateTracker() *Tracker {
	return &Tracker{protocol: "openai_chat", usage: map[string]any{}, candidateParser: ParseDeepSeekCandidate}
}
