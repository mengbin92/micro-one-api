package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"micro-one-api/domain/requesttrace"
	"micro-one-api/domain/upstream/evidence"
	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
)

type observingRepo struct {
	mu     sync.Mutex
	events []evidence.SendEvent
}

func (r *observingRepo) Put(_ context.Context, e evidence.SendEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
	return nil
}
func (r *observingRepo) Pending(context.Context, int) ([]relaybiz.PendingObservation, error) {
	return nil, nil
}
func (r *observingRepo) Acknowledge(context.Context, string, string) error { return nil }
func (r *observingRepo) Defer(context.Context, string, time.Time) error    { return nil }
func (r *observingRepo) Stats(context.Context) (relaybiz.ObservationOutboxStats, error) {
	return relaybiz.ObservationOutboxStats{}, nil
}
func (r *observingRepo) snapshot() []evidence.SendEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]evidence.SendEvent(nil), r.events...)
}

// Production currently runs an empty native-settlement allowlist. A user send
// must still leave observation evidence in that configuration.
func TestUsageObservationIsIndependentOfFinancialAllowlist(t *testing.T) {
	t.Setenv("RELAY_USAGE_OBSERVATION_V1", "true")
	t.Setenv("RELAY_EXECUTION_DELIVERY_V1", "false")
	t.Setenv("RELAY_USAGE_SHADOW", "false")
	t.Setenv("RELAY_NATIVE_USAGE_V2_ALLOWLIST", "")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-9","usage":{"prompt_tokens":7,"completion_tokens":1,"total_tokens":8},"choices":[{"finish_reason":"stop"}]}`)
	}))
	defer upstream.Close()

	repo := &observingRepo{}
	s := &HTTPServer{}
	s.SetObservationDelivery(relaybiz.NewObservationDelivery(repo, nil))
	ctx := s.observeNativeUsage(context.Background(), &relaybiz.Channel{ID: 4}, "req-independent", "exact-model")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstream.URL+"/v1/chat/completions", strings.NewReader(`{}`))
	require.NoError(t, err)
	resp, err := evidence.Do(upstream.Client(), req, "openai_chat")
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Contains(t, string(body), "prompt_tokens", "observation must not change the client response")

	events := repo.snapshot()
	require.Len(t, events, 2, "started and one terminal are expected")
	require.Equal(t, "started", events[0].Category)
	require.Equal(t, "terminal", events[1].Category)
	require.Equal(t, evidence.CaptureComplete, events[1].Capture)
	require.Equal(t, "verified", events[1].Record.Observation.Status)
	require.Equal(t, evidence.PurposeUser, events[1].Send.Purpose)
	require.Equal(t, "channel", events[1].Send.SourceKind)
}

func TestUsageObservationDisabledLeavesNoSink(t *testing.T) {
	t.Setenv("RELAY_USAGE_OBSERVATION_V1", "false")
	repo := &observingRepo{}
	s := &HTTPServer{}
	s.SetObservationDelivery(relaybiz.NewObservationDelivery(repo, nil))
	ctx := s.observeNativeUsage(context.Background(), &relaybiz.Channel{ID: 4}, "req-off", "exact-model")
	require.Nil(t, evidence.ObservationSinkFrom(ctx), "the disabled producer must not attach a sink")
	require.Empty(t, repo.snapshot())
}

