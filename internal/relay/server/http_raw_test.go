package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	relaybiz "micro-one-api/internal/relay/biz"
	relaydata "micro-one-api/internal/relay/data"
	relayprovider "micro-one-api/internal/relay/provider"

	khttp "github.com/go-kratos/kratos/v2/transport/http"
)

func TestHTTPServerRawRoutesAreRegistered(t *testing.T) {
	httpServer := NewHTTPServer(nil, nil, nil, nil, nil)
	srv := khttp.NewServer()
	httpServer.RegisterRoutes(srv)

	for _, route := range []string{
		"/v1/completions",
		"/v1/embeddings",
		"/v1/images/generations",
		"/v1/audio/transcriptions",
		"/v1/audio/translations",
		"/v1/audio/speech",
		"/v1/moderations",
	} {
		req := httptest.NewRequest(http.MethodPost, route, strings.NewReader(`{}`))
		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		if rec.Code == http.StatusNotFound {
			t.Fatalf("route %s is not registered", route)
		}
	}
}

func TestHTTPServerUnsupportedOpenAIRoutesReturnStableNotImplemented(t *testing.T) {
	httpServer := NewHTTPServer(nil, nil, nil, nil, nil)
	srv := khttp.NewServer()
	httpServer.RegisterRoutes(srv)

	cases := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/v1/edits", `{}`},
		{http.MethodPost, "/v1/responses", `{}`},
		{http.MethodGet, "/v1/responses/resp-123", ``},
		{http.MethodDelete, "/v1/responses/resp-123", ``},
		{http.MethodPost, "/v1/responses/resp-123/cancel", ``},
		{http.MethodGet, "/v1/responses/resp-123/input_items", ``},
		{http.MethodGet, "/v1/engines", ``},
		{http.MethodPost, "/v1/engines/text-embedding-ada-002/embeddings", `{}`},
		{http.MethodGet, "/v1/files", ``},
		{http.MethodPost, "/v1/files", `{}`},
		{http.MethodGet, "/v1/files/file-123", ``},
		{http.MethodDelete, "/v1/files/file-123", ``},
		{http.MethodPost, "/v1/files/file-123/content", ``},
		{http.MethodPost, "/v1/fine-tunes", `{}`},
		{http.MethodGet, "/v1/fine-tunes", ``},
		{http.MethodGet, "/v1/fine-tunes/ft-123", ``},
		{http.MethodPost, "/v1/fine-tunes/ft-123/cancel", ``},
		{http.MethodPost, "/v1/fine_tuning/jobs", `{}`},
		{http.MethodGet, "/v1/fine_tuning/jobs", ``},
		{http.MethodGet, "/v1/fine_tuning/jobs/ftjob-123", ``},
		{http.MethodPost, "/v1/fine_tuning/jobs/ftjob-123/cancel", ``},
		{http.MethodGet, "/v1/batches", ``},
		{http.MethodPost, "/v1/batches", `{}`},
		{http.MethodGet, "/v1/batches/batch-123", ``},
		{http.MethodPost, "/v1/batches/batch-123/cancel", ``},
		{http.MethodPost, "/v1/uploads", `{}`},
		{http.MethodPost, "/v1/uploads/upload-123/parts", `{}`},
		{http.MethodPost, "/v1/uploads/upload-123/complete", `{}`},
		{http.MethodPost, "/v1/uploads/upload-123/cancel", `{}`},
		{http.MethodPost, "/v1/images/edits", `{}`},
		{http.MethodPost, "/v1/images/variations", `{}`},
		{http.MethodGet, "/v1/vector_stores", ``},
		{http.MethodPost, "/v1/vector_stores", `{}`},
		{http.MethodGet, "/v1/vector_stores/vs-123", ``},
		{http.MethodDelete, "/v1/vector_stores/vs-123", ``},
		{http.MethodGet, "/v1/vector_stores/vs-123/files", ``},
		{http.MethodPost, "/v1/vector_stores/vs-123/files", `{}`},
		{http.MethodPost, "/v1/vector_stores/vs-123/file_batches", `{}`},
		{http.MethodGet, "/v1/evals", ``},
		{http.MethodPost, "/v1/evals", `{}`},
		{http.MethodGet, "/v1/evals/eval-123", ``},
		{http.MethodPost, "/v1/evals/eval-123/runs", `{}`},
		{http.MethodGet, "/v1/evals/eval-123/runs", ``},
		{http.MethodGet, "/v1/containers", ``},
		{http.MethodPost, "/v1/containers", `{}`},
		{http.MethodGet, "/v1/containers/container-123", ``},
		{http.MethodDelete, "/v1/containers/container-123", ``},
		{http.MethodGet, "/v1/containers/container-123/files", ``},
		{http.MethodPost, "/v1/containers/container-123/files", `{}`},
		{http.MethodGet, "/v1/containers/container-123/files/file-123", ``},
		{http.MethodGet, "/v1/containers/container-123/files/file-123/content", ``},
		{http.MethodDelete, "/v1/containers/container-123/files/file-123", ``},
		{http.MethodPost, "/v1/fine_tuning/alpha/graders/validate", `{}`},
		{http.MethodPost, "/v1/fine_tuning/alpha/graders/run", `{}`},
		{http.MethodPost, "/v1/realtime/sessions", `{}`},
		{http.MethodPost, "/v1/realtime/transcription_sessions", `{}`},
		{http.MethodPost, "/v1/conversations", `{}`},
		{http.MethodGet, "/v1/conversations/conv-123", ``},
		{http.MethodPost, "/v1/conversations/conv-123/items", `{}`},
		{http.MethodGet, "/v1/conversations/conv-123/items", ``},
		{http.MethodDelete, "/v1/conversations/conv-123/items/item-123", ``},
		{http.MethodGet, "/v1/assistants", ``},
		{http.MethodPost, "/v1/assistants", `{}`},
		{http.MethodGet, "/v1/assistants/asst-123", ``},
		{http.MethodPost, "/v1/threads", `{}`},
		{http.MethodGet, "/v1/threads/thread-123", ``},
		{http.MethodPost, "/v1/threads/thread-123/messages", `{}`},
		{http.MethodGet, "/v1/threads/thread-123/messages", ``},
		{http.MethodPost, "/v1/threads/thread-123/runs", `{}`},
		{http.MethodGet, "/v1/threads/thread-123/runs", ``},
		{http.MethodGet, "/v1/threads/thread-123/runs/run-123", ``},
	}

	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("%s %s status = %d, want 501, body=%s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, `"error"`) || !strings.Contains(body, `"type":"one_api_not_implemented"`) {
			t.Fatalf("%s %s error shape mismatch: %s", tc.method, tc.path, body)
		}
	}
}

