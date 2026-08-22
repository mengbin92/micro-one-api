package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	relaydata "micro-one-api/internal/data"
	"micro-one-api/pkg/jsonx"

	khttp "github.com/go-kratos/kratos/v3/transport/http"
)

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

func TestNewHTTPServerInitializesAPIKeyAdaptorClients(t *testing.T) {
	server := NewHTTPServer(nil, nil, nil, relayprovider.NewProviderFactory(2*time.Second), nil)
	if server.apiKeyHTTPClient == nil || server.apiKeyHTTPClient.Timeout != 2*time.Second {
		t.Fatalf("non-stream client = %#v", server.apiKeyHTTPClient)
	}
	if server.apiKeyStreamHTTPClient == nil || server.apiKeyStreamHTTPClient.Timeout != 0 || server.apiKeyStreamHTTPClient.Transport == nil {
		t.Fatalf("stream client = %#v", server.apiKeyStreamHTTPClient)
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

func TestAnthropicMessagesNativeChannelPassthrough(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %q, want /v1/messages", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "anthropic-upstream-key" {
			t.Errorf("x-api-key = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization leaked to upstream: %q", got)
		}
		if got := r.Header.Get("anthropic-beta"); got != "tools-2024-04-04" {
			t.Errorf("anthropic-beta = %q", got)
		}
		var request map[string]jsonx.RawMessage
		if err := jsonx.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		var model string
		_ = jsonx.Unmarshal(request["model"], &model)
		if model != "claude-native" {
			t.Errorf("model = %q", model)
		}
		if string(request["custom_extension"]) != `{"keep":true}` {
			t.Errorf("custom extension was not preserved: %s", request["custom_extension"])
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("request-id", "upstream-request-id")
		_, _ = w.Write([]byte(`{
			"id":"msg_native","type":"message","role":"assistant","model":"claude-native",
			"content":[{"type":"tool_use","id":"toolu_1","name":"Read","input":{"path":"README.md"}}],
			"stop_reason":"tool_use","usage":{"input_tokens":9,"output_tokens":3,"cache_creation_input_tokens":2,"cache_read_input_tokens":4}
		}`))
	}))
	defer upstream.Close()

	identityClient := rawIdentityClient{}
	channelClient := rawChannelClient{
		baseURL: upstream.URL + "/v1", key: "anthropic-upstream-key",
		chType: relayprovider.ChannelTypeAnthropic,
	}
	billingClient := &rawBillingClient{}
	logClient := &rawLogClient{}
	relayUsecase := relaybiz.NewRelayUsecase(
		relaydata.NewIdentityAdapter(identityClient), relaydata.NewChannelAdapter(channelClient), nil,
		&relaybiz.RetryPolicy{MaxAttempts: 1},
	)
	httpServer := NewHTTPServer(identityClient, channelClient, billingClient, relayprovider.NewProviderFactory(time.Second), relayUsecase, logClient)
	srv := khttp.NewServer()
	httpServer.RegisterRoutes(srv)

	body := `{"model":"claude-native","max_tokens":16,"messages":[{"role":"user","content":"inspect"}],"custom_extension":{"keep":true}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("x-api-key", "user-token")
	req.Header.Set("Authorization", "Bearer user-token")
	req.Header.Set("anthropic-beta", "tools-2024-04-04")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"id":"msg_native"`) || !strings.Contains(rec.Body.String(), `"type":"tool_use"`) {
		t.Fatalf("native response was not preserved: %s", rec.Body.String())
	}
	if got := rec.Header().Get("request-id"); got != "upstream-request-id" {
		t.Fatalf("request-id = %q", got)
	}
	if billingClient.commits != 1 || billingClient.releases != 0 {
		t.Fatalf("billing commits=%d releases=%d", billingClient.commits, billingClient.releases)
	}
	if len(billingClient.commitRequests) != 1 || billingClient.commitRequests[0].ActualTokens != 18 {
		t.Fatalf("committed usage = %#v, want 18 total tokens", billingClient.commitRequests)
	}
}