func TestUsageObservationRecordsEachWSTurnOnce(t *testing.T) {
	t.Setenv("RELAY_USAGE_OBSERVATION_V1", "true")
	client := newFakeFrameConn(16)
	upstream := newFakeFrameConn(16)
	first := []byte(`{"type":"response.completed","response":{"id":"resp1","usage":{"input_tokens":10,"output_tokens":2}}}`)
	second := []byte(`{"type":"response.completed","response":{"id":"resp2","usage":{"input_tokens":20,"output_tokens":3}}}`)
	upstream.in <- fakeFrame{msgType: coderws.MessageText, payload: first}

	repo := &observingRepo{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = relaybiz.WithObservationDelivery(ctx, relaybiz.NewObservationDelivery(repo, nil))
	ctx = relaybiz.WithUsageObservation(ctx, &relaybiz.Channel{ID: 2}, "ws-req", "exact-model")

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = relayOpenAIWSFrames(ctx, client, upstream, []byte(`{"type":"response.create"}`), openAIWSRelayOptions{writeTimeout: time.Second, idleTimeout: time.Second})
	}()

	waitFrame := func() {
		select {
		case <-client.out:
		case <-time.After(2 * time.Second):
			t.Fatal("client did not receive the relayed turn")
		}
	}
	waitFrame()
	// Second client turn: a fresh response.create opens its own observation.
	client.in <- fakeFrame{msgType: coderws.MessageText, payload: []byte(`{"type":"response.create"}`)}
	upstream.in <- fakeFrame{msgType: coderws.MessageText, payload: second}
	waitFrame()
	close(upstream.in)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("relay did not stop")
	}

	events := repo.snapshot()
	var terminals []evidence.SendEvent
	starts := 0
	for _, event := range events {
		switch event.Category {
		case "started":
			starts++
		case "terminal":
			terminals = append(terminals, event)
		}
	}
	require.Equal(t, 2, starts, "each client turn is announced once")
	require.Len(t, terminals, 2, "each turn has exactly one terminal")
	require.NotEqual(t, terminals[0].Send.SendID, terminals[1].Send.SendID, "WS turns must not share a send")
	require.NotEqual(t, terminals[0].Send.ObservationID, terminals[1].Send.ObservationID, "later turns need their own observation identity")
	require.NotEqual(t, terminals[0].Send.RequestID, terminals[1].Send.RequestID, "later turns need their own request identity")
}

func TestUsageObservationUnmatchedResponseDoesNotFabricateSend(t *testing.T) {
	t.Setenv("RELAY_USAGE_OBSERVATION_V1", "true")
	client := newFakeFrameConn(8)
	upstream := newFakeFrameConn(8)
	first := []byte(`{"type":"response.completed","response":{"id":"resp1","usage":{"input_tokens":10,"output_tokens":2}}}`)
	stranger := []byte(`{"type":"response.completed","response":{"id":"resp-unknown","usage":{"input_tokens":1,"output_tokens":1}}}`)
	upstream.in <- fakeFrame{msgType: coderws.MessageText, payload: first}
	upstream.in <- fakeFrame{msgType: coderws.MessageText, payload: stranger}
	close(upstream.in)
	go func() {
		for range client.out {
		}
	}()
	repo := &observingRepo{}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ctx = relaybiz.WithObservationDelivery(ctx, relaybiz.NewObservationDelivery(repo, nil))
	ctx = relaybiz.WithUsageObservation(ctx, &relaybiz.Channel{ID: 2}, "ws-orphan", "exact-model")
	_, _ = relayOpenAIWSFrames(ctx, client, upstream, []byte(`{"type":"response.create"}`), openAIWSRelayOptions{writeTimeout: time.Second, idleTimeout: time.Second})

	events := repo.snapshot()
	byCategory := map[string][]evidence.SendEvent{}
	for _, event := range events {
		byCategory[event.Category] = append(byCategory[event.Category], event)
	}
	require.Len(t, byCategory["started"], 1)
	require.Len(t, byCategory["terminal"], 1)
	require.Len(t, byCategory["unmatched"], 1, "a second provider response on a closed send is an association anomaly")
	require.Empty(t, byCategory["unmatched"][0].Send.SendID, "unmatched evidence must not invent a send id")
	require.Len(t, byCategory["unmatched"], 1)
}

type writeFailConn struct{ readCtx chan struct{} }

func (c *writeFailConn) ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error) {
	select {
	case <-ctx.Done():
		return coderws.MessageText, nil, ctx.Err()
	case <-c.readCtx:
		return coderws.MessageText, nil, errors.New("closed")
	}
}
func (c *writeFailConn) WriteFrame(context.Context, coderws.MessageType, []byte) error {
	return errors.New("upstream write failed")
}
func (c *writeFailConn) Close() error { return nil }

