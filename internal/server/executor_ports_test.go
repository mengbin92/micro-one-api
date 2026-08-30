package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
)

type portLifecycleHooks struct {
	reservePlan *relaybiz.RelayPlan
	reserveReq  *RelayRequest
	committed   *Reservation
	logged      relaybiz.CanonicalUsage
	loggedReq   *RelayRequest
	released    *Reservation
}

func (h *portLifecycleHooks) ReserveQuota(_ context.Context, plan *relaybiz.RelayPlan, req *RelayRequest, _ relaybiz.CanonicalUsage) (*Reservation, error) {
	h.reservePlan = plan
	h.reserveReq = req
	return &Reservation{ID: "reservation-port"}, nil
}

func (h *portLifecycleHooks) CommitQuota(_ context.Context, _ *relaybiz.RelayPlan, _ *RelayRequest, reservation *Reservation, _ relaybiz.CanonicalUsage, _ bool, _ time.Duration) error {
	h.committed = reservation
	return nil
}

func (h *portLifecycleHooks) ReleaseQuota(_ context.Context, reservation *Reservation, _ string) error {
	h.released = reservation
	return nil
}

func (h *portLifecycleHooks) LogUsage(_ context.Context, _ *relaybiz.RelayPlan, req *RelayRequest, usage relaybiz.CanonicalUsage, _ time.Duration, _ bool) {
	h.loggedReq = req
	h.logged = usage
}

func TestRelayQuotaAndEventPortsAdaptTransportNeutralRequest(t *testing.T) {
	hooks := &portLifecycleHooks{}
	quota := relayQuotaPort{hooks: hooks}
	logger := relayEventLogger{hooks: hooks}
	plan := &relaybiz.RelayPlan{Channel: &relaybiz.Channel{ID: 7}}
	req := relaybiz.ExecutorRequest{
		Token:     "token",
		Model:     "gpt-4o-mini",
		Endpoint:  "chat/completions",
		Body:      []byte(`{"messages":[]}`),
		Headers:   map[string][]string{"Authorization": []string{"Bearer token"}},
		RequestID: "request-port",
	}
	estimated := relaybiz.CanonicalUsage{TotalTokens: 3}
	reservation, err := quota.Reserve(context.Background(), plan, req, estimated)
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	if reservation == nil || reservation.ID != "reservation-port" {
		t.Fatalf("reservation = %#v", reservation)
	}
	if hooks.reservePlan != plan || hooks.reserveReq == nil {
		t.Fatalf("reserve adaptation lost plan/request: plan=%p req=%#v", hooks.reservePlan, hooks.reserveReq)
	}
	body, err := io.ReadAll(hooks.reserveReq.Body)
	if err != nil {
		t.Fatalf("read adapted body: %v", err)
	}
	if string(body) != string(req.Body) || hooks.reserveReq.RequestID != req.RequestID || hooks.reserveReq.Endpoint != EndpointChatCompletions {
		t.Fatalf("adapted request = body:%q request_id:%q endpoint:%q", body, hooks.reserveReq.RequestID, hooks.reserveReq.Endpoint)
	}
	if !reflect.DeepEqual(hooks.reserveReq.Headers, http.Header{"Authorization": []string{"Bearer token"}}) {
		t.Fatalf("adapted headers = %#v", hooks.reserveReq.Headers)
	}
	if err := quota.Commit(context.Background(), plan, req, reservation, estimated, true, time.Second); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if hooks.committed == nil || hooks.committed.ID != reservation.ID {
		t.Fatalf("committed reservation = %#v", hooks.committed)
	}
	if err := quota.Release(context.Background(), reservation, "test"); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if hooks.released == nil || hooks.released.ID != reservation.ID {
		t.Fatalf("released reservation = %#v", hooks.released)
	}
	logger.LogUsage(context.Background(), plan, relaybiz.UsageEvent{
		Model:     req.Model,
		Endpoint:  req.Endpoint,
		RequestID: req.RequestID,
	}, estimated, time.Second, false)
	if hooks.logged != estimated {
		t.Fatalf("logged usage = %#v, want %#v", hooks.logged, estimated)
	}
	var loggedBody []byte
	if hooks.loggedReq != nil && hooks.loggedReq.Body != nil {
		loggedBody, err = io.ReadAll(hooks.loggedReq.Body)
		if err != nil {
			t.Fatalf("read logged request body: %v", err)
		}
	}
	if hooks.loggedReq == nil || hooks.loggedReq.Token != "" || len(loggedBody) != 0 || len(hooks.loggedReq.Headers) != 0 {
		t.Fatalf("logged request carried sensitive data: %#v", hooks.loggedReq)
	}
	if hooks.loggedReq.Model != req.Model || hooks.loggedReq.RequestID != req.RequestID {
		t.Fatalf("logged request lost metadata: %#v", hooks.loggedReq)
	}
}

