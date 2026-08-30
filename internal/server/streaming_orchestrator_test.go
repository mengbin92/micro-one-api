package server

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"github.com/prometheus/client_golang/prometheus/testutil"

	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	relaydata "micro-one-api/internal/data"
	"micro-one-api/platform/metrics"
)

type terminalEOFStream struct {
	body []byte
	done bool
}

func (s *terminalEOFStream) Read(p []byte) (int, error) {
	if s.done {
		return 0, io.EOF
	}
	s.done = true
	return copy(p, s.body), io.EOF
}

func (*terminalEOFStream) Close() error { return nil }

type failingStreamResponseWriter struct {
	header http.Header
	status int
}

func (w *failingStreamResponseWriter) Header() http.Header { return w.header }
func (w *failingStreamResponseWriter) WriteHeader(status int) {
	w.status = status
}
func (*failingStreamResponseWriter) Write([]byte) (int, error) {
	return 0, errors.New("downstream disconnected")
}

func TestStreamingExecutorAllowlistCoversActiveSSEEndpoints(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	tests := []struct {
		name       string
		endpoint   string
		body       string
		authHeader string
		authValue  string
		wantBody   string
		legacyBody string
	}{
		{
			name:       "chat completions",
			endpoint:   relayEndpointChatCompletions,
			body:       `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"ping"}],"stream":true}`,
			authHeader: "Authorization",
			authValue:  "Bearer user-token",
			wantBody:   "chat.completion.chunk",
			legacyBody: "chat.completion.chunk",
		},
		{
			name:       "responses",
			endpoint:   relayEndpointResponses,
			body:       `{"model":"gpt-4o-mini","input":"ping","stream":true}`,
			authHeader: "Authorization",
			authValue:  "Bearer user-token",
			wantBody:   "response.completed",
			legacyBody: "response.completed",
		},
		{
			name:       "anthropic messages",
			endpoint:   relayEndpointMessages,
			body:       `{"model":"gpt-4o-mini","max_tokens":16,"messages":[{"role":"user","content":"ping"}],"stream":true}`,
			authHeader: "x-api-key",
			authValue:  "user-token",
			wantBody:   "message_stop",
			legacyBody: "message_stop",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				if r.URL.Path == "/v1/responses" {
					_, _ = fmt.Fprint(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp-stream\",\"object\":\"response\",\"model\":\"gpt-4o-mini\",\"status\":\"in_progress\",\"output\":[]}}\n\n")
					_, _ = fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"pong\"}\n\n")
					_, _ = fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-stream\",\"object\":\"response\",\"model\":\"gpt-4o-mini\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":7,\"output_tokens\":5,\"total_tokens\":12}}}\n\n")
					_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
					return
				}
				if r.URL.Path != "/v1/chat/completions" {
					t.Errorf("upstream path = %q, want /v1/chat/completions or /v1/responses", r.URL.Path)
				}
				_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-stream\",\"object\":\"chat.completion.chunk\",\"created\":1710000000,\"model\":\"gpt-4o-mini\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"pong\"},\"finish_reason\":null}]}\n\n")
				_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-stream\",\"object\":\"chat.completion.chunk\",\"created\":1710000000,\"model\":\"gpt-4o-mini\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":5,\"total_tokens\":12}}\n\n")
				_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			defer upstream.Close()

			identityClient := rawIdentityClient{}
			channelClient := rawChannelClient{baseURL: upstream.URL + "/v1", key: "sk-upstream"}
			billingClient := &rawBillingClient{}
			logClient := &rawLogClient{}
			usecase := relaybiz.NewRelayUsecase(
				relaydata.NewIdentityAdapter(identityClient),
				relaydata.NewChannelAdapter(channelClient),
				nil,
				&relaybiz.RetryPolicy{MaxAttempts: 1},
			)
			httpServer := NewHTTPServer(identityClient, channelClient, billingClient, relayprovider.NewProviderFactory(time.Second), usecase, logClient)
			httpServer.SetRelayOrchestratorEnabled(true)
			httpServer.SetRelayOrchestratorTokenHMACAllowlist(relayOrchestratorTestHMACKey, []string{relayOrchestratorTestDigest("user-token")})
			srv := khttp.NewServer()
			httpServer.RegisterRoutes(srv)

			before := testutil.ToFloat64(metrics.RelayExecutorRequestsTotal.WithLabelValues(
				tt.endpoint, "true", relayExecutionPathOrchestrator, "200", "success",
			))
			quotaBefore := testutil.ToFloat64(metrics.RelayExecutorQuotaOutcomeTotal.WithLabelValues(
				tt.endpoint, "true", relayExecutionPathOrchestrator, "commit_success",
			))
			req := httptest.NewRequest(http.MethodPost, tt.endpoint, strings.NewReader(tt.body))
			req.Header.Set(tt.authHeader, tt.authValue)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Fatalf("stream body missing %q: %s", tt.wantBody, rec.Body.String())
			}
			if billingClient.commits != 1 || billingClient.releases != 0 {
				t.Fatalf("quota lifecycle = commits:%d releases:%d", billingClient.commits, billingClient.releases)
			}
			if len(billingClient.commitRequests) != 1 || billingClient.commitRequests[0].Endpoint != tt.endpoint || !billingClient.commitRequests[0].IsStream {
				t.Fatalf("stream quota attribution = %#v", billingClient.commitRequests)
			}
			if len(logClient.entries) != 1 || !logClient.entries[0].IsStream {
				t.Fatalf("stream usage logs = %#v", logClient.entries)
			}
			if got := testutil.ToFloat64(metrics.RelayExecutorRequestsTotal.WithLabelValues(
				tt.endpoint, "true", relayExecutionPathOrchestrator, "200", "success",
			)); got != before+1 {
				t.Fatalf("executor request metric = %v, want %v", got, before+1)
			}
			if got := testutil.ToFloat64(metrics.RelayExecutorQuotaOutcomeTotal.WithLabelValues(
				tt.endpoint, "true", relayExecutionPathOrchestrator, "commit_success",
			)); got != quotaBefore+1 {
				t.Fatalf("executor quota metric = %v, want %v", got, quotaBefore+1)
			}

			legacyBefore := testutil.ToFloat64(metrics.RelayExecutorRequestsTotal.WithLabelValues(
				tt.endpoint, "true", relayExecutionPathLegacy, "200", "success",
			))
			legacyReq := httptest.NewRequest(http.MethodPost, tt.endpoint, strings.NewReader(tt.body))
			legacyAuth := strings.Replace(tt.authValue, "user-token", "legacy-token", 1)
			legacyReq.Header.Set(tt.authHeader, legacyAuth)
			legacyReq.Header.Set("Content-Type", "application/json")
			legacyRec := httptest.NewRecorder()
			srv.ServeHTTP(legacyRec, legacyReq)
			if legacyRec.Code != rec.Code || !strings.Contains(legacyRec.Body.String(), tt.legacyBody) || !strings.Contains(legacyRec.Body.String(), "pong") {
				t.Fatalf("legacy/executor mismatch:\nlegacy status=%d body=%s\nexecutor status=%d body=%s", legacyRec.Code, legacyRec.Body.String(), rec.Code, rec.Body.String())
			}
			if got := testutil.ToFloat64(metrics.RelayExecutorRequestsTotal.WithLabelValues(
				tt.endpoint, "true", relayExecutionPathLegacy, "200", "success",
			)); got != legacyBefore+1 {
				t.Fatalf("legacy request metric = %v, want %v", got, legacyBefore+1)
			}
		})
	}
}