func TestUsageObservationWSFirstWriteFailureLeavesTerminal(t *testing.T) {
	t.Setenv("RELAY_USAGE_OBSERVATION_V1", "true")
	client := newFakeFrameConn(8)
	repo := &observingRepo{}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ctx = relaybiz.WithObservationDelivery(ctx, relaybiz.NewObservationDelivery(repo, nil))
	ctx = relaybiz.WithUsageObservation(ctx, &relaybiz.Channel{ID: 2}, "ws-write-fail", "exact-model")
	_, exit := relayOpenAIWSFrames(ctx, client, &writeFailConn{readCtx: make(chan struct{})}, []byte(`{"type":"response.create"}`), openAIWSRelayOptions{writeTimeout: time.Second, idleTimeout: time.Second})
	require.NotNil(t, exit)

	events := repo.snapshot()
	var categories []string
	for _, event := range events {
		categories = append(categories, event.Category)
	}
	require.Equal(t, []string{"started", "terminal"}, categories)
	require.Equal(t, evidence.CaptureTransportError, events[1].Capture, "a failed supplier write is a transport error, not silence")
	require.Equal(t, "transport_error", events[1].Reason)
}

// R03/R04: the real orchestrator path (lifecycle hook -> reservation context)
// must carry the observation delivery, and evidence ownership must come from
// the verified auth snapshot even when request timing is disabled.
func TestUsageObservationOrchestratorPathEnablesDeliveryAndVerifiedUser(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	t.Setenv("RELAY_USAGE_OBSERVATION_V1", "true")
	t.Setenv("RELAY_REQUEST_TIMING_ENABLED", "false")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6},"choices":[{"finish_reason":"stop"}]}`)
	}))
	defer upstream.Close()

	repo := &observingRepo{}
	s := &HTTPServer{billingClient: &rawBillingClient{}, observationDelivery: relaybiz.NewObservationDelivery(repo, nil)}
	usecase := relaybiz.NewRelayUsecase(orchestratorIdentityClient{}, orchestratorChannelClient{baseURL: upstream.URL + "/v1"}, nil, &relaybiz.RetryPolicy{MaxAttempts: 1})
	orchestrator := NewRelayOrchestratorWithDependencies(usecase, relayprovider.NewProviderFactory(time.Second), httpRelayLifecycleHooks{s: s}, nil)

	ctx := requesttrace.WithVerifiedUserHolder(context.Background())
	result, err := orchestrator.Execute(ctx, &RelayRequest{
		Token: "client-token", Model: "gpt-4o-mini", Endpoint: EndpointChatCompletions,
		Body:    strings.NewReader(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`),
		Headers: http.Header{"Authorization": []string{"Bearer client-token"}}, RequestID: "request-observed",
	})
	require.NoError(t, err)
	_, _ = io.ReadAll(result.Response)
	_ = result.Response.Close()

	events := repo.snapshot()
	require.Len(t, events, 2, "the orchestrator path must produce started+terminal")
	terminal := events[1]
	require.Equal(t, "terminal", terminal.Category)
	require.Equal(t, evidence.CaptureComplete, terminal.Capture)
	require.EqualValues(t, 1, terminal.Send.UserID, "verified identity must survive RELAY_REQUEST_TIMING_ENABLED=false")
	require.NotEmpty(t, terminal.Send.RequestID)
}

// R06: a send that was written but whose upstream closes before any frame or
// usage must still get one explicit failed terminal. Silence is not an outcome.
func TestUsageObservationWSUpstreamCloseBeforeFirstFrameLeavesTerminal(t *testing.T) {
	t.Setenv("RELAY_USAGE_OBSERVATION_V1", "true")
	client := newFakeFrameConn(8)
	upstream := newFakeFrameConn(8)
	close(upstream.in)
	go func() {
		for range client.out {
		}
	}()

	repo := &observingRepo{}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ctx = relaybiz.WithObservationDelivery(ctx, relaybiz.NewObservationDelivery(repo, nil))
	ctx = relaybiz.WithUsageObservation(ctx, &relaybiz.Channel{ID: 2}, "ws-close-early", "exact-model")
	_, exit := relayOpenAIWSFrames(ctx, client, upstream, []byte(`{"type":"response.create"}`), openAIWSRelayOptions{writeTimeout: time.Second, idleTimeout: time.Second})
	require.NotNil(t, exit)

	events := repo.snapshot()
	require.Len(t, events, 2, "a written send must end with one terminal")
	require.Equal(t, "started", events[0].Category)
	require.Equal(t, "terminal", events[1].Category)
	require.Equal(t, evidence.CaptureTruncated, events[1].Capture)
	require.Equal(t, "truncated", events[1].Reason)
}

