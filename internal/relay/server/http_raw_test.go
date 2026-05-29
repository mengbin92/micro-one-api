package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	commonv1 "micro-one-api/api/common/v1"
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

func TestHTTPServerUsageReturnsBalanceForAPIKey(t *testing.T) {
	t.Setenv("PAYMENT_QUOTA_PER_UNIT", "500000")
	httpServer := NewHTTPServer(
		rawIdentityClient{userIDByToken: map[string]int64{"test-token": 42}},
		nil,
		&rawBillingClient{accountSnapshot: &commonv1.AccountSnapshot{
			UserId:       "42",
			Quota:        1000000,
			UsedQuota:    250000,
			RequestCount: 9,
			Group:        "default",
			GroupRatio:   1,
			FrozenQuota:  50000,
		}},
		nil,
		nil,
	)
	srv := khttp.NewServer()
	httpServer.RegisterRoutes(srv)

	req := httptest.NewRequest(http.MethodGet, "/v1/usage", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Mode      string  `json:"mode"`
		IsValid   bool    `json:"isValid"`
		Remaining float64 `json:"remaining"`
		Balance   float64 `json:"balance"`
		Unit      string  `json:"unit"`
		PlanName  string  `json:"planName"`
		Quota     struct {
			Remaining int64  `json:"remaining"`
			Used      int64  `json:"used"`
			Frozen    int64  `json:"frozen"`
			Unit      string `json:"unit"`
			PerUSD    int64  `json:"per_usd"`
		} `json:"quota"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v, body=%s", err, rec.Body.String())
	}
	if body.Mode != "unrestricted" || !body.IsValid || body.Remaining != 2 || body.Balance != 2 || body.Unit != "USD" || body.PlanName != "钱包余额" {
		t.Fatalf("usage summary mismatch: %+v", body)
	}
	if body.Quota.Remaining != 1000000 || body.Quota.Used != 250000 || body.Quota.Frozen != 50000 || body.Quota.Unit != "quota" || body.Quota.PerUSD != 500000 {
		t.Fatalf("quota mismatch: %+v", body.Quota)
	}
}

func TestHTTPServerAPIModelsReturnsOneAPIChannelModelMap(t *testing.T) {
	httpServer := NewHTTPServer(nil, nil, nil, nil, nil)
	srv := khttp.NewServer()
	httpServer.RegisterRoutes(srv)

	req := httptest.NewRequest(http.MethodGet, "/api/models", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Success bool                `json:"success"`
		Data    map[string][]string `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v, body=%s", err, rec.Body.String())
	}
	if !body.Success {
		t.Fatalf("success = false, body=%s", rec.Body.String())
	}
	if !containsString(body.Data["1"], "gpt-4o-mini") {
		t.Fatalf("openai channel models missing gpt-4o-mini: %s", rec.Body.String())
	}
	if !containsString(body.Data["6"], "deepseek-chat") {
		t.Fatalf("deepseek channel models missing deepseek-chat: %s", rec.Body.String())
	}
}