func TestAnthropicMessagesNativeChannelStreamingPassthrough(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		events := []struct {
			name string
			data string
		}{
			{"message_start", `{"type":"message_start","message":{"id":"msg_stream_native","type":"message","role":"assistant","content":[],"model":"claude-native","stop_reason":null,"usage":{"input_tokens":9,"output_tokens":0,"cache_creation_input_tokens":2,"cache_read_input_tokens":4}}}`},
			{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_native","name":"Read","input":{}}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"README.md\"}"}}`},
			{"content_block_stop", `{"type":"content_block_stop","index":0}`},
			{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":3}}`},
			{"message_stop", `{"type":"message_stop"}`},
		}
		for _, event := range events {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.name, event.data)
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	identityClient := rawIdentityClient{}
	channelClient := rawChannelClient{baseURL: upstream.URL, key: "anthropic-upstream-key", chType: relayprovider.ChannelTypeAnthropic}
	billingClient := &rawBillingClient{}
	logClient := &rawLogClient{}
	relayUsecase := relaybiz.NewRelayUsecase(
		relaydata.NewIdentityAdapter(identityClient), relaydata.NewChannelAdapter(channelClient), nil,
		&relaybiz.RetryPolicy{MaxAttempts: 1},
	)
	httpServer := NewHTTPServer(identityClient, channelClient, billingClient, relayprovider.NewProviderFactory(time.Second), relayUsecase, logClient)
	srv := khttp.NewServer()
	httpServer.RegisterRoutes(srv)

	body := `{"model":"claude-native","max_tokens":16,"stream":true,"messages":[{"role":"user","content":"inspect"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("x-api-key", "user-token")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	for _, expected := range []string{`"id":"msg_stream_native"`, `"id":"toolu_native"`, `"partial_json":"{\"path\":\"README.md\"}"`, `"stop_reason":"tool_use"`} {
		if !strings.Contains(rec.Body.String(), expected) {
			t.Fatalf("native stream missing %q:\n%s", expected, rec.Body.String())
		}
	}
	if billingClient.commits != 1 || billingClient.releases != 0 {
		t.Fatalf("billing commits=%d releases=%d", billingClient.commits, billingClient.releases)
	}
	if got := billingClient.commitRequests[0].ActualTokens; got != 18 {
		t.Fatalf("committed tokens = %d, want 18", got)
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

func TestAnthropicMessagesStreamingForwardsDeepSeekToolCall(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := jsonx.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode upstream request: %v", err)
		}
		if _, ok := request["max_completion_tokens"]; ok {
			t.Errorf("unexpected max_completion_tokens: %#v", request)
		}
		if got := int(request["max_tokens"].(float64)); got != 16 {
			t.Errorf("max_tokens = %d, want 16", got)
		}
		tools, _ := request["tools"].([]any)
		if len(tools) != 1 {
			t.Errorf("tools = %#v, want one tool", request["tools"])
		}
		streamOptions, _ := request["stream_options"].(map[string]any)
		if include, _ := streamOptions["include_usage"].(bool); !include {
			t.Errorf("stream_options = %#v, want include_usage=true", streamOptions)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		chunks := []string{
			`{"id":"chatcmpl-tool","object":"chat.completion.chunk","model":"deepseek-v4-pro-0813","choices":[{"index":0,"delta":{"reasoning_content":"I should inspect the repository."},"finish_reason":null}]}`,
			`{"id":"chatcmpl-tool","object":"chat.completion.chunk","model":"deepseek-v4-pro-0813","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"Read","arguments":"{\"file_path\":\"/tmp/demo.go\","}}]},"finish_reason":null}]}`,
			`{"id":"chatcmpl-tool","object":"chat.completion.chunk","model":"deepseek-v4-pro-0813","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"limit\":200}"}}]},"finish_reason":null}]}`,
			`{"id":"chatcmpl-tool","object":"chat.completion.chunk","model":"deepseek-v4-pro-0813","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":30,"completion_tokens":8,"total_tokens":38}}`,
		}
		for _, chunk := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	identityClient := rawIdentityClient{}
	channelClient := rawChannelClient{baseURL: upstream.URL + "/v1", key: "sk-upstream", chType: relayprovider.ChannelTypeDeepSeek}
	billingClient := &rawBillingClient{}
	logClient := &rawLogClient{}
	relayUsecase := relaybiz.NewRelayUsecase(
		relaydata.NewIdentityAdapter(identityClient),
		relaydata.NewChannelAdapter(channelClient),
		nil,
		&relaybiz.RetryPolicy{MaxAttempts: 1},
	)
	httpServer := NewHTTPServer(identityClient, channelClient, billingClient, relayprovider.NewProviderFactory(time.Second), relayUsecase, logClient)
	srv := khttp.NewServer()
	httpServer.RegisterRoutes(srv)

	body := `{"model":"deepseek-v4-pro-0813","max_tokens":16,"stream":true,"messages":[{"role":"user","content":"inspect"}],"tools":[{"name":"Read","description":"read file","input_schema":{"type":"object","properties":{"file_path":{"type":"string"}}}}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	req.Header.Set("x-api-key", "user-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	stream := rec.Body.String()
	for _, expected := range []string{
		`"type":"thinking"`,
		`"type":"tool_use"`,
		`"id":"call_1"`,
		`"name":"Read"`,
		`"partial_json":"{\"file_path\":\"/tmp/demo.go\",\"limit\":200}"`,
		`"stop_reason":"tool_use"`,
		"event: message_stop",
	} {
		if !strings.Contains(stream, expected) {
			t.Fatalf("stream missing %q\nfull output:\n%s", expected, stream)
		}
	}
	if count := strings.Count(stream, `"type":"tool_use"`); count != 1 {
		t.Fatalf("tool_use starts = %d, want 1\n%s", count, stream)
	}
	if billingClient.commits != 1 || billingClient.releases != 0 {
		t.Fatalf("billing commits=%d releases=%d, want 1/0", billingClient.commits, billingClient.releases)
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
