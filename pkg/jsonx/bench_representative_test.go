// Package jsonx_test benchmarks representative JSON payload shapes found in
// micro-one-api hot paths — Responses/Anthropic streaming events and
// admin/billing aggregation responses — through both jsonx (sonic ConfigStd)
// and encoding/json. This is the P3.2 evidence base for the "keep sonic vs
// fall back Marshal to std" decision (docs/design/v0.17-roadmap.md §P3).
//
// The fixtures mirror the real struct shapes from internal/apicompat without
// importing the package: an external test package (jsonx_test) is used because
// apicompat imports jsonx, so a same-package test file referencing apicompat
// would create an import cycle.
//
// NOTE: results on Apple Silicon are smoke evidence only. Performance
// conclusions must be re-run on Linux/amd64 (see the roadmap).
package jsonx_test

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"micro-one-api/internal/apicompat"
	"micro-one-api/pkg/jsonx"
)

// ---------------------------------------------------------------------------
// Fixtures — shapes copied from internal/apicompat hot paths
// ---------------------------------------------------------------------------

// benchLargeResponses mirrors a non-streaming POST /v1/responses reply: three
// output items (reasoning + message with a long text block + function_call),
// five-bucket usage. This is the largest single JSON document the relay
// serializes per request on the Responses path.
var benchLargeResponses = &apicompat.ResponsesResponse{
	ID:     "resp_bench_001",
	Object: "response",
	Model:  "gpt-5.2",
	Status: "completed",
	Output: []apicompat.ResponsesOutput{
		{
			Type:             "reasoning",
			EncryptedContent: strings.Repeat("enc:abc123", 40),
			Summary:          []apicompat.ResponsesSummary{{Type: "summary_text", Text: strings.Repeat("reasoning summary block ", 20)}},
		},
		{
			Type:   "message",
			ID:     "msg_001",
			Role:   "assistant",
			Status: "completed",
			Content: []apicompat.ResponsesContentPart{
				{Type: "output_text", Text: strings.Repeat("This is a representative long LLM output block. ", 30)},
			},
		},
		{
			Type:      "function_call",
			CallID:    "call_1",
			Name:      "get_weather",
			Arguments: `{"location":"San Francisco","unit":"celsius","limit":5,"filters":{"tags":["urgent","billing"]}}`,
		},
	},
	Usage: &apicompat.ResponsesUsage{
		InputTokens:  1520,
		OutputTokens: 640,
		TotalTokens:  2160,
		InputTokensDetails: &apicompat.ResponsesInputTokensDetails{
			CachedTokens:          1000,
			CacheCreation5mTokens: 500,
			CacheCreation1hTokens: 20,
		},
		OutputTokensDetails: &apicompat.ResponsesOutputTokensDetails{ReasoningTokens: 300},
	},
}

// benchAnthropicDelta mirrors one Anthropic SSE content_block_delta event with
// a long text delta — the per-event serialization point on the adaptor path.
var benchAnthropicDelta = &apicompat.AnthropicStreamEvent{
	Type:  "content_block_delta",
	Index: new(0),
	Delta: &apicompat.AnthropicDelta{
		Type: "text_delta",
		Text: strings.Repeat("streaming delta text ", 60),
	},
}

// benchAggMapSlice mirrors an admin/billing aggregation response: a small
// map envelope holding a slice of per-model rows (the shape produced by
// billing aggregation RPCs and echoed through admin-api).
var benchAggMapSlice = map[string]any{
	"total_requests": 12800,
	"total_tokens":   512000,
	"total_cost_usd": 31.2048,
	"items": []any{
		map[string]any{"model": "gpt-4o-mini", "tokens": 152000, "cost_usd": 9.3100, "requests": 4100},
		map[string]any{"model": "gpt-4o", "tokens": 98000, "cost_usd": 12.7400, "requests": 2300},
		map[string]any{"model": "claude-5.2", "tokens": 120000, "cost_usd": 6.4000, "requests": 3100},
		map[string]any{"model": "gemini-2.5-pro", "tokens": 72000, "cost_usd": 2.1548, "requests": 1900},
		map[string]any{"model": "deepseek-r1", "tokens": 70000, "cost_usd": 0.6000, "requests": 1400},
	},
}

var benchLargeResponsesPayload = mustMarshal(benchLargeResponses)
var benchAnthropicDeltaPayload = mustMarshal(benchAnthropicDelta)
var benchAggMapSlicePayload = mustMarshal(benchAggMapSlice)

//go:fix inline
func intPtr(v int) *int { return new(v) }