func TestHTTPServerAPIModelsReturnsProviderCatalogMetadata(t *testing.T) {
	httpServer := NewHTTPServer(nil, nil, nil, nil, nil)
	srv := khttp.NewServer()
	httpServer.RegisterRoutes(srv)

	req := httptest.NewRequest(http.MethodGet, "/api/models", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Success  bool `json:"success"`
		Metadata map[string]struct {
			Name                 string   `json:"name"`
			DefaultBaseURL       string   `json:"default_base_url"`
			RequiredConfigFields []string `json:"required_config_fields"`
			Adapter              string   `json:"adapter"`
			NativeSupported      bool     `json:"native_supported"`
			OpenAICompatible     bool     `json:"openai_compatible"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v, body=%s", err, rec.Body.String())
	}
	if !body.Success {
		t.Fatalf("success = false, body=%s", rec.Body.String())
	}
	azure := body.Metadata["5"]
	if azure.Name != "Azure OpenAI" || azure.DefaultBaseURL != "" || !containsString(azure.RequiredConfigFields, "base_url") || !containsString(azure.RequiredConfigFields, "api_version") || azure.Adapter != "native" || !azure.NativeSupported {
		t.Fatalf("azure metadata mismatch: %+v body=%s", azure, rec.Body.String())
	}
	hunyuan := body.Metadata["14"]
	if hunyuan.Name != "Tencent Hunyuan" || hunyuan.Adapter != "native_required" || hunyuan.NativeSupported || hunyuan.OpenAICompatible {
		t.Fatalf("hunyuan metadata mismatch: %+v body=%s", hunyuan, rec.Body.String())
	}
	ollama := body.Metadata["25"]
	if ollama.Name != "Ollama" || ollama.DefaultBaseURL != "http://localhost:11434/v1" || !ollama.OpenAICompatible {
		t.Fatalf("ollama metadata mismatch: %+v body=%s", ollama, rec.Body.String())
	}
	if !containsString(body.Metadata["26"].RequiredConfigFields, "account_id") {
		t.Fatalf("cloudflare metadata missing account_id: %+v body=%s", body.Metadata["26"], rec.Body.String())
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
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

func TestHTTPServerResponsesCreateForwardsAndCommitsResponsesUsage(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")

	var gotPath string
	var gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_test_123",
			"object":"response",
			"model":"gpt-4o-mini",
			"status":"completed",
			"usage":{"input_tokens":8,"output_tokens":5,"total_tokens":13}
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

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-4o-mini","input":"ping"}`))
	req.Header.Set("Authorization", "Bearer user-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if gotPath != "/v1/responses" {
		t.Fatalf("upstream path = %q", gotPath)
	}
	if gotAuth != "Bearer sk-upstream" {
		t.Fatalf("upstream auth = %q", gotAuth)
	}
	if !strings.Contains(rec.Body.String(), `"id":"resp_test_123"`) {
		t.Fatalf("response body was not forwarded: %s", rec.Body.String())
	}
	if billingClient.commits != 1 || billingClient.releases != 0 {
		t.Fatalf("billing commits=%d releases=%d", billingClient.commits, billingClient.releases)
	}
	if len(logClient.entries) != 1 {
		t.Fatalf("usage logs = %d, want 1", len(logClient.entries))
	}
	gotLog := logClient.entries[0]
	if gotLog.ModelName != "gpt-4o-mini" || gotLog.Quota != 13 || gotLog.PromptTokens != 8 || gotLog.CompletionTokens != 5 {
		t.Fatalf("usage log mismatch: model=%q quota=%d prompt=%d completion=%d", gotLog.ModelName, gotLog.Quota, gotLog.PromptTokens, gotLog.CompletionTokens)
	}
}

func TestExtractRawUsageFindsNestedResponsesUsage(t *testing.T) {
	usage := extractRawUsage([]byte(`{
		"type":"response.completed",
		"response":{
			"id":"resp_test_123",
			"usage":{"input_tokens":21,"output_tokens":9,"total_tokens":30}
		}
	}`), 100)

	if usage.TotalTokens != 30 || usage.PromptTokens != 21 || usage.CompletionTokens != 9 {
		t.Fatalf("usage = total:%d prompt:%d completion:%d", usage.TotalTokens, usage.PromptTokens, usage.CompletionTokens)
	}
}

func TestHTTPServerResponsesCreateStreamsRawSSE(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")

	var gotPath string
	var gotStream bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var payload map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		gotStream, _ = payload["stream"].(bool)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"pong\"}\n\n"))
		w.(http.Flusher).Flush()
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
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

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-4o-mini","input":"ping","stream":true}`))
	req.Header.Set("Authorization", "Bearer user-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if gotPath != "/v1/responses" || !gotStream {
		t.Fatalf("upstream path=%q stream=%v", gotPath, gotStream)
	}
	if contentType := rec.Header().Get("Content-Type"); !strings.Contains(contentType, "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream", contentType)
	}
	if !strings.Contains(rec.Body.String(), `response.output_text.delta`) || !strings.Contains(rec.Body.String(), `data: [DONE]`) {
		t.Fatalf("stream body was not forwarded: %s", rec.Body.String())
	}
	if billingClient.commits != 1 || billingClient.releases != 0 {
		t.Fatalf("billing commits=%d releases=%d", billingClient.commits, billingClient.releases)
	}
	if len(logClient.entries) != 1 || !logClient.entries[0].IsStream {
		t.Fatalf("stream usage log mismatch: entries=%d", len(logClient.entries))
	}
}

