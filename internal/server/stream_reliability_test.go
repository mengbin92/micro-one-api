package server

import (
	"context"
	"errors"
	"fmt"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"io"
	provider "micro-one-api/domain/upstream/provider"
	relayadaptor "micro-one-api/internal/adaptor"
	relaybiz "micro-one-api/internal/biz"
	relaydata "micro-one-api/internal/data"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAdaptorStreamClientEnforcesIdleAfterDroppingTotalTimeout(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer upstream.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", upstream.URL, nil)
	client := streamHTTPClient(&http.Client{Timeout: 40 * time.Millisecond})
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, err = io.ReadAll(resp.Body)
	if !errors.Is(err, provider.ErrStreamIdleTimeout) {
		t.Fatalf("silent adaptor stream survived until caller deadline: %v", err)
	}
}

func TestRequestBudgetIsSharedAndStreamTotalIsOptional(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			t.Setenv("RELAY_REQUEST_TIMEOUT", "80ms")
			t.Setenv("RELAY_STREAM_TOTAL_TIMEOUT", "0s")
			s := NewHTTPServer(nil, nil, nil, provider.NewProviderFactory(time.Second), nil)
			handler := s.withRequestBudget(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := readRouteRequestBody(r)
				if err != nil {
					t.Fatal(err)
				}
				deadline, has := r.Context().Deadline()
				if stream {
					if has {
						t.Fatal("nonstream timeout truncated a stream")
					}
					return
				}
				if !has {
					t.Fatal("missing request budget")
				}
				applyRequestBudget(r, body)
				second, _ := r.Context().Deadline()
				if second != deadline {
					t.Fatal("budget was reset")
				}
				<-r.Context().Done()
				if !errors.Is(context.Cause(r.Context()), provider.ErrRequestBudget) {
					t.Fatal(context.Cause(r.Context()))
				}
			}))
			req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(fmt.Sprintf(`{"stream":%t}`, stream)))
			handler.ServeHTTP(httptest.NewRecorder(), req)
		})
	}
}

func TestIncompleteStreamsNeverCommitOrReplay(t *testing.T) {
	for _, orchestrator := range []bool{false, true} {
		for _, endpoint := range []string{"/v1/chat/completions", "/v1/responses"} {
			t.Run(fmt.Sprintf("orchestrator=%t%s", orchestrator, endpoint), func(t *testing.T) {
				t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
				calls := 0
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					w.Header().Set("Content-Type", "text/event-stream")
					if endpoint == "/v1/responses" {
						fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n")
					} else {
						fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
					}
				}))
				defer upstream.Close()
				identity, channel, billing := rawIdentityClient{}, rawChannelClient{baseURL: upstream.URL + "/v1", key: "secret"}, &rawBillingClient{}
				uc := relaybiz.NewRelayUsecase(relaydata.NewIdentityAdapter(identity), relaydata.NewChannelAdapter(channel), nil, &relaybiz.RetryPolicy{MaxAttempts: 3})
				recorder := &selectionTestRecorder{}
				uc.SetSelectionRecorder(recorder)
				s := NewHTTPServer(identity, channel, billing, provider.NewProviderFactory(time.Second), uc)
				s.SetRelayOrchestratorEnabled(orchestrator)
				s.SetRelayOrchestratorTokenHMACAllowlist(relayOrchestratorTestHMACKey, []string{relayOrchestratorTestDigest("user-token")})
				srv := khttp.NewServer()
				s.RegisterRoutes(srv)
				req := httptest.NewRequest("POST", endpoint, strings.NewReader(`{"model":"m","input":"ping","messages":[{"role":"user","content":"ping"}],"stream":true}`))
				req.Header.Set("Authorization", "Bearer user-token")
				rec := httptest.NewRecorder()
				srv.ServeHTTP(rec, req)
				if calls != 1 || billing.commits != 0 || billing.releases != 1 {
					t.Fatalf("calls=%d commits=%d releases=%d body=%s", calls, billing.commits, billing.releases, rec.Body.String())
				}
				if strings.Contains(rec.Body.String(), `"error"`) || strings.Contains(rec.Body.String(), "[DONE]") {
					t.Fatalf("incomplete stream appended a terminal response: %s", rec.Body.String())
				}
				if len(recorder.events) != 2 || recorder.events[1].Result != "error" {
					t.Fatalf("incomplete stream selection events=%+v", recorder.events)
				}
			})
		}
	}
}

func TestCanceledAdaptorStreamReleasesSlotAndClosesUpstream(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	closed := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(closed)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"r\",\"status\":\"in_progress\"}}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer upstream.Close()
	client := &adaptorFailoverChannelClient{}
	billing := &rawBillingClient{failOnCanceledContext: true}
	s := NewHTTPServer(nil, nil, billing, nil, relaybiz.NewRelayUsecase(adaptorFailoverIdentity{}, client, nil, nil))
	s.SetOAuthHTTPClient(upstream.Client())
	plan := stickyCodexPlan(42, 1)
	plan.Channel.BaseURL = upstream.URL
	plan.Account.BaseURL = upstream.URL
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := s.executeAndMeter(ctx, plan, "gpt-5", make(http.Header), []byte(`{"model":"gpt-5","messages":[{"role":"user","content":"hi"}],"stream":true}`), relayadaptor.FormatOpenAIChatCompletions, "")
	if result.err != nil {
		t.Fatal(result.err)
	}
	result.write(&cancelOnFirstWriteRecorder{ResponseRecorder: httptest.NewRecorder(), cancel: cancel})
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("upstream remained open")
	}
	release, ok := s.accountConcurrency.TryAcquire(context.Background(), 42, 1)
	if !ok {
		t.Fatal("slot leaked")
	}
	release()
	if billing.commits != 0 || billing.releases != 1 {
		t.Fatalf("commits=%d releases=%d", billing.commits, billing.releases)
	}
	if got := client.waitForSlotReports(t, 2); len(got) != 2 {
		t.Fatalf("slot reports=%v", got)
	}
}
