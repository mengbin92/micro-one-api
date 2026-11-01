package server

import (
	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/pkg/jsonx"
)

// DeepSeek's documented hit/miss fields describe the same inclusive input
// bucket as OpenAI cached_tokens. Prove their identity before projecting the
// alias into the existing A3 parser; a disagreement cannot become zero cache.
// See https://api-docs.deepseek.com/api/create-chat-completion/ .
func normalizeTierCacheAliases(raw jsonx.RawMessage) (jsonx.RawMessage, bool) {
	var fields map[string]jsonx.RawMessage
	if jsonx.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil, false
	}
	hitRaw, hitPresent := fields["prompt_cache_hit_tokens"]
	missRaw, missPresent := fields["prompt_cache_miss_tokens"]
	if !hitPresent && !missPresent {
		return raw, true
	}
	if !hitPresent || !missPresent {
		return nil, false
	}
	var hit, miss, input *int64
	inputKey, detailsKey := "prompt_tokens", "prompt_tokens_details"
	if _, ok := fields[inputKey]; !ok {
		inputKey, detailsKey = "input_tokens", "input_tokens_details"
	}
	if jsonx.Unmarshal(hitRaw, &hit) != nil || jsonx.Unmarshal(missRaw, &miss) != nil || jsonx.Unmarshal(fields[inputKey], &input) != nil || hit == nil || miss == nil || input == nil || *hit < 0 || *miss < 0 || *input < 0 || *hit > *input || *miss != *input-*hit {
		return nil, false
	}
	checkRead := func(value jsonx.RawMessage) bool {
		var count *int64
		return jsonx.Unmarshal(value, &count) == nil && count != nil && *count == *hit
	}
	for _, key := range []string{"cached_tokens", "cache_read_tokens", "cache_read_input_tokens"} {
		if value, ok := fields[key]; ok && !checkRead(value) {
			return nil, false
		}
	}
	for _, key := range []string{"prompt_tokens_details", "input_tokens_details"} {
		value, ok := fields[key]
		if !ok {
			continue
		}
		var details map[string]jsonx.RawMessage
		if jsonx.Unmarshal(value, &details) != nil || details == nil {
			return nil, false
		}
		for _, alias := range []string{"cached_tokens", "cache_read_tokens"} {
			if value, ok := details[alias]; ok && !checkRead(value) {
				return nil, false
			}
		}
	}
	var details map[string]jsonx.RawMessage
	if value, ok := fields[detailsKey]; ok {
		if jsonx.Unmarshal(value, &details) != nil || details == nil {
			return nil, false
		}
	} else {
		details = make(map[string]jsonx.RawMessage)
	}
	details["cached_tokens"] = hitRaw
	encoded, err := jsonx.Marshal(details)
	if err != nil {
		return nil, false
	}
	fields[detailsKey] = encoded
	normalized, err := jsonx.Marshal(fields)
	return normalized, err == nil
}

// The typed OpenAI-compatible provider retains raw usage so presence and
// aliases can be verified before optional detail fields lose that evidence.
func completeTierChatUsage(u relayprovider.Usage, canonical *relayprovider.CanonicalUsage, raw jsonx.RawMessage) (relayprovider.Usage, relaybiz.UsageEnvelope, bool) {
	if !validTierUsageNumbers(raw) {
		return u, relaybiz.UsageEnvelope{}, false
	}
	normalized, ok := normalizeTierCacheAliases(raw)
	if !ok {
		return u, relaybiz.UsageEnvelope{}, false
	}
	var observed relayprovider.Usage
	if jsonx.Unmarshal(normalized, &observed) != nil {
		return u, relaybiz.UsageEnvelope{}, false
	}
	envelope := envelopeFromCompleteTierChatUsage(observed, canonical)
	return observed, envelope, envelope.ParseStatus == relaybiz.UsageParseVerified
}