func TestUsageObservationWSCompleteInvalidUsageKeepsFinancialVerdict(t *testing.T) {
	t.Setenv("RELAY_USAGE_OBSERVATION_V1", "true")
	client := newFakeFrameConn(4)
	upstream := newFakeFrameConn(4)
	upstream.in <- fakeFrame{msgType: coderws.MessageText, payload: []byte(`{"type":"response.completed","response":{"id":"resp-invalid","usage":{"input_tokens":"invalid","output_tokens":2}}}`)}
	close(upstream.in)
	go func() {
		for range client.out {
		}
	}()
	repo := &observingRepo{}
	ctx := relaybiz.WithObservationDelivery(context.Background(), relaybiz.NewObservationDelivery(repo, nil))
	ctx = relaybiz.WithUsageObservation(ctx, &relaybiz.Channel{ID: 2}, "ws-invalid", "exact-model")
	var financial evidence.Record
	_, _ = relayOpenAIWSFrames(ctx, client, upstream, []byte(`{"type":"response.create"}`), openAIWSRelayOptions{
		writeTimeout:        time.Second,
		idleTimeout:         time.Second,
		observationEndpoint: "/v1/responses",
		observationVersion:  "2026-02-06",
		durableRecord: func(record evidence.Record) error {
			financial = record
			return nil
		},
	})
	var terminal *evidence.SendEvent
	for _, event := range repo.snapshot() {
		if event.Category == "terminal" {
			copy := event
			terminal = &copy
		}
	}
	require.NotNil(t, terminal)
	require.Equal(t, "invalid", terminal.Record.Observation.Status)
	require.Equal(t, evidence.CaptureComplete, terminal.Capture, "a received response.completed is transport-complete even when its usage is invalid")
	require.Equal(t, "/v1/responses", terminal.Send.Endpoint)
	require.Equal(t, "2026-02-06", terminal.Send.APIVersion)
	require.Equal(t, terminal.Send.Endpoint, terminal.Record.UpstreamEndpoint)
	require.Equal(t, terminal.Send.APIVersion, terminal.Record.APIVersion)
	require.False(t, financial.Complete, "observation must not relax the financial verdict")
	require.Empty(t, financial.UpstreamEndpoint, "observation provenance must not rewrite financial records")
	require.Empty(t, financial.APIVersion)
}

func TestUsageObservationWSFinancialFailureKeepsReceivedEvidence(t *testing.T) {
	t.Setenv("RELAY_USAGE_OBSERVATION_V1", "true")
	client := newFakeFrameConn(4)
	upstream := newFakeFrameConn(4)
	upstream.in <- fakeFrame{msgType: coderws.MessageText, payload: []byte(`{"type":"response.completed","response":{"id":"resp-financial-failure","usage":{"input_tokens":3,"output_tokens":2}}}`)}
	close(upstream.in)
	repo := &observingRepo{}
	ctx := relaybiz.WithObservationDelivery(context.Background(), relaybiz.NewObservationDelivery(repo, nil))
	ctx = relaybiz.WithUsageObservation(ctx, &relaybiz.Channel{ID: 2}, "ws-financial-failure", "exact-model")
	failure := errors.New("financial evidence unavailable")
	_, exit := relayOpenAIWSFrames(ctx, client, upstream, []byte(`{"type":"response.create"}`), openAIWSRelayOptions{
		writeTimeout:  time.Second,
		idleTimeout:   time.Second,
		durableRecord: func(evidence.Record) error { return failure },
	})
	require.ErrorIs(t, exit.err, failure, "observation must preserve the financial failure")
	var terminals []evidence.SendEvent
	for _, event := range repo.snapshot() {
		require.NotEqual(t, "unmatched", event.Category, "a financial write failure must not emit the same response a second time")
		if event.Category == "terminal" {
			terminals = append(terminals, event)
		}
	}
	require.Len(t, terminals, 1)
	require.Equal(t, "verified", terminals[0].Record.Observation.Status, "the response was received before financial persistence failed")
	require.Equal(t, evidence.CaptureComplete, terminals[0].Capture)
}