func TestHTTPServerResponsesCreateStreamsRawSSECommitsUsage(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"pong\"}\n\n"))
		w.(http.Flusher).Flush()
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":11,\"output_tokens\":7,\"total_tokens\":18}}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
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

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-4o-mini","input":"ping","stream":true}`))
	req.Header.Set("Authorization", "Bearer user-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if len(billingClient.commitRequests) != 1 {
		t.Fatalf("commit requests = %d, want 1", len(billingClient.commitRequests))
	}
	commit := billingClient.commitRequests[0]
	if commit.ActualTokens != 18 || commit.PromptTokens != 11 || commit.CompletionTokens != 7 {
		t.Fatalf("commit usage = total:%d prompt:%d completion:%d", commit.ActualTokens, commit.PromptTokens, commit.CompletionTokens)
	}
	if len(logClient.entries) != 1 {
		t.Fatalf("usage logs = %d, want 1", len(logClient.entries))
	}
	got := logClient.entries[0]
	if got.Quota != 18 || got.PromptTokens != 11 || got.CompletionTokens != 7 || !got.IsStream {
		t.Fatalf("log usage = quota:%d prompt:%d completion:%d stream:%v", got.Quota, got.PromptTokens, got.CompletionTokens, got.IsStream)
	}
}

func TestHTTPServerResponsesStreamCommitsAfterRequestContextCanceled(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"pong\"}\n\n"))
		w.(http.Flusher).Flush()
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()

	identityClient := rawIdentityClient{}
	channelClient := rawChannelClient{baseURL: upstream.URL + "/v1", key: "sk-upstream"}
	billingClient := &rawBillingClient{failOnCanceledContext: true}
	logClient := &rawLogClient{failOnCanceledContext: true}
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

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-4o-mini","input":"ping","stream":true}`)).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer user-token")
	req.Header.Set("Content-Type", "application/json")
	baseRec := httptest.NewRecorder()
	rec := &cancelOnFirstWriteRecorder{ResponseRecorder: baseRec, cancel: cancel}

	srv.ServeHTTP(rec, req)

	if baseRec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", baseRec.Code, baseRec.Body.String())
	}
	if billingClient.commits != 1 {
		t.Fatalf("commits = %d, want 1", billingClient.commits)
	}
	if len(logClient.entries) != 1 {
		t.Fatalf("usage logs = %d, want 1", len(logClient.entries))
	}
}

func TestHTTPServerBillingMutationsIgnoreCanceledRequestContext(t *testing.T) {
	billingClient := &rawBillingClient{failOnCanceledContext: true}
	srv := &HTTPServer{billingClient: billingClient}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := srv.commitQuota(ctx, "reservation-1", 12, true, usageLogInput{
		TokenName:        "token-1",
		Endpoint:         "/v1/responses",
		PromptTokens:     5,
		CompletionTokens: 7,
	}); err != nil {
		t.Fatalf("commitQuota error = %v", err)
	}
	if err := srv.releaseQuota(ctx, "reservation-2", "upstream error"); err != nil {
		t.Fatalf("releaseQuota error = %v", err)
	}
	if billingClient.commits != 1 || billingClient.releases != 1 {
		t.Fatalf("billing mutations = commits:%d releases:%d, want 1/1", billingClient.commits, billingClient.releases)
	}
}

type cancelOnFirstWriteRecorder struct {
	*httptest.ResponseRecorder
	cancel func()
	done   bool
}

func (r *cancelOnFirstWriteRecorder) Write(p []byte) (int, error) {
	n, err := r.ResponseRecorder.Write(p)
	if !r.done {
		r.done = true
		r.cancel()
	}
	return n, err
}

func (r *cancelOnFirstWriteRecorder) Flush() {}