func TestResponsesWebSocketStaysLegacyAndObservable(t *testing.T) {
	httpServer := NewHTTPServer(nil, nil, nil, nil, nil)
	httpServer.SetRelayOrchestratorEnabled(true)
	httpServer.SetRelayOrchestratorTokenHMACAllowlist(relayOrchestratorTestHMACKey, []string{relayOrchestratorTestDigest("user-token")})

	invoke := func() int {
		req := httptest.NewRequest(http.MethodGet, relayEndpointResponses, nil)
		req.Header.Set("Authorization", "Bearer user-token")
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		rec := httptest.NewRecorder()
		httpServer.relayOrchestratorResponsesHandler(rec, req)
		return rec.Code
	}
	status := invoke()
	if status == http.StatusSwitchingProtocols {
		t.Fatal("test recorder unexpectedly completed a websocket upgrade")
	}
	before := testutil.ToFloat64(metrics.RelayExecutorRequestsTotal.WithLabelValues(
		relayEndpointResponses, "true", relayExecutionPathLegacy, fmt.Sprint(status), "error",
	))
	if gotStatus := invoke(); gotStatus != status {
		t.Fatalf("websocket status = %d, want stable %d", gotStatus, status)
	}
	if got := testutil.ToFloat64(metrics.RelayExecutorRequestsTotal.WithLabelValues(
		relayEndpointResponses, "true", relayExecutionPathLegacy, fmt.Sprint(status), "error",
	)); got != before+1 {
		t.Fatalf("websocket metric = %v, want %v", got, before+1)
	}
}