func TestUsageObservationWSUsesCurrentAdmittedTurnIdentity(t *testing.T) {
	t.Setenv("RELAY_USAGE_OBSERVATION_V1", "true")
	client := newFakeFrameConn(4)
	upstream := newFakeFrameConn(4)
	repo := &observingRepo{}
	delivery := relaybiz.NewObservationDelivery(repo, nil)
	timings := &wsTimingSession{base: context.Background(), byID: map[string]*wsTimedTurn{}, firstSend: true, userID: 7}
	initial := timings.begin()
	ctx := relaybiz.WithObservationDelivery(initial.ctx, delivery)
	ctx = relaybiz.WithUsageObservation(ctx, &relaybiz.Channel{ID: 2}, "first-request", "exact-model")
	var observations []string
	requests := []string{"first-request", "next-admitted-request"}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = relayOpenAIWSFrames(ctx, client, upstream, []byte(`{"type":"response.create"}`), openAIWSRelayOptions{
			timings: timings, writeTimeout: time.Second, idleTimeout: time.Second,
			observationContext: func(turnCtx context.Context) context.Context {
				request := requests[len(observations)]
				observations = append(observations, requesttrace.ObservationID(turnCtx))
				turnCtx = relaybiz.WithObservationDelivery(turnCtx, delivery)
				return relaybiz.WithUsageObservation(turnCtx, &relaybiz.Channel{ID: 2}, request, "exact-model")
			},
		})
	}()
	waitFrame := func(frames <-chan fakeFrame) {
		select {
		case <-frames:
		case <-time.After(2 * time.Second):
			t.Fatal("expected websocket frame")
		}
	}
	waitFrame(upstream.out)
	upstream.in <- fakeFrame{msgType: coderws.MessageText, payload: []byte(`{"type":"response.completed","response":{"id":"first-response","usage":{"input_tokens":3,"output_tokens":2}}}`)}
	waitFrame(client.out)
	client.in <- fakeFrame{msgType: coderws.MessageText, payload: []byte(`{"type":"response.create"}`)}
	waitFrame(upstream.out)
	upstream.in <- fakeFrame{msgType: coderws.MessageText, payload: []byte(`{"type":"response.completed","response":{"id":"next-response","usage":{"input_tokens":4,"output_tokens":2}}}`)}
	waitFrame(client.out)
	close(upstream.in)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not stop")
	}
	require.Len(t, observations, 2, "each send must resolve its current admitted turn context")
	var terminals []evidence.SendEvent
	for _, event := range repo.snapshot() {
		if event.Category == "terminal" {
			terminals = append(terminals, event)
		}
	}
	require.Len(t, terminals, 2)
	for i, terminal := range terminals {
		require.Equal(t, requests[i], terminal.Send.RequestID)
		require.Equal(t, observations[i], terminal.Send.ObservationID)
	}
}

func TestUsageObservationHTTPRedirectIsReceivedResponse(t *testing.T) {
	t.Setenv("RELAY_USAGE_OBSERVATION_V1", "true")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/start" {
			w.Header().Set("Location", "/chat/completions")
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
		_, _ = io.WriteString(w, `{"usage":{"prompt_tokens":3,"completion_tokens":2}}`)
	}))
	defer upstream.Close()
	repo := &observingRepo{}
	ctx := relaybiz.WithObservationDelivery(context.Background(), relaybiz.NewObservationDelivery(repo, nil))
	ctx = relaybiz.WithUsageObservation(ctx, &relaybiz.Channel{ID: 2}, "redirect-request", "exact-model")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstream.URL+"/start", nil)
	require.NoError(t, err)
	resp, err := evidence.Do(upstream.Client(), req, "openai_chat")
	require.NoError(t, err)
	_, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	var terminals []evidence.SendEvent
	for _, event := range repo.snapshot() {
		if event.Category == "terminal" {
			terminals = append(terminals, event)
		}
	}
	require.Len(t, terminals, 2)
	require.Equal(t, evidence.CaptureUpstreamRejected, terminals[0].Capture, "a received redirect is not a capture gap")
	require.Equal(t, http.StatusTemporaryRedirect, terminals[0].Record.HTTPStatus)
	require.Equal(t, evidence.CaptureComplete, terminals[1].Capture)
}
