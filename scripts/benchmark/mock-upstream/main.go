// Command mock-upstream is a deterministic, dependency-free OpenAI-compatible
// upstream server for the relay-gateway k6 baseline benchmark.
//
// It serves the three request shapes the relay-gateway forwards to an
// upstream provider:
//
//	POST /v1/chat/completions        — non-streaming chat completion
//	GET  /v1/models                  — models list
//	GET  /healthz                    — health probe (used by the benchmark harness)
//
// Every response is deterministic (fixed IDs, fixed token counts, no wall-clock
// RNG in the payload) so k6 measures relay-gateway overhead, not upstream
// variance. A small fixed processing delay is applied to keep latency in a
// realistic band; configure via -delay-ms (default 2ms).
//
// Usage:
//
//	go run ./scripts/benchmark/mock-upstream -addr 127.0.0.1:18099
//
// The relay-gateway's channel/provider config must point its upstream base URL
// at http://127.0.0.1:18099 (or whatever address you pass via -addr).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"
)

// Fixed, deterministic values — never vary between runs.
const (
	mockChatCompletionID = "chatcmpl-mock-baseline-0001"
	mockModelID          = "gpt-3.5-turbo"
	mockAssistantContent = "ok"
	promptTokens         = 10
	completionTokens     = 5
	totalTokens          = 15
	createdFixed         = 1_700_000_000 // fixed epoch; avoids payload variance
)

var delay time.Duration

// --- Request body shapes (only the fields we need to echo back) -------------

type chatRequest struct {
	Model    string `json:"model"`
	Stream   bool   `json:"stream"`
	Messages []struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
	} `json:"messages"`
}

// --- Response shapes (OpenAI-compatible) ------------------------------------

type chatCompletionResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []choice `json:"choices"`
	Usage   usage    `json:"usage"`
}

type choice struct {
	Index        int     `json:"index"`
	Message      message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type modelObject struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type modelsListResponse struct {
	Object string        `json:"object"`
	Data   []modelObject `json:"data"`
}

func main() {
	addr := flag.String("addr", "127.0.0.1:18099", "listen address")
	delayMs := flag.Int("delay-ms", 2, "fixed per-request processing delay in milliseconds")
	flag.Parse()

	delay = time.Duration(*delayMs) * time.Millisecond

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", handleHealth)
	mux.HandleFunc("/v1/models", handleModels)
	// OpenAI-compatible upstreams are called at /chat/completions (provider
	// base URL is typically the API root, e.g. https://api.openai.com/v1).
	// We register both /v1/chat/completions and /chat/completions so the mock
	// works whether the configured base URL includes /v1 or not.
	mux.HandleFunc("/v1/chat/completions", handleChatCompletions)
	mux.HandleFunc("/chat/completions", handleChatCompletions)

	log.Printf("mock-upstream listening on http://%s (delay=%s)", *addr, delay)
	srv := &http.Server{
		Addr:         *addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("mock-upstream server error: %v", err)
	}
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func handleModels(w http.ResponseWriter, r *http.Request) {
	if delay > 0 {
		time.Sleep(delay)
	}
	resp := modelsListResponse{
		Object: "list",
		Data: []modelObject{
			{ID: mockModelID, Object: "model", Created: createdFixed, OwnedBy: "mock-upstream"},
			{ID: "gpt-4o-mini", Object: "model", Created: createdFixed, OwnedBy: "mock-upstream"},
		},
	}
	writeJSON(w, http.StatusOK, resp)
}

func handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if delay > 0 {
		time.Sleep(delay)
	}

	// Best-effort parse: we only need the model name to echo back. If the body
	// is missing or malformed we still respond 200 so the benchmark measures
	// relay overhead, not upstream error handling.
	var req chatRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Model == "" {
		req.Model = mockModelID
	}

	// Streaming requests are not exercised by the baseline script, but if one
	// arrives we respond with a minimal SSE payload so the relay doesn't hang.
	if req.Stream {
		writeSSE(w, req.Model)
		return
	}

	resp := chatCompletionResponse{
		ID:      mockChatCompletionID,
		Object:  "chat.completion",
		Created: createdFixed,
		Model:   req.Model,
		Choices: []choice{
			{
				Index:        0,
				Message:      message{Role: "assistant", Content: mockAssistantContent},
				FinishReason: "stop",
			},
		},
		Usage: usage{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      totalTokens,
		},
	}
	writeJSON(w, http.StatusOK, resp)
}

// --- helpers ----------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeSSE(w http.ResponseWriter, model string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		// Cannot stream; fall back to a single non-stream chunk.
		writeJSON(w, http.StatusOK, chatCompletionResponse{
			ID: mockChatCompletionID, Object: "chat.completion", Created: createdFixed, Model: model,
		})
		return
	}
	// Two deterministic chunks + [DONE].
	fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]any{
		"id": mockChatCompletionID, "object": "chat.completion.chunk", "model": model,
		"choices": []map[string]any{{"index": 0, "delta": map[string]string{"role": "assistant"}, "finish_reason": nil}},
	}))
	flusher.Flush()
	fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]any{
		"id": mockChatCompletionID, "object": "chat.completion.chunk", "model": model,
		"choices": []map[string]any{{"index": 0, "delta": map[string]string{"content": mockAssistantContent}, "finish_reason": "stop"}},
	}))
	flusher.Flush()
	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