func mustMarshal(v any) []byte {
	b, err := jsonx.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// ---------------------------------------------------------------------------
// Large Responses response — Marshal
// ---------------------------------------------------------------------------

func BenchmarkMarshalLargeResponsesJSONX(b *testing.B) {
	b.ReportAllocs()
	b.SetBytes(int64(len(benchLargeResponsesPayload)))
	for i := 0; i < b.N; i++ {
		if _, err := jsonx.Marshal(benchLargeResponses); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshalLargeResponsesStd(b *testing.B) {
	b.ReportAllocs()
	b.SetBytes(int64(len(benchLargeResponsesPayload)))
	for i := 0; i < b.N; i++ {
		if _, err := json.Marshal(benchLargeResponses); err != nil {
			b.Fatal(err)
		}
	}
}

// ---------------------------------------------------------------------------
// Large Responses response — Unmarshal
// ---------------------------------------------------------------------------

func BenchmarkUnmarshalLargeResponsesJSONX(b *testing.B) {
	b.ReportAllocs()
	b.SetBytes(int64(len(benchLargeResponsesPayload)))
	for i := 0; i < b.N; i++ {
		var resp apicompat.ResponsesResponse
		if err := jsonx.Unmarshal(benchLargeResponsesPayload, &resp); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkUnmarshalLargeResponsesStd(b *testing.B) {
	b.ReportAllocs()
	b.SetBytes(int64(len(benchLargeResponsesPayload)))
	for i := 0; i < b.N; i++ {
		var resp apicompat.ResponsesResponse
		if err := json.Unmarshal(benchLargeResponsesPayload, &resp); err != nil {
			b.Fatal(err)
		}
	}
}

// ---------------------------------------------------------------------------
// Anthropic streaming delta event — Marshal (per-event SSE hot path)
// ---------------------------------------------------------------------------

func BenchmarkMarshalAnthropicDeltaJSONX(b *testing.B) {
	b.ReportAllocs()
	b.SetBytes(int64(len(benchAnthropicDeltaPayload)))
	for i := 0; i < b.N; i++ {
		if _, err := jsonx.Marshal(benchAnthropicDelta); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshalAnthropicDeltaStd(b *testing.B) {
	b.ReportAllocs()
	b.SetBytes(int64(len(benchAnthropicDeltaPayload)))
	for i := 0; i < b.N; i++ {
		if _, err := json.Marshal(benchAnthropicDelta); err != nil {
			b.Fatal(err)
		}
	}
}

// ---------------------------------------------------------------------------
// Admin/billing aggregation response — map + slice — Marshal/Unmarshal
// ---------------------------------------------------------------------------

func BenchmarkMarshalAggMapSliceJSONX(b *testing.B) {
	b.ReportAllocs()
	b.SetBytes(int64(len(benchAggMapSlicePayload)))
	for i := 0; i < b.N; i++ {
		if _, err := jsonx.Marshal(benchAggMapSlice); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshalAggMapSliceStd(b *testing.B) {
	b.ReportAllocs()
	b.SetBytes(int64(len(benchAggMapSlicePayload)))
	for i := 0; i < b.N; i++ {
		if _, err := json.Marshal(benchAggMapSlice); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkUnmarshalAggMapSliceJSONX(b *testing.B) {
	b.ReportAllocs()
	b.SetBytes(int64(len(benchAggMapSlicePayload)))
	for i := 0; i < b.N; i++ {
		var m map[string]any
		if err := jsonx.Unmarshal(benchAggMapSlicePayload, &m); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkUnmarshalAggMapSliceStd(b *testing.B) {
	b.ReportAllocs()
	b.SetBytes(int64(len(benchAggMapSlicePayload)))
	for i := 0; i < b.N; i++ {
		var m map[string]any
		if err := json.Unmarshal(benchAggMapSlicePayload, &m); err != nil {
			b.Fatal(err)
		}
	}
}

// ---------------------------------------------------------------------------
// NewEncoder path — large Responses response written to a sink
// (independent decision from Marshal; encoder allocates per request)
// ---------------------------------------------------------------------------

func BenchmarkEncoderLargeResponsesJSONX(b *testing.B) {
	b.ReportAllocs()
	b.SetBytes(int64(len(benchLargeResponsesPayload)))
	for i := 0; i < b.N; i++ {
		enc := jsonx.NewEncoder(io.Discard)
		if err := enc.Encode(benchLargeResponses); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEncoderLargeResponsesStd(b *testing.B) {
	b.ReportAllocs()
	b.SetBytes(int64(len(benchLargeResponsesPayload)))
	for i := 0; i < b.N; i++ {
		enc := json.NewEncoder(io.Discard)
		if err := enc.Encode(benchLargeResponses); err != nil {
			b.Fatal(err)
		}
	}
}
