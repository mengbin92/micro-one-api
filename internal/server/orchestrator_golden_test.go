package server

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	billingv1 "micro-one-api/api/billing/v1"
	logv1 "micro-one-api/api/log/v1"
	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	relaydata "micro-one-api/internal/data"
	"micro-one-api/pkg/jsonx"

	khttp "github.com/go-kratos/kratos/v3/transport/http"
)

type chatCompletionGoldenOutcome struct {
	status      int
	body        []byte
	contentType string
	commits     int
	releases    int
	commit      *billingv1.CommitQuotaRequest
	log         *logv1.IngestLogRequest
}

func runChatCompletionPath(t *testing.T, orchestrator bool, upstreamStatus int, upstreamBody string) chatCompletionGoldenOutcome {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(upstreamStatus)
		_, _ = w.Write([]byte(upstreamBody))
	}))
	t.Cleanup(upstream.Close)

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
	if orchestrator {
		httpServer.SetRelayOrchestratorEnabled(true)
		httpServer.SetRelayOrchestratorTokenAllowlist([]string{sha256Hex("user-token")})
	}
	srv := khttp.NewServer()
	httpServer.RegisterRoutes(srv)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"ping"}]}`))
	req.Header.Set("Authorization", "Bearer user-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	var commit *billingv1.CommitQuotaRequest
	if len(billingClient.commitRequests) > 0 {
		commit = billingClient.commitRequests[0]
	}
	var logEntry *logv1.IngestLogRequest
	if len(logClient.entries) > 0 {
		logEntry = logClient.entries[0]
	}
	return chatCompletionGoldenOutcome{
		status:      rec.Code,
		body:        append([]byte(nil), rec.Body.Bytes()...),
		contentType: rec.Header().Get("Content-Type"),
		commits:     billingClient.commits,
		releases:    billingClient.releases,
		commit:      commit,
		log:         logEntry,
	}
}

func canonicalJSON(t *testing.T, body []byte) []byte {
	t.Helper()
	var value any
	if err := jsonx.Unmarshal(body, &value); err != nil {
		t.Fatalf("decode response JSON: %v; body=%s", err, body)
	}
	canonical, err := jsonx.Marshal(value)
	if err != nil {
		t.Fatalf("encode response JSON: %v", err)
	}
	return canonical
}

func assertChatCompletionGoldenLifecycle(t *testing.T, want, got chatCompletionGoldenOutcome) {
	t.Helper()
	if got.status != want.status {
		t.Fatalf("status = %d, want %d", got.status, want.status)
	}
	if got.contentType != want.contentType {
		t.Fatalf("content type = %q, want %q", got.contentType, want.contentType)
	}
	if !bytes.Equal(canonicalJSON(t, got.body), canonicalJSON(t, want.body)) {
		t.Fatalf("response body differs:\n got: %s\nwant: %s", got.body, want.body)
	}
	if got.commits != want.commits || got.releases != want.releases {
		t.Fatalf("quota lifecycle = commits:%d releases:%d, want commits:%d releases:%d", got.commits, got.releases, want.commits, want.releases)
	}
	if got.commit == nil || want.commit == nil {
		t.Fatalf("commit presence = got:%v want:%v", got.commit != nil, want.commit != nil)
	}
	if got.commit.ActualTokens != want.commit.ActualTokens || got.commit.PromptTokens != want.commit.PromptTokens || got.commit.CompletionTokens != want.commit.CompletionTokens {
		t.Fatalf("commit usage differs: got=%#v want=%#v", got.commit, want.commit)
	}
	if got.log == nil || want.log == nil {
		t.Fatalf("log presence = got:%v want:%v", got.log != nil, want.log != nil)
	}
	if got.log.Quota != want.log.Quota || got.log.PromptTokens != want.log.PromptTokens || got.log.CompletionTokens != want.log.CompletionTokens || got.log.ModelName != want.log.ModelName || got.log.ChannelId != want.log.ChannelId || got.log.IsStream != want.log.IsStream {
		t.Fatalf("usage log differs: got=%#v want=%#v", got.log, want.log)
	}
}

func TestChatCompletionsOrchestratorGoldenSuccessMatchesLegacy(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	upstreamBody := `{"id":"chatcmpl-golden","object":"chat.completion","created":1710000000,"model":"gpt-4o-mini","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":5,"total_tokens":12,"prompt_tokens_details":{},"input_tokens_details":{}}}`
	want := runChatCompletionPath(t, false, http.StatusOK, upstreamBody)
	got := runChatCompletionPath(t, true, http.StatusOK, upstreamBody)
	assertChatCompletionGoldenLifecycle(t, want, got)
}

func TestChatCompletionsOrchestratorUpstreamErrorReleasesQuota(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	got := runChatCompletionPath(t, true, http.StatusBadGateway, `{"error":{"message":"secret-provider-detail"}}`)

	if got.status != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502, body=%s", got.status, got.body)
	}
	if strings.Contains(string(got.body), "secret-provider-detail") {
		t.Fatalf("upstream error detail leaked: %s", got.body)
	}
	if !strings.Contains(string(got.body), "upstream service unavailable") {
		t.Fatalf("safe upstream error missing: %s", got.body)
	}
	if got.commits != 0 || got.releases != 1 {
		t.Fatalf("quota lifecycle = commits:%d releases:%d, want commits:0 releases:1", got.commits, got.releases)
	}
	if got.log != nil {
		t.Fatal("unexpected usage log for failed request")
	}
}

func TestOrchestratorErrorMessagePreservesErrorOwnership(t *testing.T) {
	tests := []struct {
		name   string
		status int
		err    error
		want   string
	}{
		{name: "local auth", status: http.StatusUnauthorized, err: errors.New("token disabled"), want: "unauthorized"},
		{name: "local quota", status: http.StatusPaymentRequired, err: errors.New("reserve failed"), want: "insufficient quota"},
		{name: "upstream rate limit", status: http.StatusTooManyRequests, err: &relayprovider.UpstreamHTTPError{StatusCode: http.StatusTooManyRequests, Body: []byte("secret")}, want: "upstream rate limited"},
		{name: "upstream failure", status: http.StatusBadGateway, err: &relayprovider.UpstreamHTTPError{StatusCode: http.StatusBadGateway, Body: []byte("secret")}, want: "upstream service unavailable"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := orchestratorErrorMessage(tt.status, tt.err); got != tt.want {
				t.Fatalf("orchestratorErrorMessage() = %q, want %q", got, tt.want)
			}
		})
	}
}