func TestHeaderMapToHTTPCopiesValues(t *testing.T) {
	values := []string{"one", "two"}
	source := map[string][]string{"X-Test": values}
	converted := headerMapToHTTP(source)
	values[0] = "changed"
	if converted.Get("X-Test") != "one" {
		t.Fatalf("converted header changed with source mutation: %q", converted.Get("X-Test"))
	}
	convertedValues := converted.Values("X-Test")
	convertedValues[0] = "changed-again"
	if source["X-Test"][0] != "changed" {
		t.Fatalf("source header changed with converted mutation: %q", source["X-Test"][0])
	}
}

func TestRelayExecutorHeadersUsesAllowlistAndCopiesValues(t *testing.T) {
	headers := http.Header{
		"Authorization":       []string{"Bearer secret"},
		"Content-Type":        []string{"application/json"},
		"Cookie":              []string{"session=secret"},
		"OpenAI-Organization": []string{"org-1"},
		"X-Request-ID":        []string{"request-1"},
	}
	got := relayExecutorHeaders(headers)
	if !reflect.DeepEqual(got, map[string][]string{
		"Content-Type":        []string{"application/json"},
		"OpenAI-Organization": []string{"org-1"},
		"X-Request-ID":        []string{"request-1"},
	}) {
		t.Fatalf("relayExecutorHeaders() = %#v", got)
	}
	headers["Content-Type"][0] = "text/plain"
	if got["Content-Type"][0] != "application/json" {
		t.Fatalf("allowlisted values share source storage: %#v", got)
	}
}

func TestRelayExecutorAdapterReturnsTransportNeutralResponse(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3},"choices":[]}`))
	}))
	defer upstream.Close()

	usecase := relaybiz.NewRelayUsecase(
		orchestratorIdentityClient{},
		orchestratorChannelClient{baseURL: upstream.URL + "/v1"},
		nil,
		nil,
	)
	executor := NewRelayExecutorWithDependencies(usecase, relayprovider.NewProviderFactory(time.Second), nil, nil)
	request := relaybiz.ExecutorRequest{
		Token:    "client-token",
		Model:    "gpt-4o-mini",
		Endpoint: "chat/completions",
		Body:     []byte(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`),
		Headers:  map[string][]string{"Authorization": []string{"Bearer client-token"}},
	}
	response, err := executor.Execute(context.Background(), request)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if response.StatusCode != http.StatusAccepted || !strings.HasPrefix(response.RequestID, "req_") {
		t.Fatalf("response metadata = %#v", response)
	}
	if response.ChannelID != 11 || response.Headers["Content-Type"][0] != "application/json" {
		t.Fatalf("response route/headers = channel:%d headers:%#v", response.ChannelID, response.Headers)
	}
	if !strings.Contains(string(response.Body), `"total_tokens":3`) {
		t.Fatalf("response body = %s", response.Body)
	}
}

func TestRelayExecutorAdapterReturnsTransportNeutralStream(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":2,\"total_tokens\":3}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	usecase := relaybiz.NewRelayUsecase(
		orchestratorIdentityClient{},
		orchestratorChannelClient{baseURL: upstream.URL + "/v1"},
		nil,
		nil,
	)
	executor := NewRelayExecutorWithDependencies(usecase, relayprovider.NewProviderFactory(time.Second), nil, nil)
	response, err := executor.Execute(context.Background(), relaybiz.ExecutorRequest{
		Token:    "client-token",
		Model:    "gpt-4o-mini",
		Endpoint: "chat/completions",
		Body:     []byte(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}],"stream":true}`),
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if response.Stream == nil || len(response.Body) != 0 {
		t.Fatalf("stream response = stream:%#v body:%q", response.Stream, response.Body)
	}
	body, err := io.ReadAll(response.Stream)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	if err := response.Stream.Close(); err != nil {
		t.Fatalf("close stream: %v", err)
	}
	if !strings.Contains(string(body), `"total_tokens":3`) {
		t.Fatalf("stream body = %s", body)
	}
}
