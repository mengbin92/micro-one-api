package server

import (
	"net/http"
	"net/http/httptest"
	"fmt"
	"strings"
	"testing"
	"time"

	relaybiz "micro-one-api/internal/relay/biz"
	relaydata "micro-one-api/internal/relay/data"
	relayprovider "micro-one-api/internal/relay/provider"

	khttp "github.com/go-kratos/kratos/v2/transport/http"
)

// --- pure conversion tests (no server needed) ---

func TestConvertAnthropicToChatCompletions_StringContent(t *testing.T) {
	req := &anthropicInboundRequest{
		Model:     "claude-3-5-sonnet-20241022",
		MaxTokens: 100,
		Messages: []anthropicInboundMessage{
			{Role: "user", Content: jsonRaw(`"hello"`)},
		},
	}
	cc, err := convertAnthropicToChatCompletions(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cc.Model != "claude-3-5-sonnet-20241022" {
		t.Fatalf("model = %q", cc.Model)
	}
	if cc.MaxTokens == nil || *cc.MaxTokens != 100 {
		t.Fatalf("max_tokens = %v", cc.MaxTokens)
	}
	if len(cc.Messages) != 1 || cc.Messages[0].Content != "hello" {
		t.Fatalf("messages = %+v", cc.Messages)
	}
}

func TestConvertAnthropicToChatCompletions_SystemString(t *testing.T) {
	req := &anthropicInboundRequest{
		Model:     "claude",
		MaxTokens: 10,
		System:    jsonRaw(`"you are helpful"`),
		Messages: []anthropicInboundMessage{
			{Role: "user", Content: jsonRaw(`"hi"`)},
		},
	}
	cc, err := convertAnthropicToChatCompletions(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cc.Messages) != 2 {
		t.Fatalf("messages len = %d", len(cc.Messages))
	}
	if cc.Messages[0].Role != "system" || cc.Messages[0].Content != "you are helpful" {
		t.Fatalf("system msg = %+v", cc.Messages[0])
	}
}

func TestConvertAnthropicToChatCompletions_ContentBlocks(t *testing.T) {
	req := &anthropicInboundRequest{
		Model:     "claude",
		MaxTokens: 10,
		Messages: []anthropicInboundMessage{
			{Role: "user", Content: jsonRaw(`[{"type":"text","text":"part1"},{"type":"text","text":"part2"}]`)},
		},
	}
	cc, err := convertAnthropicToChatCompletions(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cc.Messages) != 1 || cc.Messages[0].Content != "part1part2" {
		t.Fatalf("messages = %+v", cc.Messages)
	}
}

func TestConvertAnthropicToChatCompletions_ToolUse(t *testing.T) {
	req := &anthropicInboundRequest{
		Model:     "claude",
		MaxTokens: 10,
		Messages: []anthropicInboundMessage{
			{
				Role:    "assistant",
				Content: jsonRaw(`[{"type":"tool_use","id":"call_1","name":"get_weather","input":{"city":"SF"}}]`),
			},
		},
	}
	cc, err := convertAnthropicToChatCompletions(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cc.Messages) != 1 {
		t.Fatalf("messages len = %d", len(cc.Messages))
	}
	m := cc.Messages[0]
	if len(m.ToolCalls) != 1 {
		t.Fatalf("tool_calls len = %d", len(m.ToolCalls))
	}
	tc := m.ToolCalls[0]
	if tc.ID != "call_1" || tc.Function.Name != "get_weather" {
		t.Fatalf("tool_call = %+v", tc)
	}
	if !strings.Contains(tc.Function.Arguments, "SF") {
		t.Fatalf("arguments = %q", tc.Function.Arguments)
	}
}

func TestConvertAnthropicToChatCompletions_ToolResult(t *testing.T) {
	req := &anthropicInboundRequest{
		Model:     "claude",
		MaxTokens: 10,
		Messages: []anthropicInboundMessage{
			{
				Role:    "user",
				Content: jsonRaw(`[{"type":"tool_result","tool_use_id":"call_1","content":"sunny"}]`),
			},
		},
	}
	cc, err := convertAnthropicToChatCompletions(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cc.Messages) != 1 {
		t.Fatalf("messages len = %d", len(cc.Messages))
	}
	m := cc.Messages[0]
	if m.Role != "tool" || m.ToolCallID != "call_1" || m.Content != "sunny" {
		t.Fatalf("tool result msg = %+v", m)
	}
}

func TestConvertChatCompletionsToAnthropic(t *testing.T) {
	resp := &relayprovider.ChatCompletionsResponse{
		ID:    "chatcmpl-1",
		Model: "claude",
		Choices: []relayprovider.Choice{
			{
				Index: 0,
				Message: relayprovider.Message{
					Role:    "assistant",
					Content: "hello there",
				},
				FinishReason: "stop",
			},
		},
		Usage: relayprovider.Usage{
			PromptTokens:     5,
			CompletionTokens: 3,
			TotalTokens:      8,
		},
	}
	got := convertChatCompletionsToAnthropic(resp, "claude-client")
	if got.ID != "chatcmpl-1" || got.Type != "message" || got.Role != "assistant" {
		t.Fatalf("response header = %+v", got)
	}
	if len(got.Content) != 1 || got.Content[0].Type != "text" || got.Content[0].Text != "hello there" {
		t.Fatalf("content = %+v", got.Content)
	}
	if got.Usage.InputTokens != 5 || got.Usage.OutputTokens != 3 {
		t.Fatalf("usage = %+v", got.Usage)
	}
	if got.StopReason == nil || *got.StopReason != "end_turn" {
		t.Fatalf("stop_reason = %v", got.StopReason)
	}
}

// --- handler integration tests ---

func TestAnthropicMessagesRouteRegistered(t *testing.T) {
	httpServer := NewHTTPServer(nil, nil, nil, nil, nil)
	srv := khttp.NewServer()
	httpServer.RegisterRoutes(srv)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Fatalf("/v1/messages is not registered (got 404)")
	}
}

