package server

import (
	"context"
	"fmt"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"github.com/prometheus/client_golang/prometheus/testutil"
	relayprovider "micro-one-api/domain/upstream/provider"
	"micro-one-api/internal/apicompat"
	relaybiz "micro-one-api/internal/biz"
	relaydata "micro-one-api/internal/data"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/metrics"
	appmiddleware "micro-one-api/platform/middleware"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestUnknownV1RouteUses501Contract(t *testing.T) {
	s := NewHTTPServer(nil, nil, nil, nil, nil)
	srv := khttp.NewServer()
	s.RegisterRoutes(srv)
	for _, tc := range []struct {
		path   string
		status int
	}{{"/v1/new_endpoint", 501}, {"/v1/new_endpoint/arbitrary-id", 501}, {"/v1/chat/completions", 405}, {"/healthz", 200}, {"/api/unknown", 404}} {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest("GET", tc.path, nil))
		if rec.Code != tc.status {
			t.Errorf("%s = %d, want %d", tc.path, rec.Code, tc.status)
		}
	}
}
func TestResponsesProtocolFallbackDisabled(t *testing.T) {
	for _, status := range []int{400, 404, 405, 415, 422, 501, 502, 503} {
		if shouldFallbackResponsesToChat("/responses", []byte(`{"model":"m","input":"ping"}`), &relayprovider.UpstreamHTTPError{StatusCode: status}) {
			t.Errorf("status %d silently enables Chat substitution", status)
		}
	}
}
func TestResponsesAnthropicMismatchMakesNoUpstreamCall(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) }))
	defer upstream.Close()
	s := NewHTTPServer(nil, nil, nil, relayprovider.NewProviderFactory(time.Second), nil)
	_, err := s.forwardResponsesViaAnthropicFallback(context.Background(), &relaybiz.Channel{Type: relayprovider.ChannelTypeAnthropic, BaseURL: upstream.URL}, nil, []byte(`{"model":"m","input":"ping"}`))
	if err == nil || calls != 0 {
		t.Fatalf("mismatch: err=%v upstream calls=%d", err, calls)
	}
}

// Both execution paths must release their reservation and stop on endpoint
// capability errors even with another candidate and a permissive retry policy.
func TestResponsesCapabilityFailureNeverSubstitutesProtocol(t *testing.T) {
	for _, orchestrator := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			for _, code := range []int{404, 405, 501} {
				t.Run(fmt.Sprintf("orchestrator=%t/stream=%t/status=%d", orchestrator, stream, code), func(t *testing.T) {
					t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
					calls := 0
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls++
						if r.URL.Path != "/v1/responses" {
							t.Errorf("protocol changed to %s", r.URL.Path)
						}
						w.WriteHeader(code)
					}))
					defer upstream.Close()
					identity, channel, billing := rawIdentityClient{}, rawChannelClient{baseURL: upstream.URL + "/v1", key: "secret"}, &rawBillingClient{}
					uc := relaybiz.NewRelayUsecase(relaydata.NewIdentityAdapter(identity), relaydata.NewChannelAdapter(channel), nil, &relaybiz.RetryPolicy{MaxAttempts: 3, RetryableStatus: map[int]bool{404: true, 405: true, 501: true}})
					s := NewHTTPServer(identity, channel, billing, relayprovider.NewProviderFactory(time.Second), uc)
					s.SetRelayOrchestratorEnabled(orchestrator)
					s.SetRelayOrchestratorTokenHMACAllowlist(relayOrchestratorTestHMACKey, []string{relayOrchestratorTestDigest("user-token")})
					srv := khttp.NewServer()
					s.RegisterRoutes(srv)
					req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(fmt.Sprintf(`{"model":"m","input":"ping","stream":%t}`, stream)))
					req.Header.Set("Authorization", "Bearer user-token")
					rec := httptest.NewRecorder()
					srv.ServeHTTP(rec, req)
					if rec.Header().Get("X-Source-Kind") != "channel" || rec.Header().Get("X-Source-ID") == "" || rec.Header().Get("X-Request-ID") == "" {
						t.Fatalf("missing error identity: %v", rec.Header())
					}
					if rec.Code != 501 || calls != 1 || billing.commits != 0 || billing.releases != 1 {
						t.Fatalf("status=%d calls=%d commits=%d releases=%d body=%s", rec.Code, calls, billing.commits, billing.releases, rec.Body.String())
					}
				})
			}
		}
	}
}

func TestUnknownV1RouteMetricsAndIdentityRemainBounded(t *testing.T) {
	s := NewHTTPServer(nil, nil, nil, nil, nil)
	s.UseRouteMiddleware(appmiddleware.NewHTTPMetricsMiddleware("relay-contract-test"), appmiddleware.CORS(&appmiddleware.CORSConfig{AllowedOrigins: []string{"https://client.test"}, AllowedMethods: []string{"GET", "POST", "OPTIONS"}, AllowedHeaders: []string{"Authorization"}}))
	srv := khttp.NewServer()
	s.RegisterRoutes(srv)
	before := testutil.ToFloat64(metrics.HTTPRequestTotal.WithLabelValues("relay-contract-test", "GET", "/v1/unknown", "501"))
	for _, path := range []string{"/v1/arbitrary-one", "/v1/arbitrary-two"} {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		var body struct {
			Error map[string]any `json:"error"`
		}
		if err := jsonx.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		id := rec.Header().Get("X-Request-ID")
		if id == "" || body.Error["request_id"] != id {
			t.Fatalf("missing root identity: %s", rec.Body.String())
		}
		if _, ok := body.Error["source_id"]; ok {
			t.Fatal("unknown route fabricated source")
		}
	}
	if got := testutil.ToFloat64(metrics.HTTPRequestTotal.WithLabelValues("relay-contract-test", "GET", "/v1/unknown", "501")); got != before+2 {
		t.Fatalf("unknown route label=%v want=%v", got, before+2)
	}
	req := httptest.NewRequest("OPTIONS", "/v1/arbitrary-one", nil)
	req.Header.Set("Origin", "https://client.test")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 204 || rec.Header().Get("Access-Control-Allow-Origin") != "https://client.test" {
		t.Fatalf("preflight status=%d headers=%v", rec.Code, rec.Header())
	}
}

func TestMessagesCommitFailureIsNeverReplayable(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, `{"id":"m","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":4,"output_tokens":5}}`)
	}))
	defer upstream.Close()
	billing := &rawBillingClient{commitMessage: "billing timeout"}
	s := NewHTTPServer(nil, nil, billing, relayprovider.NewProviderFactory(time.Second), nil)
	plan := &relaybiz.RelayPlan{Auth: &relaybiz.AuthSnapshot{UserID: 42, TokenID: 1}, Channel: &relaybiz.Channel{ID: 1, Type: relayprovider.ChannelTypeAnthropic, BaseURL: upstream.URL, Key: "secret"}}
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"max_tokens":8}`)
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(string(body)))
	var parsed apicompat.AnthropicRequest
	if err := jsonx.Unmarshal(body, &parsed); err != nil {
		t.Fatal(err)
	}
	err := s.executeAnthropicChannelAttempt(context.Background(), httptest.NewRecorder(), req, plan, plan.Channel, &parsed, body, "m", "m")
	if calls != 1 || !relaybiz.IsPostForwardError(err) {
		t.Fatalf("commit failure replayable: calls=%d err=%v", calls, err)
	}
}
