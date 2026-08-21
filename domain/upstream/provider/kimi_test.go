package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"micro-one-api/pkg/jsonx"
)

func TestOpenAIProvider_KimiK3NormalizesTypedChatRequest(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := jsonx.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-k3","object":"chat.completion","model":"kimi-k3","choices":[],"usage":{}}`))
	}))
	defer server.Close()

	p, err := NewOpenAIProvider(server.URL, "test-key", time.Second)
	if err != nil {
		t.Fatalf("NewOpenAIProvider: %v", err)
	}
	temperature := 0.7
	maxTokens := 2048
	_, err = p.ChatCompletions(context.Background(), &ChatCompletionsRequest{
		Model:           "Kimi-K3",
		Messages:        []Message{{Role: "user", Content: "hello"}},
		Temperature:     &temperature,
		MaxTokens:       &maxTokens,
		ReasoningEffort: "low",
	})
	if err != nil {
		t.Fatalf("ChatCompletions: %v", err)
	}

	if _, ok := got["temperature"]; ok {
		t.Fatalf("temperature must be omitted for Kimi K3: %#v", got)
	}
	if _, ok := got["max_tokens"]; ok {
		t.Fatalf("max_tokens must be translated for Kimi K3: %#v", got)
	}
	if got["max_completion_tokens"] != float64(maxTokens) {
		t.Fatalf("max_completion_tokens = %#v, want %d", got["max_completion_tokens"], maxTokens)
	}
	if got["reasoning_effort"] != "low" {
		t.Fatalf("reasoning_effort = %#v, want low", got["reasoning_effort"])
	}
}

func TestNormalizeKimiK3ChatBodyRemovesFixedSamplingParameters(t *testing.T) {
	body, err := normalizeKimiK3ChatBody([]byte(`{
		"model":"kimi-k3-preview",
		"messages":[{"role":"user","content":"hello"}],
		"temperature":0.7,
		"top_p":0.8,
		"n":2,
		"presence_penalty":0.1,
		"frequency_penalty":0.2,
		"max_tokens":123
	}`))
	if err != nil {
		t.Fatalf("normalizeKimiK3ChatBody: %v", err)
	}

	var got map[string]any
	if err := jsonx.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode normalized body: %v", err)
	}
	for _, key := range []string{"temperature", "top_p", "n", "presence_penalty", "frequency_penalty", "max_tokens"} {
		if _, ok := got[key]; ok {
			t.Fatalf("%s must be omitted for Kimi K3: %#v", key, got)
		}
	}
	if got["max_completion_tokens"] != float64(123) {
		t.Fatalf("max_completion_tokens = %#v, want 123", got["max_completion_tokens"])
	}
}

func TestOpenAIProvider_ForwardNormalizesKimiK3ChatBody(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %q, want /v1/chat/completions", r.URL.Path)
		}
		if err := jsonx.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-k3","choices":[],"usage":{}}`))
	}))
	defer server.Close()

	p, err := NewOpenAIProvider(server.URL, "test-key", time.Second)
	if err != nil {
		t.Fatalf("NewOpenAIProvider: %v", err)
	}
	_, err = p.Forward(context.Background(), &RawRequest{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Body:   []byte(`{"model":"kimi-k3","temperature":0.7,"top_p":0.8,"max_tokens":64}`),
	})
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if _, ok := got["temperature"]; ok {
		t.Fatalf("temperature must be omitted: %#v", got)
	}
	if _, ok := got["top_p"]; ok {
		t.Fatalf("top_p must be omitted: %#v", got)
	}
	if got["max_completion_tokens"] != float64(64) {
		t.Fatalf("max_completion_tokens = %#v, want 64", got["max_completion_tokens"])
	}
}

func TestNormalizeKimiK3ChatBodyLeavesOtherModelsUnchanged(t *testing.T) {
	body := []byte(`{"model":"kimi-k2.7","temperature":0.7,"max_tokens":123}`)
	got, err := normalizeKimiK3ChatBody(body)
	if err != nil {
		t.Fatalf("normalizeKimiK3ChatBody: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("non-K3 body changed: got %s, want %s", got, body)
	}
}