func TestAnthropicMessagesAuthFromXAPIKey(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-test",
			"object":"chat.completion",
			"created":1710000000,
			"model":"claude-3-5-sonnet",
			"choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":7,"completion_tokens":5,"total_tokens":12}
		}`))
	}))
	defer upstream.Close()

	identityClient := rawIdentityClient{}
	channelClient := rawChannelClient{baseURL: upstream.URL + "/v1", key: "sk-upstream"}
	billingClient := &rawBillingClient{}
	logClient := &rawLogClient{}
	relayUsecase := relaybiz.NewRelayUsecase(
		relaydata.NewIdentityAdapter(identityClient),
		relaydata.NewChannelAdapter(channelClient),
		nil,
		&relaybiz.RetryPolicy{MaxAttempts: 1},
	)
	httpServer := NewHTTPServer(
		identityClient,
		channelClient,
		billingClient,
		relayprovider.NewProviderFactory(time.Second),
		relayUsecase,
		logClient,
	)
	srv := khttp.NewServer()
	httpServer.RegisterRoutes(srv)

	body := `{"model":"claude-3-5-sonnet","max_tokens":16,"messages":[{"role":"user","content":"ping"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("x-api-key", "user-token")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	// Verify Anthropic response format.
	respBody := rec.Body.String()
	if !strings.Contains(respBody, `"type":"message"`) {
		t.Fatalf("missing type=message: %s", respBody)
	}
	if !strings.Contains(respBody, `"input_tokens":7`) {
		t.Fatalf("missing input_tokens: %s", respBody)
	}
	if !strings.Contains(respBody, `"output_tokens":5`) {
		t.Fatalf("missing output_tokens: %s", respBody)
	}
	if !strings.Contains(respBody, `"pong"`) {
		t.Fatalf("missing content pong: %s", respBody)
	}
	if !strings.Contains(respBody, `"stop_reason":"end_turn"`) {
		t.Fatalf("missing stop_reason: %s", respBody)
	}

	// Verify usage log was written.
	if len(logClient.entries) != 1 {
		t.Fatalf("usage logs = %d, want 1", len(logClient.entries))
	}
	got := logClient.entries[0]
	if got.ModelName != "claude-3-5-sonnet" {
		t.Fatalf("log model_name = %q", got.ModelName)
	}
	if !strings.Contains(got.Message, "quota=12") {
		t.Fatalf("log message = %q", got.Message)
	}
	if got.Quota != 12 {
		t.Fatalf("log quota = %d, want 12", got.Quota)
	}
}

func TestAnthropicMessagesStreaming(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, _ := w.(http.Flusher)
		// chunk 1: content
		fmt.Fprintf(w, "data: %s\n\n", `{"id":"1","object":"chat.completion.chunk","model":"claude","choices":[{"index":0,"delta":{"content":"Hel"},"finish_reason":null}]}`)
		flusher.Flush()
		// chunk 2: content
		fmt.Fprintf(w, "data: %s\n\n", `{"id":"1","object":"chat.completion.chunk","model":"claude","choices":[{"index":0,"delta":{"content":"lo"},"finish_reason":null}]}`)
		flusher.Flush()
		// chunk 3: finish + usage
		fmt.Fprintf(w, "data: %s\n\n", `{"id":"1","object":"chat.completion.chunk","model":"claude","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`)
		flusher.Flush()
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	identityClient := rawIdentityClient{}
	channelClient := rawChannelClient{baseURL: upstream.URL + "/v1", key: "sk-upstream"}
	billingClient := &rawBillingClient{}
	logClient := &rawLogClient{}
	relayUsecase := relaybiz.NewRelayUsecase(
		relaydata.NewIdentityAdapter(identityClient),
		relaydata.NewChannelAdapter(channelClient),
		nil,
		&relaybiz.RetryPolicy{MaxAttempts: 1},
	)
	httpServer := NewHTTPServer(
		identityClient,
		channelClient,
		billingClient,
		relayprovider.NewProviderFactory(time.Second),
		relayUsecase,
		logClient,
	)
	srv := khttp.NewServer()
	httpServer.RegisterRoutes(srv)

	body := `{"model":"claude-3-5-sonnet","max_tokens":16,"stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("x-api-key", "user-token")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	respBody := rec.Body.String()
	for _, expected := range []string{
		"event: message_start",
		"event: content_block_start",
		"event: content_block_delta",
		"event: content_block_stop",
		"event: message_delta",
		"event: message_stop",
		`"Hel"`,
		`"lo"`,
		`"stop_reason":"end_turn"`,
		`"output_tokens":2`,
	} {
		if !strings.Contains(respBody, expected) {
			t.Fatalf("stream missing %q\nfull output:\n%s", expected, respBody)
		}
	}
}

func TestAnthropicMessagesRejectsMissingAPIKey(t *testing.T) {
	httpServer := NewHTTPServer(nil, nil, nil, nil, nil)
	srv := khttp.NewServer()
	httpServer.RegisterRoutes(srv)

	body := `{"model":"claude","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"type":"error"`) {
		t.Fatalf("error body = %s", rec.Body.String())
	}
}

func TestAnthropicMessagesAcceptsBearerAuth(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-test",
			"object":"chat.completion",
			"created":1710000000,
			"model":"claude",
			"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`))
	}))
	defer upstream.Close()

	identityClient := rawIdentityClient{}
	channelClient := rawChannelClient{baseURL: upstream.URL + "/v1", key: "sk-upstream"}
	billingClient := &rawBillingClient{}
	logClient := &rawLogClient{}
	relayUsecase := relaybiz.NewRelayUsecase(
		relaydata.NewIdentityAdapter(identityClient),
		relaydata.NewChannelAdapter(channelClient),
		nil,
		&relaybiz.RetryPolicy{MaxAttempts: 1},
	)
	httpServer := NewHTTPServer(
		identityClient,
		channelClient,
		billingClient,
		relayprovider.NewProviderFactory(time.Second),
		relayUsecase,
		logClient,
	)
	srv := khttp.NewServer()
	httpServer.RegisterRoutes(srv)

	body := `{"model":"claude","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	// Use Authorization: Bearer instead of x-api-key
	req.Header.Set("Authorization", "Bearer user-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
}

// jsonRaw is a tiny helper to create json.RawMessage in tests without importing
// encoding/json into every test.
func jsonRaw(s string) []byte {
	return []byte(s)
}
