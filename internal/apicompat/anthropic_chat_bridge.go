package apicompat

import (
	"fmt"

	"micro-one-api/pkg/jsonx"
)

// AnthropicToChatCompletionsRequest composes the Anthropic -> Responses and
// Responses -> Chat bridges while preserving fields that Responses does not
// represent on the wire, such as stop_sequences. It is the canonical request
// bridge for Anthropic clients routed to OpenAI-compatible upstreams.
func AnthropicToChatCompletionsRequest(req *AnthropicRequest) (*ChatCompletionsRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("anthropic request is nil")
	}

	responsesReq, err := AnthropicToResponses(req)
	if err != nil {
		return nil, err
	}
	chatReq, err := ResponsesToChatCompletionsRequest(responsesReq)
	if err != nil {
		return nil, err
	}

	// Anthropic max_tokens maps to the widely supported Chat Completions
	// max_tokens field. Do not retain the Responses bridge's
	// max_completion_tokens form: DeepSeek-compatible endpoints may reject it.
	if req.MaxTokens > 0 {
		maxTokens := min(req.MaxTokens, 64000)
		chatReq.MaxTokens = &maxTokens
		chatReq.MaxCompletionTokens = nil
	}

	if len(req.StopSeqs) > 0 {
		stop, err := jsonx.Marshal(req.StopSeqs)
		if err != nil {
			return nil, fmt.Errorf("marshal stop_sequences: %w", err)
		}
		chatReq.Stop = stop
	}

	// AnthropicToResponses intentionally defaults reasoning effort for Codex
	// Responses upstreams. Generic Chat endpoints must not receive that field
	// unless the Anthropic caller explicitly requested thinking controls.
	if req.Thinking == nil && req.OutputConfig == nil {
		chatReq.ReasoningEffort = ""
	}

	if req.Stream {
		chatReq.StreamOptions = &ChatStreamOptions{IncludeUsage: true}
	}
	return chatReq, nil
}

// ChatCompletionsResponseToAnthropic converts a complete Chat Completions
// response through the Responses hub and rejects malformed tool arguments
// before they can be serialized as an Anthropic tool_use input object.
func ChatCompletionsResponseToAnthropic(resp *ChatCompletionsResponse, model string) (*AnthropicResponse, error) {
	responsesResp := ChatCompletionsResponseToResponses(resp, model)
	if err := validateResponsesToolArguments(responsesResp.Output); err != nil {
		return nil, err
	}
	return ResponsesToAnthropic(responsesResp, model), nil
}

func validateResponsesToolArguments(output []ResponsesOutput) error {
	for _, item := range output {
		if item.Type != "function_call" && item.Type != "custom_tool_call" {
			continue
		}
		raw := sanitizeAnthropicToolUseInput(item.Name, item.Arguments)
		if !validJSONObject(raw) {
			return fmt.Errorf("tool %q returned invalid JSON object arguments", item.Name)
		}
	}
	return nil
}

func validJSONObject(raw jsonx.RawMessage) bool {
	if len(raw) == 0 || !jsonx.Valid(raw) {
		return false
	}
	var object map[string]jsonx.RawMessage
	return jsonx.Unmarshal(raw, &object) == nil && object != nil
}