func TestHTTPServerResponsesCreateFallsBackToChatCompletions(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")

	var gotPaths []string
	var chatPayload map[string]interface{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/responses":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"not found"}}`))
		case "/v1/chat/completions":
			_ = json.NewDecoder(r.Body).Decode(&chatPayload)
			_, _ = w.Write([]byte(`{
				"id":"chatcmpl_fallback_123",
				"object":"chat.completion",
				"created":1710000000,
				"model":"gpt-4o-mini",
				"choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}
			}`))
		default:
			http.NotFound(w, r)
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

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-4o-mini","input":"ping"}`))
	req.Header.Set("Authorization", "Bearer user-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if got := strings.Join(gotPaths, ","); got != "/v1/responses,/v1/chat/completions" {
		t.Fatalf("upstream paths = %q", got)
	}
	if chatPayload["model"] != "gpt-4o-mini" {
		t.Fatalf("fallback chat model = %v", chatPayload["model"])
	}
	if !strings.Contains(rec.Body.String(), `"object":"response"`) || !strings.Contains(rec.Body.String(), `"output_text":"pong"`) {
		t.Fatalf("fallback response mismatch: %s", rec.Body.String())
	}
	if billingClient.commits != 1 || billingClient.releases != 0 {
		t.Fatalf("billing commits=%d releases=%d", billingClient.commits, billingClient.releases)
	}
	if len(logClient.entries) != 1 {
		t.Fatalf("usage logs = %d, want 1", len(logClient.entries))
	}
	if len(billingClient.commitRequests) != 1 || billingClient.commitRequests[0].Endpoint != "/chat/completions" {
		t.Fatalf("commit endpoint mismatch: %#v", billingClient.commitRequests)
	}
	gotLog := logClient.entries[0]
	if gotLog.Quota != 6 || gotLog.PromptTokens != 4 || gotLog.CompletionTokens != 2 {
		t.Fatalf("usage log mismatch: quota=%d prompt=%d completion=%d", gotLog.Quota, gotLog.PromptTokens, gotLog.CompletionTokens)
	}
}

func TestHTTPServerResponsesCreateStreamFallsBackToChatCompletions(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")

	var gotPaths []string
	var chatStream bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.URL.Path)
		switch r.URL.Path {
		case "/v1/responses":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"not found"}}`))
		case "/v1/chat/completions":
			var payload map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			chatStream, _ = payload["stream"].(bool)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(`data: {"id":"chatcmpl_stream_123","object":"chat.completion.chunk","created":1710000000,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{"content":"po"},"finish_reason":null}]}` + "\n\n"))
			_, _ = w.Write([]byte(`data: {"id":"chatcmpl_stream_123","object":"chat.completion.chunk","created":1710000000,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{"content":"ng"},"finish_reason":null}]}` + "\n\n"))
			_, _ = w.Write([]byte(`data: {"id":"chatcmpl_stream_123","object":"chat.completion.chunk","created":1710000000,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}` + "\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		default:
			http.NotFound(w, r)
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

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-4o-mini","input":"ping","stream":true}`))
	req.Header.Set("Authorization", "Bearer user-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if got := strings.Join(gotPaths, ","); got != "/v1/responses,/v1/chat/completions" {
		t.Fatalf("upstream paths = %q", got)
	}
	if !chatStream {
		t.Fatal("fallback chat request was not streaming")
	}
	body := rec.Body.String()
	for _, want := range []string{
		`event: response.created`,
		`"type":"response.created"`,
		`event: response.in_progress`,
		`"type":"response.in_progress"`,
		`event: response.output_item.added`,
		`"type":"response.output_item.added"`,
		`event: response.content_part.added`,
		`"type":"response.content_part.added"`,
		`event: response.output_text.delta`,
		`"type":"response.output_text.delta"`,
		`"delta":"po"`,
		`event: response.output_text.done`,
		`"type":"response.output_text.done"`,
		`event: response.content_part.done`,
		`"type":"response.content_part.done"`,
		`event: response.output_item.done`,
		`"type":"response.output_item.done"`,
		`event: response.completed`,
		`"type":"response.completed"`,
		`data: [DONE]`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("fallback stream body missing %s: %s", want, body)
		}
	}
	if strings.Contains(body, `"response_id":"chatcmpl_stream_123"`) {
		t.Fatalf("fallback stream leaked chat completion id as response_id: %s", body)
	}
	if !strings.Contains(body, `"response_id":"resp_`) {
		t.Fatalf("fallback stream body mismatch: %s", body)
	}
	if billingClient.commits != 1 || billingClient.releases != 0 {
		t.Fatalf("billing commits=%d releases=%d", billingClient.commits, billingClient.releases)
	}
	if len(billingClient.commitRequests) != 1 || billingClient.commitRequests[0].Endpoint != "/chat/completions" {
		t.Fatalf("commit endpoint mismatch: %#v", billingClient.commitRequests)
	}
	if len(logClient.entries) != 1 || !logClient.entries[0].IsStream {
		t.Fatalf("stream usage log mismatch: entries=%#v", logClient.entries)
	}
	gotLog := logClient.entries[0]
	if gotLog.Quota != 6 || gotLog.PromptTokens != 4 || gotLog.CompletionTokens != 2 {
		t.Fatalf("stream usage log = quota:%d prompt:%d completion:%d", gotLog.Quota, gotLog.PromptTokens, gotLog.CompletionTokens)
	}
	if !strings.Contains(body, `"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}`) {
		t.Fatalf("fallback stream response missing responses usage: %s", body)
	}
}

