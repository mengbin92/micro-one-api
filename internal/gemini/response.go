package gemini

import (
	"fmt"
	"math"

	"micro-one-api/pkg/jsonx"
)

// Usage keeps Gemini's inclusive input and separately reported thinking tokens.
// The five-bucket projection charges candidates + thoughts as output.
type Usage struct{ Prompt, Cached, Candidates, Thoughts, Total int64 }

type Response struct {
	Usage    *Usage
	Terminal bool
	ID       string
}

// ParseResponse verifies a text/function frame without projecting its content.
// Native bytes (including opaque thought signatures) remain intact.
func ParseResponse(body []byte) (Response, error) {
	var out Response
	if err := jsonx.ValidateUnique(body); err != nil {
		return out, fmt.Errorf("invalid Gemini response JSON")
	}
	var root map[string]jsonx.RawMessage
	if jsonx.Unmarshal(body, &root) != nil || root == nil {
		return out, fmt.Errorf("invalid Gemini response")
	}
	if _, exists := root["error"]; exists {
		return out, fmt.Errorf("Gemini upstream error")
	}
	_ = jsonx.Unmarshal(root["responseId"], &out.ID)
	var candidates []struct {
		Index        int              `json:"index"`
		Content      jsonx.RawMessage `json:"content"`
		FinishReason string           `json:"finishReason"`
	}
	if raw, ok := root["candidates"]; ok {
		if jsonx.Unmarshal(raw, &candidates) != nil || len(candidates) > 1 {
			return out, fmt.Errorf("invalid Gemini candidates")
		}
	}
	for _, candidate := range candidates {
		if candidate.Index != 0 {
			return out, fmt.Errorf("multiple candidates are unsupported")
		}
		if len(candidate.Content) != 0 && string(candidate.Content) != "null" {
			if err := validateContent(candidate.Content, false); err != nil {
				return out, fmt.Errorf("unsupported Gemini response content: %w", err)
			}
		}
		switch candidate.FinishReason {
		case "", "FINISH_REASON_UNSPECIFIED":
		case "STOP", "MAX_TOKENS", "SAFETY", "RECITATION", "LANGUAGE", "OTHER", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "MALFORMED_FUNCTION_CALL", "UNEXPECTED_TOOL_CALL", "TOO_MANY_TOOL_CALLS":
			out.Terminal = true
		default:
			return out, fmt.Errorf("unsupported Gemini finish reason")
		}
	}
	var feedback struct {
		BlockReason string `json:"blockReason"`
	}
	if raw, ok := root["promptFeedback"]; ok {
		if jsonx.Unmarshal(raw, &feedback) != nil {
			return out, fmt.Errorf("invalid Gemini promptFeedback")
		}
		if feedback.BlockReason != "" && feedback.BlockReason != "BLOCK_REASON_UNSPECIFIED" {
			out.Terminal = true
		}
	}
	if raw, ok := root["usageMetadata"]; ok {
		usage, err := parseUsage(raw)
		if err != nil {
			return out, err
		}
		out.Usage = &usage
	}
	if len(candidates) == 0 && !out.Terminal && out.Usage == nil {
		return out, fmt.Errorf("empty Gemini response")
	}
	return out, nil
}

func parseUsage(raw []byte) (Usage, error) {
	var out Usage
	fields, err := object(raw, "promptTokenCount", "cachedContentTokenCount", "candidatesTokenCount", "thoughtsTokenCount", "totalTokenCount", "toolUsePromptTokenCount", "promptTokensDetails", "cacheTokensDetails", "candidatesTokensDetails", "toolUsePromptTokensDetails", "serviceTier")
	if err != nil {
		return out, err
	}
	for key, target := range map[string]*int64{"promptTokenCount": &out.Prompt, "cachedContentTokenCount": &out.Cached, "candidatesTokenCount": &out.Candidates, "thoughtsTokenCount": &out.Thoughts, "totalTokenCount": &out.Total} {
		value, ok := fields[key]
		if !ok {
			if key == "promptTokenCount" || key == "totalTokenCount" {
				return out, fmt.Errorf("Gemini usage missing %s", key)
			}
			continue
		}
		if jsonx.Unmarshal(value, target) != nil || *target < 0 || *target > math.MaxInt32 {
			return out, fmt.Errorf("invalid Gemini usage %s", key)
		}
	}
	if out.Cached > out.Prompt || out.Total != out.Prompt+out.Candidates+out.Thoughts {
		return out, fmt.Errorf("inconsistent Gemini usage")
	}
	if raw, ok := fields["toolUsePromptTokenCount"]; ok {
		var value int64
		if jsonx.Unmarshal(raw, &value) != nil || value != 0 {
			return out, fmt.Errorf("server tool usage is unsupported")
		}
	}
	for _, key := range []string{"promptTokensDetails", "cacheTokensDetails", "candidatesTokensDetails", "toolUsePromptTokensDetails"} {
		if raw, ok := fields[key]; ok {
			var details []struct {
				Modality   string `json:"modality"`
				TokenCount int64  `json:"tokenCount"`
			}
			if jsonx.Unmarshal(raw, &details) != nil {
				return out, fmt.Errorf("invalid Gemini modality usage")
			}
			for _, detail := range details {
				if detail.Modality != "TEXT" || detail.TokenCount < 0 {
					return out, fmt.Errorf("non-text Gemini usage is unsupported")
				}
			}
		}
	}
	return out, nil
}
