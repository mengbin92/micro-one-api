package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	relaydata "micro-one-api/internal/data"
	applogger "micro-one-api/platform/logging"

	khttp "github.com/go-kratos/kratos/v3/transport/http"
)

func TestHTTPServerResponsesUpstream4xxMatrix(t *testing.T) {
	tests := []struct {
		name           string
		upstreamStatus int
		wantStatus     int
		wantPaths      string
		wantResult     string
		wantCommits    int
		wantReleases   int
		wantCategory   string
	}{
		{name: "upstream_401", upstreamStatus: http.StatusUnauthorized, wantStatus: http.StatusBadGateway, wantPaths: "/v1/responses", wantResult: "client_error", wantReleases: 1, wantCategory: "upstream_auth"},
		{name: "upstream_403", upstreamStatus: http.StatusForbidden, wantStatus: http.StatusBadGateway, wantPaths: "/v1/responses", wantResult: "client_error", wantReleases: 1, wantCategory: "upstream_auth"},
		{name: "request_too_large", upstreamStatus: http.StatusRequestEntityTooLarge, wantStatus: http.StatusRequestEntityTooLarge, wantPaths: "/v1/responses", wantResult: "client_error", wantReleases: 1, wantCategory: "request_too_large"},
		{name: "unsupported_media_fallback", upstreamStatus: http.StatusUnsupportedMediaType, wantStatus: http.StatusOK, wantPaths: "/v1/responses,/v1/chat/completions", wantResult: "success", wantCommits: 1, wantCategory: "request_compatibility"},
		{name: "unprocessable_fallback", upstreamStatus: http.StatusUnprocessableEntity, wantStatus: http.StatusOK, wantPaths: "/v1/responses,/v1/chat/completions", wantResult: "success", wantCommits: 1, wantCategory: "request_compatibility"},
	}

	for _, stream := range []bool{false, true} {
		for _, tt := range tests {
			t.Run(fmt.Sprintf("%s/stream_%t", tt.name, stream), func(t *testing.T) {
				t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
				core, observed := observer.New(zap.DebugLevel)
				applogger.SetLoggerForTest(t, zap.New(core))

				var gotPaths []string
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					gotPaths = append(gotPaths, r.URL.Path)
					if r.URL.Path == "/v1/responses" {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(tt.upstreamStatus)
						_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","code":"invalid_payload","param":"input","message":"opaque-secret-123"}}`))
						return
					}
					if r.URL.Path != "/v1/chat/completions" {
						http.NotFound(w, r)
						return
					}
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = w.Write([]byte(`data: {"id":"chatcmpl_matrix","object":"chat.completion.chunk","created":1710000000,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{"content":"pong"},"finish_reason":null}]}` + "\n\n"))
						_, _ = w.Write([]byte(`data: {"id":"chatcmpl_matrix","object":"chat.completion.chunk","created":1710000000,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}` + "\n\n"))
						_, _ = w.Write([]byte("data: [DONE]\n\n"))
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"id":"chatcmpl_matrix","object":"chat.completion","created":1710000000,"model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`))
				}))
				defer upstream.Close()

				identityClient := rawIdentityClient{}
				channelClient := rawChannelClient{baseURL: upstream.URL + "/v1", key: "sk-upstream"}
				billingClient := &rawBillingClient{}
				recorder := &selectionTestRecorder{}
				relayUsecase := relaybiz.NewRelayUsecase(
					relaydata.NewIdentityAdapter(identityClient),
					relaydata.NewChannelAdapter(channelClient),
					nil,
					&relaybiz.RetryPolicy{MaxAttempts: 1, RetryableStatus: map[int]bool{}},
				)
				relayUsecase.SetSelectionRecorder(recorder)
				httpServer := NewHTTPServer(identityClient, channelClient, billingClient, relayprovider.NewProviderFactory(time.Second), relayUsecase, &rawLogClient{})
				srv := khttp.NewServer()
				httpServer.RegisterRoutes(srv)

				body := `{"model":"gpt-4o-mini","input":"ping"}`
				if stream {
					body = `{"model":"gpt-4o-mini","input":"ping","stream":true}`
				}
				req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
				req.Header.Set("Authorization", "Bearer user-token")
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()

				srv.ServeHTTP(rec, req)

				if rec.Code != tt.wantStatus {
					t.Fatalf("status = %d, want %d, body=%s", rec.Code, tt.wantStatus, rec.Body.String())
				}
				if got := strings.Join(gotPaths, ","); got != tt.wantPaths {
					t.Fatalf("upstream paths = %q, want %q", got, tt.wantPaths)
				}
				if billingClient.commits != tt.wantCommits || billingClient.releases != tt.wantReleases {
					t.Fatalf("billing commits=%d releases=%d, want %d/%d", billingClient.commits, billingClient.releases, tt.wantCommits, tt.wantReleases)
				}
				if len(recorder.events) == 0 || recorder.events[len(recorder.events)-1].Result != tt.wantResult {
					t.Fatalf("selection events = %#v, want terminal result %q", recorder.events, tt.wantResult)
				}

				entries := observed.FilterMessage("responses upstream request failed").All()
				if len(entries) == 0 {
					t.Fatal("missing responses upstream failure log")
				}
				fields := entries[0].ContextMap()
				if fields["upstream_status"] != int64(tt.upstreamStatus) || fields["endpoint"] != "/v1/responses" || fields["stream"] != stream || fields["execution_path"] != relayExecutionPathLegacy || fields["phase"] != "native" || fields["error_category"] != tt.wantCategory {
					t.Fatalf("log fields = %#v", fields)
				}
				if tt.wantStatus != http.StatusOK {
					if len(entries) != 2 || entries[1].ContextMap()["phase"] != "terminal" {
						t.Fatalf("terminal failure log missing: %#v", entries)
					}
				}
				for _, entry := range observed.All() {
					if strings.Contains(fmt.Sprint(entry.ContextMap()), "opaque-secret-123") {
						t.Fatalf("upstream error body leaked into log: %#v", entry.ContextMap())
					}
				}
			})
		}
	}
}

func TestWriteResponsesUpstreamErrorPreservesSanitizedClientMetadata(t *testing.T) {
	core, _ := observer.New(zap.DebugLevel)
	applogger.SetLoggerForTest(t, zap.New(core))
	s := &HTTPServer{}
	rec := httptest.NewRecorder()
	err := &relayprovider.UpstreamHTTPError{
		StatusCode: http.StatusUnprocessableEntity,
		Body:       []byte(`{"error":{"type":"invalid_request_error","code":"invalid_function_parameters","param":"input[8].tools[1].parameters","message":"invalid api_key=secret123"}}`),
	}

	s.writeResponsesUpstreamError(rec, err)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`"type":"invalid_request_error"`, `"code":"invalid_function_parameters"`, `"param":"input[8].tools[1].parameters"`, `"message":"invalid api_key:\"***REDACTED***\""`} {
		if !strings.Contains(body, want) {
			t.Fatalf("response missing %s: %s", want, body)
		}
	}
	if strings.Contains(body, "secret123") {
		t.Fatalf("response leaked sensitive upstream detail: %s", body)
	}
}
