// Package usage provides provider-agnostic token-bucket extraction from raw
// upstream response JSON. It lives below package server so both the server
// handlers and the forwarder sub-package can use it without creating an import
// cycle.
package usage

import (
	"micro-one-api/pkg/jsonx"

	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/platform/metrics"
)

// ExtractFromJSON parses a raw upstream response body and returns a canonical
// bucketed usage view. It mirrors the normalization rules in
// internal/server/http_raw_helpers.go but emits the CanonicalUsage type used by
// the orchestrator and forwarder.
func ExtractFromJSON(body []byte, fallback int64, promptExclusive bool) relaybiz.CanonicalUsage {
	var payload any
	if err := jsonx.Unmarshal(body, &payload); err != nil {
		return relaybiz.CanonicalUsage{TotalTokens: fallback, PromptExclusive: promptExclusive}
	}
	u := extractFromValue(payload, fallback)
	u.PromptExclusive = promptExclusive
	return u
}

func extractFromValue(value any, fallback int64) relaybiz.CanonicalUsage {
	switch typed := value.(type) {
	case map[string]any:
		var usage relaybiz.CanonicalUsage
		if nested, ok := typed["usage"]; ok {
			usage = extractFromValue(nested, 0)
		}
		fiveM, oneH, _, _ := cacheCreationDetailTokens(typed)
		prompt := numberField(typed, "prompt_tokens", "input_tokens")
		if prompt < 0 {
			recordAnomaly("negative")
			prompt = 0
		}
		completion := numberField(typed, "completion_tokens", "output_tokens")
		if completion < 0 {
			recordAnomaly("negative")
			completion = 0
		}
		usage = mergeUsage(usage, relaybiz.CanonicalUsage{
			PromptTokens:          prompt,
			CompletionTokens:      completion,
			CacheReadTokens:       cacheReadTokensFromMap(typed),
			CacheCreation5mTokens: fiveM,
			CacheCreation1hTokens: oneH,
			TotalTokens:           numberField(typed, "total_tokens"),
		})
		if !usage.IsEmpty() {
			return normalize(usage, fallback)
		}
		for _, nested := range typed {
			usage = extractFromValue(nested, 0)
			if !usage.IsEmpty() {
				return normalize(usage, fallback)
			}
		}
	case []any:
		for _, item := range typed {
			usage := extractFromValue(item, 0)
			if !usage.IsEmpty() {
				return normalize(usage, fallback)
			}
		}
	}
	return relaybiz.CanonicalUsage{TotalTokens: fallback}
}

func mergeUsage(primary, fallback relaybiz.CanonicalUsage) relaybiz.CanonicalUsage {
	if primary.PromptTokens == 0 {
		primary.PromptTokens = fallback.PromptTokens
	}
	if primary.CompletionTokens == 0 {
		primary.CompletionTokens = fallback.CompletionTokens
	}
	if primary.CacheReadTokens == 0 {
		primary.CacheReadTokens = fallback.CacheReadTokens
	}
	if primary.CacheCreation5mTokens == 0 {
		primary.CacheCreation5mTokens = fallback.CacheCreation5mTokens
	}
	if primary.CacheCreation1hTokens == 0 {
		primary.CacheCreation1hTokens = fallback.CacheCreation1hTokens
	}
	if primary.TotalTokens == 0 {
		primary.TotalTokens = fallback.TotalTokens
	}
	return primary
}

func normalize(usage relaybiz.CanonicalUsage, fallback int64) relaybiz.CanonicalUsage {
	if usage.TotalTokens == 0 {
		if derived := usage.DerivedTotal(); derived > 0 {
			usage.TotalTokens = derived
		}
	}
	if usage.TotalTokens <= 0 {
		usage.TotalTokens = fallback
	}
	if usage.PromptTokens == 0 {
		usage.PromptTokens = fallback
	}
	if usage.CompletionTokens == 0 {
		usage.CompletionTokens = fallback
	}
	return usage
}

// cacheReadTokensFromMap extracts cache-read tokens from a usage map, checking
// both flat keys and nested *_details objects.
func cacheReadTokensFromMap(m map[string]any) int64 {
	if value := numberField(m, "cache_read_input_tokens", "cache_read_tokens", "cached_tokens"); value != 0 {
		if value < 0 {
			recordAnomaly("negative")
			return 0
		}
		return value
	}
	for _, key := range []string{"prompt_tokens_details", "input_tokens_details"} {
		details, ok := m[key].(map[string]any)
		if !ok {
			continue
		}
		if value := numberField(details, "cache_read_tokens", "cached_tokens"); value != 0 {
			if value < 0 {
				recordAnomaly("negative")
				return 0
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
func cacheCreationDetailTokens(m map[string]any) (fiveM, oneH, flatTotal int64, hadDetail bool) {
	if raw := numberField(m, "cache_creation_input_tokens"); raw != 0 {
		if raw < 0 {
			recordAnomaly("negative")
		} else {
			flatTotal = raw
		}
	}
	// Provider-level flattened buckets at top level or inside details.
	for _, key := range []string{"cache_creation_5m_tokens", "cache_creation_1h_tokens"} {
		if raw := numberField(m, key); raw > 0 {
			hadDetail = true
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
		if raw := numberField(details, "cache_creation_5m_tokens"); raw > 0 {
			hadDetail = true
			fiveM = raw
		}
		if raw := numberField(details, "cache_creation_1h_tokens"); raw > 0 {
			hadDetail = true
			oneH = raw
		}
	}
	// Anthropic nested detail shape.
	nested, _ := m["cache_creation"].(map[string]any)
	if nested != nil {
		if raw := numberField(nested, "ephemeral_5m_input_tokens"); raw != 0 {
			hadDetail = true
			if raw < 0 {
				recordAnomaly("negative")
			} else {
				fiveM = raw
			}
		}
		if raw := numberField(nested, "ephemeral_1h_input_tokens"); raw != 0 {
			hadDetail = true
			if raw < 0 {
				recordAnomaly("negative")
			} else {
				oneH = raw
			}
		}
	}
	if !hadDetail && flatTotal > 0 {
		// No TTL detail: default the flat total into the 5m bucket (ADR §4.2).
		fiveM = flatTotal
	}
	if hadDetail && flatTotal > 0 && fiveM+oneH > flatTotal {
		recordAnomaly("ttl_detail_exceeds_total")
	}
	return fiveM, oneH, flatTotal, hadDetail
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