func TestHTTPServerResponsesCreateStreamFallbackConvertsToolCalls(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")

	var chatStream bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/responses":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"not found"}}`))
		case "/v1/chat/completions":
			var payload map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			chatStream, _ = payload["stream"].(bool)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(`data: {"id":"chatcmpl_tool_123","object":"chat.completion.chunk","created":1710000000,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_exec_123","type":"function","function":{"name":"exec_command","arguments":"{\"cmd\":"}}]},"finish_reason":null}]}` + "\n\n"))
			_, _ = w.Write([]byte(`data: {"id":"chatcmpl_tool_123","object":"chat.completion.chunk","created":1710000000,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"date\"}"}}]},"finish_reason":null}]}` + "\n\n"))
			_, _ = w.Write([]byte(`data: {"id":"chatcmpl_tool_123","object":"chat.completion.chunk","created":1710000000,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":9,"completion_tokens":3,"total_tokens":12}}` + "\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		default:
			http.NotFound(w, r)
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

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{
		"model":"gpt-4o-mini",
		"input":"run date",
		"tools":[{"type":"function","name":"exec_command","parameters":{"type":"object"}}],
		"stream":true
	}`))
	req.Header.Set("Authorization", "Bearer user-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if !chatStream {
		t.Fatal("fallback chat request was not streaming")
	}
	body := rec.Body.String()
	for _, want := range []string{
		`event: response.output_item.added`,
		`"type":"function_call"`,
		`"call_id":"call_exec_123"`,
		`"name":"exec_command"`,
		`event: response.function_call_arguments.delta`,
		`"delta":"{\"cmd\":"`,
		`"delta":"\"date\"}"`,
		`event: response.function_call_arguments.done`,
		`"arguments":"{\"cmd\":\"date\"}"`,
		`event: response.output_item.done`,
		`event: response.completed`,
		`data: [DONE]`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("fallback tool stream body missing %s: %s", want, body)
		}
	}
	if strings.Contains(body, `response.output_text.done`) {
		t.Fatalf("tool-call fallback should not emit text-done events: %s", body)
	}
	if billingClient.commits != 1 || billingClient.releases != 0 {
		t.Fatalf("billing commits=%d releases=%d", billingClient.commits, billingClient.releases)
	}
	if len(logClient.entries) != 1 || !logClient.entries[0].IsStream {
		t.Fatalf("stream usage log mismatch: entries=%#v", logClient.entries)
	}
}

func TestResponsesRequestToChatCompletionsMapsMaxOutputTokens(t *testing.T) {
	body, stream, err := responsesRequestToChatCompletionsBody([]byte(`{"model":"gpt-4o-mini","input":"ping","max_output_tokens":123,"stream":true}`))
	if err != nil {
		t.Fatalf("responsesRequestToChatCompletionsBody error: %v", err)
	}
	if !stream {
		t.Fatal("stream = false, want true")
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode chat body: %v, body=%s", err, string(body))
	}
	if got := payload["max_tokens"]; got != float64(123) {
		t.Fatalf("max_tokens = %#v, want 123; body=%s", got, string(body))
	}
	if _, ok := payload["max_output_tokens"]; ok {
		t.Fatalf("chat body should not include max_output_tokens: %s", string(body))
	}
}

func TestResponsesRequestToChatCompletionsConvertsCodexPayload(t *testing.T) {
	body, stream, err := responsesRequestToChatCompletionsBody([]byte(`{
		"model":"mimo-v2.5-pro",
		"input":[
			{"type":"message","role":"developer","content":[
				{"type":"input_text","text":"system rules"},
				{"type":"input_text","text":"tool rules"}
			]},
			{"type":"message","role":"user","content":[
				{"type":"input_text","text":"只回复 pong"}
			]}
		],
		"tools":[
			{"type":"function","name":"exec_command","description":"run shell","parameters":{"type":"object","properties":{"cmd":{"type":"string"}},"required":["cmd"]}},
			{"type":"custom","name":"apply_patch","description":"patch files"},
			{"type":"web_search","external_web_access":false}
		],
		"tool_choice":{"type":"function","name":"exec_command"},
		"parallel_tool_calls":true,
		"prompt_cache_key":"thread_123",
		"client_metadata":{"x-codex-installation-id":"install_123"},
		"stream":true
	}`))
	if err != nil {
		t.Fatalf("responsesRequestToChatCompletionsBody error: %v", err)
	}
	if !stream {
		t.Fatal("stream = false, want true")
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode chat body: %v, body=%s", err, string(body))
	}
	messages, ok := payload["messages"].([]interface{})
	if !ok || len(messages) != 2 {
		t.Fatalf("messages mismatch: %#v body=%s", payload["messages"], string(body))
	}
	first := messages[0].(map[string]interface{})
	if first["role"] != "system" || first["content"] != "system rules\ntool rules" {
		t.Fatalf("developer message was not converted to system text: %#v", first)
	}
	second := messages[1].(map[string]interface{})
	if second["role"] != "user" || second["content"] != "只回复 pong" {
		t.Fatalf("user message mismatch: %#v", second)
	}
	tools, ok := payload["tools"].([]interface{})
	if !ok || len(tools) != 1 {
		t.Fatalf("tools mismatch: %#v body=%s", payload["tools"], string(body))
	}
	tool := tools[0].(map[string]interface{})
	if tool["type"] != "function" {
		t.Fatalf("tool type = %#v, want function", tool["type"])
	}
	fn, ok := tool["function"].(map[string]interface{})
	if !ok || fn["name"] != "exec_command" || fn["description"] != "run shell" {
		t.Fatalf("function tool was not converted: %#v", tool)
	}
	if _, ok := payload["parallel_tool_calls"]; ok {
		t.Fatalf("chat body should not include Responses-only parallel_tool_calls: %s", string(body))
	}
	choice := payload["tool_choice"].(map[string]interface{})
	if choice["type"] != "function" {
		t.Fatalf("tool_choice type mismatch: %#v", choice)
	}
	choiceFn := choice["function"].(map[string]interface{})
	if choiceFn["name"] != "exec_command" {
		t.Fatalf("tool_choice function mismatch: %#v", choice)
	}
}

func TestHTTPServerResponsesRetrieveUsesStoredResponseRoute(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")

	var gotPaths []string
	var gotMethods []string
	var gotQueries []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.URL.Path)
		gotMethods = append(gotMethods, r.Method)
		gotQueries = append(gotQueries, r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/responses":
			_, _ = w.Write([]byte(`{"id":"resp_test_123","object":"response","model":"gpt-4o-mini","usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}`))
		case "/v1/responses/resp_test_123":
			_, _ = w.Write([]byte(`{"id":"resp_test_123","object":"response","status":"completed"}`))
		case "/v1/responses/resp_test_123/input_items":
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		case "/v1/responses/resp_test_123/cancel":
			_, _ = w.Write([]byte(`{"id":"resp_test_123","object":"response","status":"cancelled"}`))
		default:
			http.NotFound(w, r)
		}
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

	createReq := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-4o-mini","input":"ping"}`))
	createReq.Header.Set("Authorization", "Bearer user-token")
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	srv.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create status = %d, want 200, body=%s", createRec.Code, createRec.Body.String())
	}

	cases := []struct {
		method string
		path   string
		body   string
		want   string
	}{
		{http.MethodGet, "/v1/responses/resp_test_123", "", `"status":"completed"`},
		{http.MethodGet, "/v1/responses/resp_test_123/input_items?limit=1", "", `"object":"list"`},
		{http.MethodPost, "/v1/responses/resp_test_123/cancel", "{}", `"status":"cancelled"`},
		{http.MethodDelete, "/v1/responses/resp_test_123", "", `"status":"completed"`},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Authorization", "Bearer user-token")
		if tc.body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s status = %d, want 200, body=%s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), tc.want) {
			t.Fatalf("%s %s body was not forwarded: %s", tc.method, tc.path, rec.Body.String())
		}
	}

	wantPaths := []string{
		"/v1/responses",
		"/v1/responses/resp_test_123",
		"/v1/responses/resp_test_123/input_items",
		"/v1/responses/resp_test_123/cancel",
		"/v1/responses/resp_test_123",
	}
	wantMethods := []string{http.MethodPost, http.MethodGet, http.MethodGet, http.MethodPost, http.MethodDelete}
	if len(gotPaths) != len(wantPaths) {
		t.Fatalf("upstream paths = %#v, want %#v", gotPaths, wantPaths)
	}
	for i := range wantPaths {
		if gotPaths[i] != wantPaths[i] || gotMethods[i] != wantMethods[i] {
			t.Fatalf("upstream call %d = %s %s, want %s %s", i, gotMethods[i], gotPaths[i], wantMethods[i], wantPaths[i])
		}
	}
	if gotQueries[2] != "limit=1" {
		t.Fatalf("input_items query = %q, want limit=1", gotQueries[2])
	}
	if billingClient.commits != 5 || billingClient.releases != 0 {
		t.Fatalf("billing commits=%d releases=%d", billingClient.commits, billingClient.releases)
	}
}