func TestHTTPServerRetrieveModelCompatibility(t *testing.T) {
	httpServer := NewHTTPServer(nil, nil, nil, nil, nil)
	srv := khttp.NewServer()
	httpServer.RegisterRoutes(srv)

	req := httptest.NewRequest(http.MethodGet, "/v1/models/gpt-4o-mini", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"id":"gpt-4o-mini"`) {
		t.Fatalf("model response missing id: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"permission"`) {
		t.Fatalf("model response missing permission: %s", rec.Body.String())
	}
}

func TestHTTPServerAPIStatusCompatibility(t *testing.T) {
	httpServer := NewHTTPServer(nil, nil, nil, nil, nil)
	srv := khttp.NewServer()
	httpServer.RegisterRoutes(srv)

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"success":true`) {
		t.Fatalf("status response missing success: %s", rec.Body.String())
	}
}

func TestHTTPServerRawRouteRequiresAuthorization(t *testing.T) {
	httpServer := NewHTTPServer(nil, nil, nil, nil, nil)
	srv := khttp.NewServer()
	httpServer.RegisterRoutes(srv)

	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(`{"input":"hello"}`))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", rec.Code, rec.Body.String())
	}
}

func TestHTTPServerRawRelayForwardsResponseAndCommitsBilling(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")

	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"embedding":[1]}],"usage":{"total_tokens":17}}`))
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

	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(`{"model":"text-embedding-ada-002","input":"hello"}`))
	req = req.WithContext(context.Background())
	req.Header.Set("Authorization", "Bearer user-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if gotPath != "/v1/embeddings" {
		t.Fatalf("upstream path = %q", gotPath)
	}
	if !strings.Contains(rec.Body.String(), `"embedding"`) {
		t.Fatalf("response body was not forwarded: %s", rec.Body.String())
	}
	if billingClient.commits != 1 {
		t.Fatalf("commits = %d, want 1", billingClient.commits)
	}
	if billingClient.releases != 0 {
		t.Fatalf("releases = %d, want 0", billingClient.releases)
	}
	if len(logClient.entries) != 1 {
		t.Fatalf("usage logs = %d, want 1", len(logClient.entries))
	}
	if got := logClient.entries[0]; got.ModelName != "text-embedding-ada-002" || got.Quota != 17 || got.ChannelId != 11 {
		t.Fatalf("usage log mismatch: model=%q quota=%d channel=%d", got.ModelName, got.Quota, got.ChannelId)
	}
}

