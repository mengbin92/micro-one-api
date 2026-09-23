package server

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	relaydata "micro-one-api/internal/data"
	"micro-one-api/platform/metrics"
)

func TestClientCancellationReachesUpstreamAndFinalizes(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	t.Setenv("RELAY_ROUTING_CONTEXT_V2", "false")
	for _, tc := range []struct {
		endpoint                     string
		http2, orchestrator, timeout bool
	}{
		{"/v1/messages", false, false, false},
		{"/v1/messages", true, false, false},
		{"/v1/messages", true, true, false},
		{"/v1/chat/completions", false, false, false},
		{"/v1/chat/completions", true, true, false},
		{"/v1/responses", false, false, false},
		{"/v1/responses", true, true, false},
		{"/v1/messages", true, false, true},
		{"/v1/chat/completions", false, false, true},
		{"/v1/responses", true, true, true},
	} {
		t.Run(fmt.Sprintf("%s/http2=%t/orchestrator=%t/timeout=%t", tc.endpoint, tc.http2, tc.orchestrator, tc.timeout), func(t *testing.T) {
			wantResult := "canceled"
			if tc.timeout {
				t.Setenv("RELAY_STREAM_TOTAL_TIMEOUT", "200ms")
				wantResult = "timeout"
			}
			upstreamCanceled := make(chan struct{})
			stopUpstream := make(chan struct{})
			var calls atomic.Int32
			upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				if (r.ProtoMajor == 2) != tc.http2 {
					t.Errorf("upstream protocol = %s, http2=%t", r.Proto, tc.http2)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if tc.endpoint == "/v1/responses" {
					fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n")
				} else if tc.endpoint == "/v1/chat/completions" && tc.orchestrator {
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
				} else {
					fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":4,\"output_tokens\":0}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"partial\"}}\n\n")
				}
				w.(http.Flusher).Flush()
				select {
				case <-r.Context().Done():
					close(upstreamCanceled)
				case <-stopUpstream:
				}
			}))
			upstream.EnableHTTP2 = tc.http2
			if tc.http2 {
				upstream.StartTLS()
			} else {
				upstream.Start()
			}
			defer upstream.Close()
			defer close(stopUpstream)

			identity := rawIdentityClient{}
			channel := rawChannelClient{baseURL: upstream.URL, chType: provider.ChannelTypeAnthropic, key: "test"}
			if tc.endpoint == "/v1/responses" || (tc.endpoint == "/v1/chat/completions" && tc.orchestrator) {
				channel.chType = provider.ChannelTypeOpenAI
			}
			billing := &rawBillingClient{failOnCanceledContext: true}
			recorder := &selectionTestRecorder{}
			uc := relaybiz.NewRelayUsecase(relaydata.NewIdentityAdapter(identity), relaydata.NewChannelAdapter(channel), nil, &relaybiz.RetryPolicy{MaxAttempts: 3})
			uc.SetSelectionRecorder(recorder)
			s := NewHTTPServer(identity, channel, billing, provider.NewProviderFactory(time.Second), uc)
			s.apiKeyStreamHTTPClient = upstream.Client()
			s.SetRelayOrchestratorEnabled(tc.orchestrator)
			s.SetRelayOrchestratorTokenHMACAllowlist(relayOrchestratorTestHMACKey, []string{relayOrchestratorTestDigest("user-token")})
			srv := khttp.NewServer()
			s.RegisterRoutes(srv)
			handlerDone := make(chan struct{})
			relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(handlerDone)
				srv.ServeHTTP(w, r)
			}))
			defer relay.Close()
			path := relayExecutionPathLegacy
			if tc.orchestrator {
				path = relayExecutionPathOrchestrator
			}
			counter := metrics.RelayExecutorRequestsTotal.WithLabelValues(tc.endpoint, "true", path, "200", wantResult)
			before := testutil.ToFloat64(counter)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, relay.URL+tc.endpoint, strings.NewReader(`{"model":"step-3.7-flash","input":"continue","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"continue"}]}`))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer user-token")
			req.Header.Set("Content-Type", "application/json")
			response, err := relay.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(response.Body)
				t.Fatalf("status=%d body=%s", response.StatusCode, body)
			}
			if _, err := bufio.NewReader(response.Body).ReadString('\n'); err != nil {
				t.Fatal(err)
			}
			if !tc.timeout {
				cancel()
			}
			for name, done := range map[string]<-chan struct{}{"upstream cancellation": upstreamCanceled, "relay completion": handlerDone} {
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					t.Fatalf("timed out waiting for %s", name)
				}
			}
			if calls.Load() != 1 || len(billing.reserveRequests) != 1 || billing.releases != 1 || billing.commits != 0 {
				t.Errorf("calls=%d reserves=%d releases=%d commits=%d", calls.Load(), len(billing.reserveRequests), billing.releases, billing.commits)
			}
			if got := testutil.ToFloat64(counter); got != before+1 {
				t.Errorf("%s observations=%v, want %v", wantResult, got, before+1)
			}
			finals := 0
			for _, event := range recorder.events {
				if !event.Planned {
					finals++
					if event.Result != wantResult {
						t.Errorf("routing result=%q, want %s", event.Result, wantResult)
					}
				}
			}
			if finals != 1 {
				t.Errorf("final selection events=%d, want 1", finals)
			}
		})
	}
}
