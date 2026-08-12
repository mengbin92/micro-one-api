package apicompat

// Shared fixtures for the v0.19 protocol compatibility matrix
// (compatibility_matrix_test.go). Keeping the canonical input shapes here —
// instead of inline in every matrix cell — means the "what a web_search /
// server_tool_use / tool round-trip looks like" contract is defined once and
// reused by every path that must agree on it (OAuth adaptor, fallback, relay,
// chat round-trip).

import "micro-one-api/pkg/jsonx"

// matrixWebSearchResponsesRequest is a Responses API request whose history
// contains a replayed web_search_call output item and whose tools include ALL
// THREE server-side web-search tool variants (web_search, google_search,
// web_search_20250305 — the full drop list in convertResponsesToAnthropicTools)
// plus a regular function tool. Every Responses→Anthropic path must: skip the
// web_search_call item, drop every server tool variant, and preserve the
// function tool.
func matrixWebSearchResponsesRequest(stream bool) *ResponsesRequest {
	items := []ResponsesInputItem{
		{Role: "user", Content: raw(`"List the weather in Tokyo"`)},
		{
			Type:    "web_search_call",
			CallID:  "ws_123",
			Name:    "web_search",
			ID:      "item_ws_call",
			Content: raw(`[]`),
		},
		{Role: "assistant", Content: raw(`"Tokyo is 18C and sunny."`)},
	}
	input, _ := jsonx.Marshal(items)
	return &ResponsesRequest{
		Model:           "claude-sonnet-4-5",
		Instructions:    "Be concise.",
		Input:           input,
		MaxOutputTokens: intPtr(256),
		Stream:          stream,
		Tools: []ResponsesTool{
			{Type: "web_search", Name: "web_search"},
			{Type: "google_search", Name: "google_search"},
			{Type: "web_search_20250305", Name: "web_search"},
			{
				Type:        "function",
				Name:        "exec_command",
				Description: "run a shell command",
				Parameters:  raw(`{"type":"object","properties":{}}`),
			},
		},
	}
}

// matrixServerToolAnthropicResponse is a non-streaming Anthropic response
// containing a server-side web search block (server_tool_use +
// web_search_tool_result) that MUST be silently dropped, plus regular text
// that must survive.
func matrixServerToolAnthropicResponse() *AnthropicResponse {
	return &AnthropicResponse{
		ID:         "msg_ws_1",
		Type:       "message",
		Role:       "assistant",
		Model:      "claude-sonnet-4-5",
		StopReason: "end_turn",
		Content: []AnthropicContentBlock{
			{Type: "text", Text: "Tokyo is 18C and sunny."},
			{
				Type:  "server_tool_use",
				ID:    "toolu_ws_1",
				Name:  "web_search",
				Input: raw(`{"query":"Tokyo weather"}`),
			},
			{
				Type:      "web_search_tool_result",
				ToolUseID: "toolu_ws_1",
				Content:   raw(`[{"type":"text","text":"Tokyo: 18C, sunny."}]`),
			},
		},
		Usage: AnthropicUsage{InputTokens: 10, OutputTokens: 5},
	}
}

// matrixServerToolAnthropicStream builds a streaming Anthropic SSE sequence
// whose content_block_start carries the provider-only server_tool_use block.
// The converter must skip the block and its deltas (SkippingBlock) without
// emitting a Codex-incompatible item.
func matrixServerToolAnthropicStream() []*AnthropicStreamEvent {
	return []*AnthropicStreamEvent{
		{Type: "message_start", Message: matrixServerToolAnthropicResponse()},
		{Type: "content_block_start", Index: intPtr(0), ContentBlock: &AnthropicContentBlock{Type: "text", Text: "Tokyo"}},
		{Type: "content_block_delta", Index: intPtr(0), Delta: &AnthropicDelta{Type: "text_delta", Text: " is 18C"}},
		{Type: "content_block_stop", Index: intPtr(0)},
		// server_tool_use block: must be skipped entirely.
		{Type: "content_block_start", Index: intPtr(1), ContentBlock: &AnthropicContentBlock{Type: "server_tool_use", ID: "toolu_ws_2", Name: "web_search"}},
		{Type: "content_block_delta", Index: intPtr(1), Delta: &AnthropicDelta{Type: "input_json_delta", PartialJSON: `{"query":"Tokyo"}`}},
		{Type: "content_block_stop", Index: intPtr(1)},
		{Type: "message_delta", Delta: &AnthropicDelta{Type: "message_delta"}, Usage: &AnthropicUsage{OutputTokens: 7}},
		{Type: "message_stop"},
	}
}

// matrixChatToolRoundTrip is a Chat Completions conversation with an
// assistant tool call and the matching tool result. Round-tripping through
// Responses must preserve the call/result pairing.
func matrixChatToolRoundTrip(stream bool) *ChatCompletionsRequest {
	return &ChatCompletionsRequest{
		Model:  "gpt-5",
		Stream: stream,
		Messages: []ChatMessage{
			{Role: "system", Content: raw(`"You are helpful."`)},
			{Role: "user", Content: raw(`"What is 2+2? Use calc."`)},
			{
				Role:    "assistant",
				Content: raw(`"Let me compute."`),
				ToolCalls: []ChatToolCall{{
					ID:   "call_1",
					Type: "function",
					Function: ChatFunctionCall{
						Name:      "calc",
						Arguments: `{"expression":"2+2"}`,
					},
				}},
			},
			{Role: "tool", ToolCallID: "call_1", Content: raw(`"4"`)},
		},
		Tools: []ChatTool{{
			Type: "function",
			Function: &ChatFunction{
				Name:        "calc",
				Description: "evaluate an expression",
				Parameters:  raw(`{"type":"object","properties":{"expression":{"type":"string"}}}`),
			},
		}},
	}
}

// raw converts a literal JSON string to jsonx.RawMessage for fixtures.
func raw(s string) jsonx.RawMessage {
	return jsonx.RawMessage(s)
}
