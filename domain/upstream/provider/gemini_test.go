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

func TestGeminiProvider_ChatCompletions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "generateContent") {
			t.Fatalf("expected generateContent path, got %s", r.URL.Path)
		}
		if r.Header.Get("x-goog-api-key") != "test-api-key" {
			t.Fatalf("expected x-goog-api-key=test-api-key, got %s", r.Header.Get("x-goog-api-key"))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(geminiResponse{
			Candidates: []geminiCandidate{
				{
					Content: geminiContent{
						Role:  "model",
						Parts: []geminiPart{{Text: "Hello from Gemini!"}},
					},
					FinishReason: "STOP",
				},
			},
			UsageMetadata: geminiUsageMetadata{
				PromptTokenCount:     5,
				CandidatesTokenCount: 4,
				TotalTokenCount:      9,
			},
		})
	}))
	defer server.Close()

	p, err := NewGeminiProvider(server.URL, "test-api-key", 30*time.Second)
	if err != nil {
		t.Fatalf("NewGeminiProvider: %v", err)
	}
	resp, err := p.ChatCompletions(context.Background(), &ChatCompletionsRequest{
		Model:    "gemini-pro",
		Messages: []Message{{Role: "user", Content: "Hello"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletions() error = %v", err)
	}
	if resp.Choices[0].Message.Content != "Hello from Gemini!" {
		t.Fatalf("unexpected content: %s", resp.Choices[0].Message.Content)
	}
	if resp.Usage.TotalTokens != 9 {
		t.Fatalf("unexpected total tokens: %d", resp.Usage.TotalTokens)
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Fatalf("expected finish_reason=stop, got %s", resp.Choices[0].FinishReason)
	}
}

func TestGeminiProvider_ChatCompletions_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": {"message": "invalid model"}}`))
	}))
	defer server.Close()

	p, err := NewGeminiProvider(server.URL, "test-api-key", 30*time.Second)
	if err != nil {
		t.Fatalf("NewGeminiProvider: %v", err)
	}
	_, err = p.ChatCompletions(context.Background(), &ChatCompletionsRequest{
		Model:    "invalid-model",
		Messages: []Message{{Role: "user", Content: "Hello"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGeminiProvider_ChatCompletionsStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "streamGenerateContent") {
			t.Fatalf("expected streamGenerateContent path, got %s", r.URL.Path)
		}
		if r.URL.Query().Get("alt") != "sse" {
			t.Fatalf("expected alt=sse, got %s", r.URL.Query().Get("alt"))
		}

		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{
			`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Hello"}]},"finishReason":""}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":1,"totalTokenCount":6}}`,
			`data: {"candidates":[{"content":{"role":"model","parts":[{"text":" Gemini"}]},"finishReason":""}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":2,"totalTokenCount":7}}`,
			`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"!"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":3,"totalTokenCount":8}}`,
		}
		for _, chunk := range chunks {
			w.Write([]byte(chunk + "\n\n"))
			w.(http.Flusher).Flush()
		}
	}))
	defer server.Close()

	p, err := NewGeminiProvider(server.URL, "test-api-key", 30*time.Second)
	if err != nil {
		t.Fatalf("NewGeminiProvider: %v", err)
	}
	chunkChan, err := p.ChatCompletionsStream(context.Background(), &ChatCompletionsRequest{
		Model:    "gemini-pro",
		Messages: []Message{{Role: "user", Content: "Hello"}},
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("ChatCompletionsStream() error = %v", err)
	}

	var fullContent strings.Builder
	chunkCount := 0
	for chunk := range chunkChan {
		chunkCount++
		if len(chunk.Choices) > 0 {
			fullContent.WriteString(chunk.Choices[0].Delta.Content)
		}
	}

	if chunkCount != 3 {
		t.Fatalf("expected 3 chunks, got %d", chunkCount)
	}
	if fullContent.String() != "Hello Gemini!" {
		t.Fatalf("expected 'Hello Gemini!', got '%s'", fullContent.String())
	}
}

func TestGeminiProvider_ChatCompletionsStream_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error": {"message": "API key invalid"}}`))
	}))
	defer server.Close()

	p, err := NewGeminiProvider(server.URL, "test-api-key", 30*time.Second)
	if err != nil {
		t.Fatalf("NewGeminiProvider: %v", err)
	}
	_, err = p.ChatCompletionsStream(context.Background(), &ChatCompletionsRequest{
		Model:    "gemini-pro",
		Messages: []Message{{Role: "user", Content: "Hello"}},
		Stream:   true,
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestNewGeminiProvider_DefaultTimeout(t *testing.T) {
	p, err := NewGeminiProvider("https://generativelanguage.googleapis.com", "key", 0)
	if err != nil {
		t.Fatalf("NewGeminiProvider: %v", err)
	}
	if p.timeout != 30*time.Second {
		t.Fatalf("expected 30s default timeout, got %v", p.timeout)
	}
}

func TestNewGeminiProvider_DefaultBaseURL(t *testing.T) {
	p, err := NewGeminiProvider("", "key", 30*time.Second)
	if err != nil {
		t.Fatalf("NewGeminiProvider: %v", err)
	}
	if p.baseURL != "https://generativelanguage.googleapis.com" {
		t.Fatalf("expected default base URL, got %s", p.baseURL)
	}
}

func TestProviderFactory_CreateProvider_Gemini(t *testing.T) {
	factory := NewProviderFactory(30 * time.Second)
	p, err := factory.CreateProvider(ChannelTypeGemini, "https://generativelanguage.googleapis.com", "key")
	if err != nil {
		t.Fatalf("CreateProvider(Gemini) error = %v", err)
	}
	if _, ok := p.(*GeminiProvider); !ok {
		t.Fatalf("expected *GeminiProvider, got %T", p)
	}
}