func TestStreamingExecutorReleasesQuotaWhenTerminalEventIsMissing(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"chatcmpl-truncated\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
	}))
	defer upstream.Close()

	identityClient := rawIdentityClient{}
	channelClient := rawChannelClient{baseURL: upstream.URL + "/v1", key: "sk-upstream"}
	billingClient := &rawBillingClient{}
	usecase := relaybiz.NewRelayUsecase(
		relaydata.NewIdentityAdapter(identityClient),
		relaydata.NewChannelAdapter(channelClient),
		nil,
		&relaybiz.RetryPolicy{MaxAttempts: 1},
	)
	httpServer := NewHTTPServer(identityClient, channelClient, billingClient, relayprovider.NewProviderFactory(time.Second), usecase)
	httpServer.SetRelayOrchestratorEnabled(true)
	httpServer.SetRelayOrchestratorTokenHMACAllowlist(relayOrchestratorTestHMACKey, []string{relayOrchestratorTestDigest("user-token")})
	srv := khttp.NewServer()
	httpServer.RegisterRoutes(srv)

	before := testutil.ToFloat64(metrics.RelayExecutorRequestsTotal.WithLabelValues(
		relayEndpointChatCompletions, "true", relayExecutionPathOrchestrator, "200", "stream_error",
	))
	req := httptest.NewRequest(http.MethodPost, relayEndpointChatCompletions, strings.NewReader(
		`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"ping"}],"stream":true}`,
	))
	req.Header.Set("Authorization", "Bearer user-token")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || billingClient.commits != 0 || billingClient.releases != 1 {
		t.Fatalf("truncated stream = status:%d commits:%d releases:%d body:%s", rec.Code, billingClient.commits, billingClient.releases, rec.Body.String())
	}
	if got := testutil.ToFloat64(metrics.RelayExecutorRequestsTotal.WithLabelValues(
		relayEndpointChatCompletions, "true", relayExecutionPathOrchestrator, "200", "stream_error",
	)); got != before+1 {
		t.Fatalf("stream error metric = %v, want %v", got, before+1)
	}
}

func TestOrchestratedStreamDoesNotCommitAfterDownstreamWriteFailure(t *testing.T) {
	completed := true
	stream := newFinalizingRelayStream(
		EndpointChatCompletions,
		&terminalEOFStream{body: []byte("data: [DONE]\n\n")},
		relaybiz.CanonicalUsage{},
		func(_ relaybiz.CanonicalUsage, _ string, success bool) error {
			completed = success
			return nil
		},
	)
	w := &failingStreamResponseWriter{header: make(http.Header)}
	writeOrchestratedRelayStream(w, relaybiz.ExecutionResponse{StatusCode: http.StatusOK, Stream: stream})
	if completed {
		t.Fatal("downstream write failure was treated as a completed stream")
	}
}

func TestStreamTerminalTrackerIgnoresTerminalWordsInModelOutput(t *testing.T) {
	tracker := streamTerminalTracker{endpoint: EndpointResponses}
	tracker.ObserveBytes([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"response.completed\"}\n\n"))
	if tracker.Success() {
		t.Fatal("model output text was mistaken for a response.completed event")
	}
}