func TestHTTPServerResponsesInputTokensForwardsStandardEndpoint(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")

	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"response.input_tokens","total_tokens":11,"usage":{"input_tokens":11,"total_tokens":11}}`))
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

	req := httptest.NewRequest(http.MethodPost, "/v1/responses/input_tokens", strings.NewReader(`{"model":"gpt-4o-mini","input":"ping"}`))
	req.Header.Set("Authorization", "Bearer user-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if gotPath != "/v1/responses/input_tokens" {
		t.Fatalf("upstream path = %q", gotPath)
	}
	if !strings.Contains(rec.Body.String(), `"total_tokens":11`) {
		t.Fatalf("response body was not forwarded: %s", rec.Body.String())
	}
	if billingClient.commits != 1 || billingClient.releases != 0 {
		t.Fatalf("billing commits=%d releases=%d", billingClient.commits, billingClient.releases)
	}
}

func TestHTTPServerResponsesStoredRouteIsScopedToCreatingUser(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")

	upstreamCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_test_123","object":"response","model":"gpt-4o-mini","usage":{"total_tokens":5}}`))
	}))
	defer upstream.Close()

	identityClient := rawIdentityClient{userIDByToken: map[string]int64{
		"user-token":  42,
		"other-token": 99,
	}}
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

	createReq := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-4o-mini","input":"ping"}`))
	createReq.Header.Set("Authorization", "Bearer user-token")
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	srv.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create status = %d, want 200, body=%s", createRec.Code, createRec.Body.String())
	}

	retrieveReq := httptest.NewRequest(http.MethodGet, "/v1/responses/resp_test_123", nil)
	retrieveReq.Header.Set("Authorization", "Bearer other-token")
	retrieveRec := httptest.NewRecorder()
	srv.ServeHTTP(retrieveRec, retrieveReq)

	if retrieveRec.Code != http.StatusNotFound {
		t.Fatalf("retrieve status = %d, want 404, body=%s", retrieveRec.Code, retrieveRec.Body.String())
	}
	if upstreamCalls != 1 {
		t.Fatalf("upstream calls = %d, want 1", upstreamCalls)
	}
	if billingClient.commits != 1 || billingClient.releases != 0 {
		t.Fatalf("billing commits=%d releases=%d", billingClient.commits, billingClient.releases)
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
	if got.ChannelId != 11 || got.TokenName != "test-token" || got.IsStream {
		t.Fatalf("log metadata mismatch: channel=%d token=%q stream=%v", got.ChannelId, got.TokenName, got.IsStream)
	}
}

func TestHTTPServerChatCompletionRejectsFailedReservation(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")

	upstreamCalled := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"usage":{"total_tokens":1}}`))
	}))
	defer upstream.Close()

	identityClient := rawIdentityClient{}
	channelClient := rawChannelClient{baseURL: upstream.URL + "/v1", key: "sk-upstream"}
	billingClient := &rawBillingClient{reserveSuccess: false, reserveMessage: "insufficient quota"}
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

	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402, body=%s", rec.Code, rec.Body.String())
	}
	if upstreamCalled {
		t.Fatal("upstream was called after failed reservation")
	}
	if billingClient.commits != 0 {
		t.Fatalf("commits = %d, want 0", billingClient.commits)
	}
	if len(logClient.entries) != 0 {
		t.Fatalf("usage logs = %d, want 0", len(logClient.entries))
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
