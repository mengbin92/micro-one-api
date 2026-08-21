package provider

import (
	"strings"

	"micro-one-api/pkg/jsonx"
)

// isKimiK3Model reports whether model uses the Kimi K3 request contract.
// K3 model aliases may carry a deployment suffix, so match the normalized
// prefix rather than only the exact catalog spelling.
func isKimiK3Model(model string) bool {
	normalized := strings.ToLower(strings.TrimSpace(model))
	return normalized == "kimi-k3" || strings.HasPrefix(normalized, "kimi-k3-")
}

// normalizeKimiK3ChatRequest adapts the typed Chat Completions request to the
// Kimi K3 contract. K3 always reasons and rejects/does not need explicit
// sampling parameters; max_tokens is accepted by the gateway but K3 expects
// max_completion_tokens. See the official limits:
// https://platform.kimi.com/docs/guide/kimi-k3-quickstart#%E9%87%8D%E8%A6%81%E9%99%90%E5%88%B6
func normalizeKimiK3ChatRequest(req *ChatCompletionsRequest) *ChatCompletionsRequest {
	if req == nil || !isKimiK3Model(req.Model) {
		return req
	}

	normalized := *req
	normalized.Temperature = nil
	if normalized.MaxCompletionTokens == nil {
		normalized.MaxCompletionTokens = normalized.MaxTokens
	}
	normalized.MaxTokens = nil
	return &normalized
}

// normalizeKimiK3ChatBody adapts raw chat requests used by the fallback/raw
// forwarding paths. It removes fixed sampling parameters even when they were
// not represented by the typed request and maps the OpenAI legacy token name.
func normalizeKimiK3ChatBody(body []byte) ([]byte, error) {
	var payload map[string]jsonx.RawMessage
	if err := jsonx.Unmarshal(body, &payload); err != nil {
		return nil, err
	}

	var model string
	if rawModel, ok := payload["model"]; ok {
		if err := jsonx.Unmarshal(rawModel, &model); err != nil {
			return nil, err
		}
	}
	if !isKimiK3Model(model) {
		return body, nil
	}

	for _, key := range []string{"temperature", "top_p", "n", "presence_penalty", "frequency_penalty"} {
		delete(payload, key)
	}
	if _, ok := payload["max_completion_tokens"]; !ok {
		if maxTokens, ok := payload["max_tokens"]; ok {
			payload["max_completion_tokens"] = maxTokens
		}
	}
	delete(payload, "max_tokens")

	return jsonx.Marshal(payload)
}

func isChatCompletionsPath(path string) bool {
	return strings.HasSuffix(strings.TrimRight(path, "/"), "/chat/completions")
}
