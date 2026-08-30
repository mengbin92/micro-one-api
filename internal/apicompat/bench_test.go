// Benchmarks for the apicompat conversion hot paths, per v0.17-roadmap P3.2.
// These measure end-to-end conversion throughput (which internally performs
// many jsonx Marshal/Unmarshal calls) for the four shapes the relay handles on
// every request: Anthropic request -> Responses request, Responses request ->
// ChatCompletions request, Anthropic response -> Responses response, and
// Responses stream event -> SSE line.
//
// NOTE: results on Apple Silicon are smoke evidence only. Performance
// conclusions must be re-run on Linux/amd64 (see the roadmap).
package apicompat

import (
	"strings"
	"testing"

	"micro-one-api/pkg/jsonx"
)

// buildAnthropicBenchRequest mirrors a Claude-Code-style request: long system
// prompt, three messages with text/tool_use/tool_result content blocks, and
// two tools with JSON-schema parameters.
func buildAnthropicBenchRequest() *AnthropicRequest {
	return &AnthropicRequest{
		Model:     "claude-sonnet-5",
		MaxTokens: 8192,
		System:    jsonx.RawMessage(`"` + strings.Repeat("You are a coding assistant. ", 40) + `"`),
		Messages: []AnthropicMessage{
			{
				Role: "user",
				Content: jsonx.RawMessage(`[{"type":"text","text":"` +
					strings.Repeat("Explain the following codebase to me in detail. ", 25) +
					`"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGVsbG8="}}]`),
			},
			{
				Role: "assistant",
				Content: jsonx.RawMessage(`[{"type":"text","text":"Let me look at that."},` +
					`{"type":"tool_use","id":"toolu_01","name":"read_file","input":{"path":"/src/main.go"}}]`),
			},
			{
				Role: "user",
				Content: jsonx.RawMessage(`[{"type":"tool_result","tool_use_id":"toolu_01",` +
					`"content":"package main\n\nfunc main() {}\n"}]`),
			},
		},
		Tools: []AnthropicTool{
			{
				Type:        "custom",
				Name:        "read_file",
				Description: "Read a file from the repository",
				InputSchema: jsonx.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
			},
			{
				Type:        "custom",
				Name:        "search_code",
				Description: "Semantic search across the codebase",
				InputSchema: jsonx.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"limit":{"type":"integer"}},"required":["query"]}`),
			},
		},
		Stream:      false,
		Temperature: new(0.2),
		Thinking:    &AnthropicThinking{Type: "enabled", BudgetTokens: 4096},
		Metadata:    jsonx.RawMessage(`{"user_id":"user_bench_01"}`),
	}
}

// buildResponsesBenchRequest mirrors a Responses request with tools and a
// multi-turn input (used by the ChatCompletions bridge benchmark).
func buildResponsesBenchRequest() *ResponsesRequest {
	itemJSON := `{"type":"message","role":"user","content":[{"type":"input_text","text":"` +
		strings.Repeat("Summarize the attached conversation. ", 20) + `"}]}`
	maxOutputTokens := 2048
	return &ResponsesRequest{
		Model:           "gpt-5.2",
		Instructions:    strings.Repeat("You are a helpful assistant. ", 10),
		Input:           jsonx.RawMessage(`[` + itemJSON + `,` + itemJSON + `]`),
		MaxOutputTokens: &maxOutputTokens,
		Stream:          true,
		Tools: []ResponsesTool{
			{
				Type:        "function",
				Name:        "get_weather",
				Description: "Get current weather for a location",
				Parameters:  jsonx.RawMessage(`{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}`),
			},
		},
		Reasoning: &ResponsesReasoning{Effort: "high"},
	}
}

//go:fix inline
func float64Ptr(v float64) *float64 { return new(v) }

func BenchmarkAnthropicToResponses(b *testing.B) {
	req := buildAnthropicBenchRequest()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := AnthropicToResponses(req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkResponsesToChatCompletionsRequest(b *testing.B) {
	req := buildResponsesBenchRequest()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := ResponsesToChatCompletionsRequest(req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAnthropicToResponsesResponse(b *testing.B) {
	resp := &AnthropicResponse{
		ID:   "msg_bench_01",
		Type: "message",
		Role: "assistant",
		Content: []AnthropicContentBlock{
			{Type: "text", Text: strings.Repeat("Here is the answer. ", 40)},
			{Type: "tool_use", ID: "toolu_02", Name: "search_code", Input: jsonx.RawMessage(`{"query":"benchmark"}`)},
		},
		Model:      "claude-sonnet-5",
		StopReason: "tool_use",
		Usage: AnthropicUsage{
			InputTokens:              1520,
			OutputTokens:             640,
			CacheCreationInputTokens: 500,
			CacheReadInputTokens:     1000,
		},
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if got := AnthropicToResponsesResponse(resp); got == nil {
			b.Fatal("nil response")
		}
	}
}

func BenchmarkResponsesEventToSSE(b *testing.B) {
	evt := ResponsesStreamEvent{
		Type:           "response.output_text.delta",
		OutputIndex:    0,
		ContentIndex:   0,
		Delta:          strings.Repeat("streaming text ", 30),
		ItemID:         "msg_001",
		SequenceNumber: 42,
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := ResponsesEventToSSE(evt); err != nil {
			b.Fatal(err)
		}
	}
}
