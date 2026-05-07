package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOpenAIProvider_ChatCompletionsStream(t *testing.T) {
	chunkCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST request, got %s", r.Method)
		}

		if r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader != "Bearer test-api-key" {
			t.Fatalf("expected Authorization Bearer test-api-key, got %s", authHeader)
		}

		var req ChatCompletionsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}

		if !req.Stream {
			t.Fatalf("expected stream=true, got %v", req.Stream)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")

		chunks := []string{
			`data: {"id":"chunk1","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`,
			`data: {"id":"chunk2","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}`,
			`data: {"id":"chunk3","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`data: [DONE]`,
		}

		for _, chunk := range chunks {
			w.Write([]byte(chunk + "\n\n"))
			w.(http.Flusher).Flush()
		}
	}))
	defer server.Close()

	provider, err := NewOpenAIProvider(server.URL, "test-api-key", 30*time.Second)
	if err != nil {
		t.Fatalf("NewOpenAIProvider() error = %v", err)
	}

	req := &ChatCompletionsRequest{
		Model: "gpt-4o-mini",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
		Stream: true,
	}

	chunkChan, err := provider.ChatCompletionsStream(context.Background(), req)
	if err != nil {
		t.Fatalf("ChatCompletionsStream() error = %v", err)
	}

	var fullContent strings.Builder
	for chunk := range chunkChan {
		chunkCount++
		if len(chunk.Choices) > 0 {
			fullContent.WriteString(chunk.Choices[0].Delta.Content)
		}
	}

	if chunkCount != 3 {
		t.Fatalf("expected 3 chunks, got %d", chunkCount)
	}

	if fullContent.String() != "Hello world" {
		t.Fatalf("expected content 'Hello world', got '%s'", fullContent.String())
	}
}

func TestOpenAIProvider_ChatCompletionsStream_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error": {"message": "Invalid API key"}}`))
	}))
	defer server.Close()

	provider, err := NewOpenAIProvider(server.URL, "test-api-key", 30*time.Second)
	if err != nil {
		t.Fatalf("NewOpenAIProvider() error = %v", err)
	}

	req := &ChatCompletionsRequest{
		Model:    "gpt-4o-mini",
		Messages: []Message{{Role: "user", Content: "Hello"}},
		Stream:   true,
	}

	_, err = provider.ChatCompletionsStream(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for unauthorized request")
	}
}

func TestOpenAIProvider_ChatCompletions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST request, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-api-key" {
			t.Fatalf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}

		var req ChatCompletionsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		if req.Stream {
			t.Fatalf("expected stream=false, got true")
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatCompletionsResponse{
			ID:      "chatcmpl-test",
			Object:  "chat.completion",
			Created: 1234567890,
			Model:   "gpt-4o-mini",
			Choices: []Choice{{
				Index: 0,
				Message: Message{
					Role:    "assistant",
					Content: "Hello, world!",
				},
				FinishReason: "stop",
			}},
			Usage: Usage{
				PromptTokens:     10,
				CompletionTokens: 3,
				TotalTokens:      13,
			},
		})
	}))
	defer server.Close()

	provider, err := NewOpenAIProvider(server.URL, "test-api-key", 30*time.Second)
	if err != nil {
		t.Fatalf("NewOpenAIProvider() error = %v", err)
	}
	resp, err := provider.ChatCompletions(context.Background(), &ChatCompletionsRequest{
		Model: "gpt-4o-mini",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
		Stream: false,
	})
	if err != nil {
		t.Fatalf("ChatCompletions() error = %v", err)
	}
	if resp.ID != "chatcmpl-test" {
		t.Fatalf("unexpected ID: %s", resp.ID)
	}
	if resp.Choices[0].Message.Content != "Hello, world!" {
		t.Fatalf("unexpected content: %s", resp.Choices[0].Message.Content)
	}
	if resp.Usage.TotalTokens != 13 {
		t.Fatalf("unexpected total tokens: %d", resp.Usage.TotalTokens)
	}
}

func TestOpenAIProvider_ChatCompletions_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": {"message": "invalid request"}}`))
	}))
	defer server.Close()

	provider, err := NewOpenAIProvider(server.URL, "test-api-key", 30*time.Second)
	if err != nil {
		t.Fatalf("NewOpenAIProvider() error = %v", err)
	}
	_, err = provider.ChatCompletions(context.Background(), &ChatCompletionsRequest{
		Model:    "gpt-4o-mini",
		Messages: []Message{{Role: "user", Content: "Hello"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestProviderFactory_CreateProvider(t *testing.T) {
	factory := NewProviderFactory(30 * time.Second)

	p, err := factory.CreateProvider(ChannelTypeOpenAI, "https://api.openai.com/v1", "sk-test")
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil provider")
	}

	p2, err := factory.CreateProvider(999, "https://custom.api/v1", "sk-test")
	if err != nil {
		t.Fatalf("CreateProvider(unknown) error = %v", err)
	}
	if p2 == nil {
		t.Fatal("expected non-nil provider for unknown type")
	}
}

func TestProviderFactory_DefaultTimeout(t *testing.T) {
	factory := NewProviderFactory(0)
	if factory.defaultTimeout != 30*time.Second {
		t.Fatalf("expected 30s default timeout, got %v", factory.defaultTimeout)
	}
}

func TestNewOpenAIProvider_DefaultTimeout(t *testing.T) {
	provider, err := NewOpenAIProvider("https://api.openai.com", "sk-test", 0)
	if err != nil {
		t.Fatalf("NewOpenAIProvider() error = %v", err)
	}
	if provider.timeout != 30*time.Second {
		t.Fatalf("expected 30s default timeout, got %v", provider.timeout)
	}
}