package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// Allow connections to localhost for testing (mock upstream servers)
	os.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	os.Exit(m.Run())
}

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

func TestOpenAIProvider_ChatCompletionsStreamParsesUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{
			`data: {"id":"chunk1","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`,
			`data: {"id":"chunk2","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":3,"total_tokens":14}}`,
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

	chunkChan, err := provider.ChatCompletionsStream(context.Background(), &ChatCompletionsRequest{
		Model:    "gpt-4o-mini",
		Messages: []Message{{Role: "user", Content: "Hello"}},
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("ChatCompletionsStream() error = %v", err)
	}

	var usage Usage
	for chunk := range chunkChan {
		if chunk.Usage.TotalTokens > 0 {
			usage = chunk.Usage
		}
	}

	if usage.PromptTokens != 11 || usage.CompletionTokens != 3 || usage.TotalTokens != 14 {
		t.Fatalf("usage = %+v, want prompt=11 completion=3 total=14", usage)
	}
}

func TestOpenAIProvider_ChatCompletionsStreamPassesToolCallDeltas(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{
			`data: {"id":"chunk1","object":"chat.completion.chunk","created":1234567890,"model":"mimo-v2.5","choices":[{"index":0,"delta":{"reasoning_content":"Need weather.","tool_calls":[{"index":0,"id":"call_weather","type":"function","function":{"name":"get_current_weather","arguments":"{\"location\":"}}]},"finish_reason":null}]}`,
			`data: {"id":"chunk2","object":"chat.completion.chunk","created":1234567890,"model":"mimo-v2.5","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Beijing\"}"}}]},"finish_reason":null}]}`,
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

	chunkChan, err := provider.ChatCompletionsStream(context.Background(), &ChatCompletionsRequest{
		Model:    "mimo-v2.5",
		Messages: []Message{{Role: "user", Content: "北京天气怎么样"}},
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("ChatCompletionsStream() error = %v", err)
	}

	var chunks []StreamChunk
	for chunk := range chunkChan {
		chunks = append(chunks, chunk)
	}
	if len(chunks) != 2 {
		t.Fatalf("chunk count = %d, want 2", len(chunks))
	}
	firstDelta := chunks[0].Choices[0].Delta
	if firstDelta.ReasoningContent != "Need weather." {
		t.Fatalf("reasoning_content mismatch: %#v", firstDelta.ReasoningContent)
	}
	if len(firstDelta.ToolCalls) != 1 {
		t.Fatalf("tool_calls were not parsed: %#v", firstDelta.ToolCalls)
	}
	if firstDelta.ToolCalls[0].Index == nil || *firstDelta.ToolCalls[0].Index != 0 {
		t.Fatalf("tool_call index mismatch: %#v", firstDelta.ToolCalls[0].Index)
	}
	if firstDelta.ToolCalls[0].ID != "call_weather" || firstDelta.ToolCalls[0].Function.Name != "get_current_weather" {
		t.Fatalf("tool_call mismatch: %#v", firstDelta.ToolCalls[0])
	}
	if chunks[1].Choices[0].Delta.ToolCalls[0].Function.Arguments != `"Beijing"}` {
		t.Fatalf("tool_call arguments delta mismatch: %#v", chunks[1].Choices[0].Delta.ToolCalls[0].Function.Arguments)
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

func TestOpenAIProvider_ChatCompletionsPassesBackReasoningContentWithToolCalls(t *testing.T) {
	var gotBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatCompletionsResponse{
			ID:      "chatcmpl-test",
			Object:  "chat.completion",
			Created: 1234567890,
			Model:   "mimo-v2.5",
			Choices: []Choice{{
				Index:        0,
				Message:      Message{Role: "assistant", Content: "ok"},
				FinishReason: "stop",
			}},
			Usage: Usage{TotalTokens: 13},
		})
	}))
	defer server.Close()

	provider, err := NewOpenAIProvider(server.URL, "test-api-key", 30*time.Second)
	if err != nil {
		t.Fatalf("NewOpenAIProvider() error = %v", err)
	}

	_, err = provider.ChatCompletions(context.Background(), &ChatCompletionsRequest{
		Model: "mimo-v2.5",
		Tools: []map[string]interface{}{
			{
				"type": "function",
				"function": map[string]interface{}{
					"name":        "get_current_weather",
					"description": "Get current weather.",
					"parameters": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"location": map[string]interface{}{"type": "string"},
						},
					},
				},
			},
		},
		ToolChoice: "auto",
		Messages: []Message{
			{Role: "user", Content: "北京天气怎么样"},
			{
				Role:             "assistant",
				Content:          "",
				ReasoningContent: "Need to call the weather tool for Beijing.",
				ToolCalls: []ToolCall{{
					ID:   "call_weather_beijing",
					Type: "function",
					Function: ToolCallFunction{
						Name:      "get_current_weather",
						Arguments: `{"location":"Beijing"}`,
					},
				}},
			},
			{Role: "tool", Content: "Sunny 25C", ToolCallID: "call_weather_beijing"},
			{Role: "user", Content: "那上海呢？"},
		},
	})
	if err != nil {
		t.Fatalf("ChatCompletions() error = %v", err)
	}

	messages, ok := gotBody["messages"].([]interface{})
	if !ok || len(messages) != 4 {
		t.Fatalf("messages mismatch: %#v", gotBody["messages"])
	}
	tools, ok := gotBody["tools"].([]interface{})
	if !ok || len(tools) != 1 {
		t.Fatalf("tools were not passed through: %#v", gotBody["tools"])
	}
	if gotBody["tool_choice"] != "auto" {
		t.Fatalf("tool_choice was not passed through: %#v", gotBody["tool_choice"])
	}
	assistantMessage, ok := messages[1].(map[string]interface{})
	if !ok {
		t.Fatalf("assistant message mismatch: %#v", messages[1])
	}
	if assistantMessage["reasoning_content"] != "Need to call the weather tool for Beijing." {
		t.Fatalf("reasoning_content was not passed back: %#v", assistantMessage)
	}
	toolCalls, ok := assistantMessage["tool_calls"].([]interface{})
	if !ok || len(toolCalls) != 1 {
		t.Fatalf("tool_calls were not passed back: %#v", assistantMessage["tool_calls"])
	}
	toolCall := toolCalls[0].(map[string]interface{})
	if toolCall["id"] != "call_weather_beijing" || toolCall["type"] != "function" {
		t.Fatalf("tool_call metadata mismatch: %#v", toolCall)
	}
	function := toolCall["function"].(map[string]interface{})
	if function["name"] != "get_current_weather" || function["arguments"] != `{"location":"Beijing"}` {
		t.Fatalf("tool_call function mismatch: %#v", function)
	}
	toolMessage := messages[2].(map[string]interface{})
	if toolMessage["tool_call_id"] != "call_weather_beijing" {
		t.Fatalf("tool message tool_call_id mismatch: %#v", toolMessage)
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

func TestAzureProvider_ChatCompletionsUsesDeploymentPathAndAPIVersion(t *testing.T) {
	var gotPath string
	var gotQuery string
	var gotAuth string
	var gotBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("api-key")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatCompletionsResponse{
			ID:      "chatcmpl-azure",
			Object:  "chat.completion",
			Created: 1234567890,
			Model:   "gpt-4o-mini",
			Choices: []Choice{{
				Index:        0,
				Message:      Message{Role: "assistant", Content: "ok"},
				FinishReason: "stop",
			}},
			Usage: Usage{TotalTokens: 9},
		})
	}))
	defer server.Close()

	provider, err := NewAzureProvider(server.URL, "azure-key", "2024-02-15-preview", 30*time.Second)
	if err != nil {
		t.Fatalf("NewAzureProvider() error = %v", err)
	}
	_, err = provider.ChatCompletions(context.Background(), &ChatCompletionsRequest{
		Model:    "gpt-4o-mini",
		Messages: []Message{{Role: "user", Content: "Hello"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletions() error = %v", err)
	}

	if gotPath != "/openai/deployments/gpt-4o-mini/chat/completions" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotQuery != "api-version=2024-02-15-preview" {
		t.Fatalf("query = %q", gotQuery)
	}
	if gotAuth != "azure-key" {
		t.Fatalf("api-key = %q", gotAuth)
	}
	if _, ok := gotBody["model"]; ok {
		t.Fatalf("azure request should omit model from body, got %v", gotBody)
	}
}

func TestAzureProvider_ChatCompletionsKeepsConfiguredDeploymentPath(t *testing.T) {
	var gotPath string
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatCompletionsResponse{
			ID:      "chatcmpl-azure",
			Object:  "chat.completion",
			Created: 1234567890,
			Model:   "configured-deploy",
			Choices: []Choice{{
				Index:        0,
				Message:      Message{Role: "assistant", Content: "ok"},
				FinishReason: "stop",
			}},
			Usage: Usage{TotalTokens: 9},
		})
	}))
	defer server.Close()

	provider, err := NewAzureProvider(server.URL+"/openai/deployments/configured-deploy?api-version=2023-12-01-preview", "azure-key", "", 30*time.Second)
	if err != nil {
		t.Fatalf("NewAzureProvider() error = %v", err)
	}
	_, err = provider.ChatCompletions(context.Background(), &ChatCompletionsRequest{
		Model:    "client-model",
		Messages: []Message{{Role: "user", Content: "Hello"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletions() error = %v", err)
	}

	if gotPath != "/openai/deployments/configured-deploy/chat/completions" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotQuery != "api-version=2023-12-01-preview" {
		t.Fatalf("query = %q", gotQuery)
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
	if factory.defaultTimeout != time.Minute {
		t.Fatalf("expected 1m default timeout, got %v", factory.defaultTimeout)
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