func TestHTTPServerRawRelayForwardsAzureWithConfiguredAPIVersion(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")

	var gotPath string
	var gotQuery string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"usage":{"total_tokens":5}}`))
	}))
	defer upstream.Close()

	identityClient := rawIdentityClient{}
	channelClient := rawChannelClient{
		baseURL:    upstream.URL,
		key:        "sk-upstream",
		chType:     relayprovider.ChannelTypeAzure,
		apiVersion: "2024-10-21",
	}
	billingClient := &rawBillingClient{}
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
	)
	srv := khttp.NewServer()
	httpServer.RegisterRoutes(srv)

	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(`{"model":"embedding-deploy","input":"hello"}`))
	req.Header.Set("Authorization", "Bearer user-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if gotPath != "/openai/deployments/embedding-deploy/embeddings" {
		t.Fatalf("path = %q", gotPath)
	}
	if !strings.Contains(gotQuery, "api-version=2024-10-21") {
		t.Fatalf("query = %q", gotQuery)
	}
}

func TestHTTPServerChatCompletionWritesUsageLogOnSuccess(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-test",
			"object":"chat.completion",
			"created":1710000000,
			"model":"gpt-4o-mini",
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

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"ping"}]}`))
	req.Header.Set("Authorization", "Bearer user-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if len(logClient.entries) != 1 {
		t.Fatalf("usage logs = %d, want 1", len(logClient.entries))
	}
	got := logClient.entries[0]
	if got.UserId != 42 {
		t.Fatalf("log user_id = %d, want 42", got.UserId)
	}
	if got.Source != "relay-gateway" || got.Level != "consume" {
		t.Fatalf("log level/source = %q/%q", got.Level, got.Source)
	}
	if got.ModelName != "gpt-4o-mini" {
		t.Fatalf("log model_name = %q, want gpt-4o-mini", got.ModelName)
	}
	if got.Quota != 12 || got.PromptTokens != 7 || got.CompletionTokens != 5 {
		t.Fatalf("log usage = quota:%d prompt:%d completion:%d", got.Quota, got.PromptTokens, got.CompletionTokens)
	}
	if got.ChannelId != 11 || got.TokenName != "token-7" || got.IsStream {
		t.Fatalf("log metadata mismatch: channel=%d token=%q stream=%v", got.ChannelId, got.TokenName, got.IsStream)
	}
}

func TestHTTPServerStreamingChatCompletionWritesPreciseUsageLogOnSuccess(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{
			`data: {"id":"chunk1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{"content":"pong"},"finish_reason":null}]}`,
			`data: {"id":"chunk2","object":"chat.completion.chunk","created":1710000000,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":9,"completion_tokens":4,"total_tokens":13}}`,
			`data: [DONE]`,
		}
		for _, chunk := range chunks {
			_, _ = w.Write([]byte(chunk + "\n\n"))
			w.(http.Flusher).Flush()
		}
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

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o-mini","stream":true,"messages":[{"role":"user","content":"ping"}]}`))
	req.Header.Set("Authorization", "Bearer user-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if billingClient.commits != 1 || billingClient.releases != 0 {
		t.Fatalf("billing commits=%d releases=%d", billingClient.commits, billingClient.releases)
	}
	if len(logClient.entries) != 1 {
		t.Fatalf("usage logs = %d, want 1", len(logClient.entries))
	}
	got := logClient.entries[0]
	if !got.IsStream {
		t.Fatalf("log is_stream = false, want true")
	}
	if got.Quota != 13 || got.PromptTokens != 9 || got.CompletionTokens != 4 {
		t.Fatalf("log usage = quota:%d prompt:%d completion:%d", got.Quota, got.PromptTokens, got.CompletionTokens)
	}
	if got.ModelName != "gpt-4o-mini" || got.ChannelId != 11 || got.UserId != 42 {
		t.Fatalf("log metadata mismatch: model=%q channel=%d user=%d", got.ModelName, got.ChannelId, got.UserId)
	}
}

func TestHTTPServerRawRelayReleasesBillingOnUpstreamError(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream failed", http.StatusBadGateway)
	}))
	defer upstream.Close()

	identityClient := rawIdentityClient{}
	channelClient := rawChannelClient{baseURL: upstream.URL + "/v1", key: "sk-upstream"}
	billingClient := &rawBillingClient{}
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
	)
	srv := khttp.NewServer()
	httpServer.RegisterRoutes(srv)

	req := httptest.NewRequest(http.MethodPost, "/v1/moderations", strings.NewReader(`{"input":"hello"}`))
	req.Header.Set("Authorization", "Bearer user-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		body, _ := io.ReadAll(rec.Result().Body)
		t.Fatalf("status = %d, want 502, body=%s", rec.Code, string(body))
	}
	if billingClient.commits != 0 {
		t.Fatalf("commits = %d, want 0", billingClient.commits)
	}
	if billingClient.releases != 1 {
		t.Fatalf("releases = %d, want 1", billingClient.releases)
	}
}

func TestHTTPServerOneAPIProxyForwardsExplicitChannel(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")

	var gotMethod string
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	identityClient := rawIdentityClient{}
	channelClient := rawChannelClient{baseURL: upstream.URL + "/v1", key: "sk-upstream"}
	billingClient := &rawBillingClient{}
	httpServer := NewHTTPServer(
		identityClient,
		channelClient,
		billingClient,
		relayprovider.NewProviderFactory(time.Second),
		nil,
	)
	srv := khttp.NewServer()
	httpServer.RegisterRoutes(srv)

	req := httptest.NewRequest(http.MethodPatch, "/v1/oneapi/proxy/11/custom/path", strings.NewReader(`{"model":"gpt-3.5-turbo"}`))
	req.Header.Set("Authorization", "Bearer user-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if gotMethod != http.MethodPatch {
		t.Fatalf("method = %q", gotMethod)
	}
	if gotPath != "/v1/custom/path" {
		t.Fatalf("path = %q", gotPath)
	}
}
