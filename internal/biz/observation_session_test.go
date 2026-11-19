package biz

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"micro-one-api/domain/requesttrace"
	"micro-one-api/domain/upstream/evidence"
)

type fakeObservationRepo struct {
	mu     sync.Mutex
	events []evidence.SendEvent
	fail   error
}

func (r *fakeObservationRepo) Put(_ context.Context, e evidence.SendEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail != nil {
		return r.fail
	}
	for _, existing := range r.events {
		if existing.ID == e.ID {
			return nil
		}
	}
	r.events = append(r.events, e)
	return nil
}
func (r *fakeObservationRepo) Pending(context.Context, int) ([]PendingObservation, error) {
	return nil, nil
}
func (r *fakeObservationRepo) Acknowledge(context.Context, string, string) error { return nil }
func (r *fakeObservationRepo) Defer(context.Context, string, time.Time) error    { return nil }
func (r *fakeObservationRepo) Stats(context.Context) (ObservationOutboxStats, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return ObservationOutboxStats{Pending: int64(len(r.events))}, nil
}

func (r *fakeObservationRepo) recorded() []evidence.SendEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]evidence.SendEvent(nil), r.events...)
}

func observationTestContext(t *testing.T, repo UsageObservationRepo) (context.Context, *httptest.Server) {
	t.Helper()
	t.Setenv("RELAY_USAGE_OBSERVATION_V1", "true")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-1","object":"chat.completion","usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12},"choices":[{"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(upstream.Close)
	ctx := WithObservationDelivery(context.Background(), NewObservationDelivery(repo, nil))
	ctx = WithUsageObservation(ctx, &Channel{ID: 7}, "req-1", "exact-model")
	return ctx, upstream
}

func sendObservedRequest(t *testing.T, ctx context.Context, upstream *httptest.Server) (string, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstream.URL+"/v1/chat/completions", strings.NewReader(`{"model":"exact-model","messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := evidence.Do(upstream.Client(), req, "openai_chat")
	if err != nil {
		return "", err
	}
	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return string(body), readErr
}

func TestObservationSessionRecordsStartedAndSingleTerminal(t *testing.T) {
	repo := &fakeObservationRepo{}
	ctx, upstream := observationTestContext(t, repo)
	body, err := sendObservedRequest(t, ctx, upstream)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if !strings.Contains(body, "prompt_tokens") {
		t.Fatalf("client response changed: %s", body)
	}
	events := repo.recorded()
	if len(events) != 2 {
		t.Fatalf("expected started+terminal, got %d: %+v", len(events), events)
	}
	if events[0].Category != "started" || events[1].Category != "terminal" {
		t.Fatalf("unexpected categories: %s/%s", events[0].Category, events[1].Category)
	}
	terminal := events[1]
	if terminal.Send.SendID != events[0].Send.SendID {
		t.Fatal("started and terminal must share one send id")
	}
	if terminal.Capture != evidence.CaptureComplete || terminal.Record == nil || terminal.Record.Observation.Status != "verified" {
		t.Fatalf("unexpected terminal capture/status: %+v", terminal)
	}
	if terminal.Send.Purpose != evidence.PurposeUser || terminal.Send.SourceKind != "channel" || terminal.Send.SourceID != 7 {
		t.Fatalf("untrusted attribution: %+v", terminal.Send)
	}

	// A response with no open send is an association anomaly, never a second
	// terminal and never a fabricated send.
	evidence.ObservationSinkFrom(ctx).Terminal(evidence.Record{Observation: evidence.Observation{Protocol: "openai_chat", Status: "verified"}})
	after := repo.recorded()
	if len(after) != 3 || after[2].Category != "unmatched" {
		t.Fatalf("expected one unmatched association anomaly, got %+v", after)
	}
	if after[2].Send.SendID != "" {
		t.Fatal("unmatched evidence must not carry a send id")
	}
}

func TestObservationPersistenceFailureDoesNotChangeTheResponse(t *testing.T) {
	repo := &fakeObservationRepo{fail: errors.New("disk full")}
	ctx, upstream := observationTestContext(t, repo)
	body, err := sendObservedRequest(t, ctx, upstream)
	if err != nil {
		t.Fatalf("observation failure escaped into the business path: %v", err)
	}
	if !strings.Contains(body, "prompt_tokens") {
		t.Fatalf("client response changed: %s", body)
	}
	if len(repo.recorded()) != 0 {
		t.Fatal("no event should be retained when local persistence failed")
	}
}

func TestObservationSinkNeverFabricatesASendWhenAdmissionRejects(t *testing.T) {
	repo := &fakeObservationRepo{}
	ctx, upstream := observationTestContext(t, repo)
	ctx = evidence.WithDurableRecorder(ctx, func() error { return errors.New("admission rejected") }, nil)
	_, err := sendObservedRequest(t, ctx, upstream)
	if err == nil {
		t.Fatal("durable admission must still reject the send")
	}
	events := repo.recorded()
	if len(events) != 1 || events[0].Category != "not_sent" || events[0].Reason != evidence.NotSentAdmissionRejected {
		t.Fatalf("expected one not_sent event, got %+v", events)
	}
	if events[0].Record != nil {
		t.Fatal("not_sent must not carry fake upstream evidence")
	}
	if events[0].Send.SendID != "" {
		t.Fatal("not_sent must not be assigned a send id")
	}
}

func TestObservationCaptureMarksClientCancelOnlyOnTrustedCancel(t *testing.T) {
	repo := &fakeObservationRepo{}
	t.Setenv("RELAY_USAGE_OBSERVATION_V1", "true")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	defer upstream.Close()
	sessionCtx, cancel := context.WithCancel(context.Background())
	ctx := WithObservationDelivery(sessionCtx, NewObservationDelivery(repo, nil))
	ctx = WithUsageObservation(ctx, &Channel{ID: 3}, "req-cancel", "m")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstream.URL+"/v1/chat/completions", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := evidence.Do(upstream.Client(), req, "openai_chat")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	cancel()
	_ = resp.Body.Close()
	events := repo.recorded()
	if len(events) != 2 || events[1].Capture != evidence.CaptureClientCancel {
		t.Fatalf("expected client_cancel terminal, got %+v", events)
	}
}

// R20: the observation rollout boundary is independent of the financial
// allowlist and must be able to exclude a source without changing its traffic.
func TestObservationScopeSelectsSourcesWithoutTouchingBusinessPaths(t *testing.T) {
	t.Setenv("RELAY_USAGE_OBSERVATION_V1", "true")
	scope, err := NewObservationScope("channel:7:exact-model,subscription:9:*")
	if err != nil {
		t.Fatalf("scope: %v", err)
	}
	for _, tc := range []struct {
		kind  string
		id    int64
		model string
		want  bool
	}{
		{"channel", 7, "exact-model", true},
		{"channel", 7, "other-model", false},
		{"channel", 8, "exact-model", false},
		{"subscription", 9, "any-model", true},
		{"unknown", 0, "exact-model", false},
	} {
		if got := scope.Allows(tc.kind, tc.id, tc.model); got != tc.want {
			t.Fatalf("Allows(%s,%d,%s)=%v want %v", tc.kind, tc.id, tc.model, got, tc.want)
		}
	}
	if _, err := NewObservationScope("channel:not-a-number:*"); err == nil {
		t.Fatal("a malformed scope must be rejected at startup")
	}

	repo := &fakeObservationRepo{}
	delivery := NewObservationDelivery(repo, nil)
	delivery.SetScope(scope)
	ctx := WithObservationDelivery(context.Background(), delivery)
	included := WithUsageObservation(ctx, &Channel{ID: 7}, "req-in", "exact-model")
	if evidence.ObservationSinkFrom(included) == nil {
		t.Fatal("an in-scope source must be observed")
	}
	excluded := WithUsageObservation(ctx, &Channel{ID: 8}, "req-out", "exact-model")
	if evidence.ObservationSinkFrom(excluded) != nil {
		t.Fatal("an out-of-scope source must not produce evidence")
	}
	if evidence.ObservationExpected(excluded) {
		t.Fatal("an out-of-scope source must not count as an expected send")
	}
}

// R01: protocol, source_kind and purpose are three different finite enums. The
// producer must not normalise a source or purpose through the protocol mapper.
func TestObservationMetricsKeepIndependentLabelEnums(t *testing.T) {
	repo := &fakeObservationRepo{}
	ctx, upstream := observationTestContext(t, repo)
	if _, err := sendObservedRequest(t, ctx, upstream); err != nil {
		t.Fatalf("send: %v", err)
	}
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	found := false
	for _, family := range families {
		if family.GetName() != "micro_one_api_relay_usage_observation_started_total" {
			continue
		}
		for _, sample := range family.GetMetric() {
			labels := map[string]string{}
			for _, pair := range sample.GetLabel() {
				labels[pair.GetName()] = pair.GetValue()
			}
			if labels["source_kind"] == "other" || labels["purpose"] == "other" {
				t.Fatalf("source/purpose were normalised through the protocol enum: %v", labels)
			}
			if labels["protocol"] == "openai_chat" && labels["source_kind"] == "channel" && labels["purpose"] == "user" && sample.GetCounter().GetValue() >= 1 {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("no started sample carried protocol=openai_chat source_kind=channel purpose=user")
	}
}

// R02: the session-level capture must follow the protocol terminator, so an
// invalid usage object on a complete stream is visible to the complete-usage
// anomaly rules instead of hiding behind capture=truncated.
func TestObservationCaptureKeepsCompleteStreamWithInvalidUsage(t *testing.T) {
	repo := &fakeObservationRepo{}
	t.Setenv("RELAY_USAGE_OBSERVATION_V1", "true")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"usage\":{\"prompt_tokens\":\"not-a-number\",\"completion_tokens\":2}}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	ctx := WithObservationDelivery(context.Background(), NewObservationDelivery(repo, nil))
	ctx = WithUsageObservation(ctx, &Channel{ID: 7}, "req-invalid", "exact-model")
	if _, err := sendObservedRequest(t, ctx, upstream); err != nil {
		t.Fatalf("send: %v", err)
	}
	events := repo.recorded()
	if len(events) != 2 || events[1].Category != "terminal" {
		t.Fatalf("expected terminal evidence, got %+v", events)
	}
	if events[1].Capture != evidence.CaptureComplete || events[1].Record.Observation.Status != "invalid" {
		t.Fatalf("expected complete/invalid, got %s/%s", events[1].Capture, events[1].Record.Observation.Status)
	}
}

func TestObservationChannelOneChatUsageAliases(t *testing.T) {
	t.Setenv("RELAY_USAGE_OBSERVATION_V1", "true")
	const aliases = `{"usage":{"prompt_tokens":10,"input_tokens":10,"completion_tokens":3,"output_tokens":3,"total_tokens":13,"prompt_tokens_details":{"cached_tokens":4},"input_tokens_details":{"cached_tokens":4},"completion_tokens_details":{"reasoning_tokens":2},"output_tokens_details":{"reasoning_tokens":2}}}`
	const partition = `,"prompt_cache_hit_tokens":4,"prompt_cache_miss_tokens":6`
	cacheWrite := strings.ReplaceAll(aliases, `"cached_tokens":4`, `"cached_tokens":4,"cache_write_tokens":5`)
	conflict := strings.Replace(aliases, `"input_tokens":10`, `"input_tokens":11`, 1)
	for _, tc := range []struct {
		name, body, status string
		channel            Channel
		stream             bool
	}{
		{name: "matching aliases", body: aliases, channel: Channel{ID: 1}, status: "verified"},
		{name: "cache partition", body: strings.Replace(aliases, `"total_tokens":13`, `"total_tokens":13`+partition, 1), channel: Channel{ID: 1}, status: "verified"},
		{name: "cache write in SSE", body: cacheWrite, channel: Channel{ID: 1}, stream: true, status: "verified"},
		{name: "partial earlier SSE frame", body: `{"usage":{"input_tokens":10,"prompt_tokens":10}}` + "\n\ndata: " + aliases, channel: Channel{ID: 1}, stream: true, status: "verified"},
		{name: "conflicting counts", body: conflict, channel: Channel{ID: 1}, status: "unsupported"},
		{name: "conflicting earlier SSE frame", body: conflict + "\n\ndata: " + aliases, channel: Channel{ID: 1}, stream: true, status: "unsupported"},
		{name: "invalid alias type", body: strings.Replace(aliases, `"input_tokens":10`, `"input_tokens":"10"`, 1), channel: Channel{ID: 1}, status: "unsupported"},
		{name: "conflicting cache partition", body: strings.Replace(aliases, `"total_tokens":13`, `"total_tokens":13,"prompt_cache_hit_tokens":4,"prompt_cache_miss_tokens":7`, 1), channel: Channel{ID: 1}, status: "unsupported"},
		{name: "conflicting cache write", body: strings.Replace(cacheWrite, `"cache_write_tokens":5`, `"cache_write_tokens":6`, 1), channel: Channel{ID: 1}, status: "unsupported"},
		{name: "unknown dimension", body: strings.Replace(aliases, `"output_tokens":3`, `"output_tokens":3,"new_meter":2`, 1), channel: Channel{ID: 1}, status: "unsupported"},
		{name: "other channel", body: aliases, channel: Channel{ID: 2}, status: "unsupported"},
		{name: "subscription with same id", body: aliases, channel: Channel{ID: 1, SubscriptionAccountID: 1}, status: "unsupported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, contentType := tc.body, "application/json"
			if tc.stream {
				body, contentType = "data: "+body+"\n\ndata: [DONE]\n\n", "text/event-stream"
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", contentType)
				_, _ = io.WriteString(w, body)
			}))
			defer upstream.Close()
			repo := &fakeObservationRepo{}
			ctx := WithObservationDelivery(context.Background(), NewObservationDelivery(repo, nil))
			ctx = WithUsageObservation(ctx, &tc.channel, "req-aliases", "exact-model")
			var durable evidence.Record
			ctx = evidence.WithDurableRecorder(ctx, nil, func(record evidence.Record) error {
				durable = record
				return nil
			})
			response, err := sendObservedRequest(t, ctx, upstream)
			if err != nil || response != body {
				t.Fatalf("response changed: %q, %v", response, err)
			}
			events := repo.recorded()
			if len(events) == 0 {
				t.Fatal("no observation events")
			}
			terminal := events[len(events)-1]
			if terminal.Category != "terminal" || terminal.Capture != evidence.CaptureComplete || terminal.Record == nil {
				t.Fatalf("missing complete terminal: %+v", events)
			}
			got := terminal.Record.Observation
			if got.Status != tc.status {
				t.Fatalf("observation = %s/%s, want %s", got.Status, got.Reason, tc.status)
			}
			original := durable.Observation
			if original.Status != "unsupported" || original.Buckets != nil || string(got.Usage) != string(original.Usage) || got.UsageDigest != original.UsageDigest {
				t.Fatalf("durable verdict or raw evidence changed: original=%+v, observation=%+v", original, got)
			}
			if tc.status == "verified" && (got.Buckets == nil || *got.Buckets != [5]int64{6, 4, 0, 0, 3} || got.ContractRevision == original.ContractRevision) {
				t.Fatalf("expected scoped compatibility revision and native buckets, got %+v", got)
			}
		})
	}
}

// R08: one client.Do that follows a redirect performs two real HTTP sends. The
// observation seam must show both hops; the redirect policy itself is unchanged.
func TestObservationRecordsRedirectHopsAsSeparateSends(t *testing.T) {
	repo := &fakeObservationRepo{}
	t.Setenv("RELAY_USAGE_OBSERVATION_V1", "true")
	var hits atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, "/v1/chat/completions-final", http.StatusTemporaryRedirect)
	})
	mux.HandleFunc("/v1/chat/completions-final", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`)
	})
	upstream := httptest.NewServer(mux)
	defer upstream.Close()

	ctx := WithObservationDelivery(context.Background(), NewObservationDelivery(repo, nil))
	ctx = WithUsageObservation(ctx, &Channel{ID: 7}, "req-redirect", "exact-model")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstream.URL+"/v1/chat/completions", strings.NewReader(`{"model":"exact-model","messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := evidence.Do(upstream.Client(), req, "openai_chat")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if hits.Load() != 2 {
		t.Fatalf("redirect policy changed: upstream hits=%d want 2", hits.Load())
	}

	events := repo.recorded()
	var started, terminals int
	hops := 0
	for _, event := range events {
		switch event.Category {
		case "started":
			started++
		case "terminal":
			terminals++
			if event.Reason == "redirect_hop" {
				hops++
			}
		}
	}
	if started != 2 || terminals != 2 || hops != 1 {
		t.Fatalf("expected two hops with one redirect terminal, got started=%d terminals=%d hops=%d: %+v", started, terminals, hops, events)
	}
	if events[0].Send.SendID == events[2].Send.SendID {
		t.Fatal("each redirect hop is a distinct send")
	}
}

// A rollback keeps the delivery only to drain retained evidence; live requests
// must not be marked as expected, or the detached-seam alert would fire for a
// legitimately disabled deployment.
func TestDisabledProducerDoesNotMarkRequestsExpected(t *testing.T) {
	t.Setenv("RELAY_USAGE_OBSERVATION_V1", "false")
	delivery := NewObservationDelivery(&fakeObservationRepo{}, nil)
	ctx := WithObservationDelivery(context.Background(), delivery)
	if evidence.ObservationExpected(ctx) {
		t.Fatal("a disabled producer must not set the expectation flag")
	}
	if evidence.ObservationSinkFrom(WithUsageObservation(ctx, &Channel{ID: 7}, "req", "model")) != nil {
		t.Fatal("a disabled producer must not attach a sink")
	}
}

func TestObservationScopeReselectionClearsInheritedSink(t *testing.T) {
	t.Setenv("RELAY_USAGE_OBSERVATION_V1", "true")
	scope, err := NewObservationScope("channel:7:exact-model")
	if err != nil {
		t.Fatal(err)
	}
	delivery := NewObservationDelivery(&fakeObservationRepo{}, nil)
	delivery.SetScope(scope)
	ctx := WithObservationDelivery(context.Background(), delivery)
	included := WithUsageObservation(ctx, &Channel{ID: 7}, "req-a", "exact-model")
	if evidence.ObservationSinkFrom(included) == nil {
		t.Fatal("included source has no sink")
	}
	excluded := WithUsageObservation(included, &Channel{ID: 8}, "req-b", "exact-model")
	if evidence.ObservationSinkFrom(excluded) != nil {
		t.Fatal("excluded source inherited the previous source's sink")
	}
	if evidence.ObservationExpected(excluded) {
		t.Fatal("excluded source is still expected")
	}
	reincluded := WithUsageObservation(excluded, &Channel{ID: 7}, "req-a", "exact-model")
	if evidence.ObservationSinkFrom(reincluded) == nil || !evidence.ObservationExpected(reincluded) {
		t.Fatal("reselected included source must restore its sink and expectation")
	}
}

func TestObservationScopeRejectsEmptyEntries(t *testing.T) {
	for _, raw := range []string{",", "channel:7:exact-model,", ",channel:7:exact-model", "channel:7:exact-model,,channel:8:*"} {
		t.Run(raw, func(t *testing.T) {
			if _, err := NewObservationScope(raw); err == nil {
				t.Fatal("malformed scope must not silently widen observation")
			}
		})
	}
}

func TestObservationScopePreservesExactModelWithColon(t *testing.T) {
	scope, err := NewObservationScope("channel:7:vendor/model:revision")
	if err != nil {
		t.Fatalf("valid exact upstream model rejected: %v", err)
	}
	if !scope.Allows("channel", 7, "vendor/model:revision") || scope.Allows("channel", 7, "vendor/model") {
		t.Fatal("scope did not preserve the exact upstream model")
	}
}

func TestObservationRequestEventsStayUniqueAcrossAttempts(t *testing.T) {
	t.Setenv("RELAY_USAGE_OBSERVATION_V1", "true")
	for _, category := range []string{"not_sent", "unmatched"} {
		t.Run(category, func(t *testing.T) {
			repo := &fakeObservationRepo{}
			ctx := WithObservationDelivery(context.Background(), NewObservationDelivery(repo, nil))
			ctx, _ = requesttrace.StartObservation(ctx, "http", "/v1/responses", "http", "legacy", true)
			for i, requestID := range []string{"attempt-one", "attempt-two"} {
				attempt := requesttrace.WithAttempt(ctx, requesttrace.Attempt{RootRequestID: "root", Number: int32(i + 1)})
				sink := evidence.ObservationSinkFrom(WithUsageObservation(attempt, &Channel{ID: 7}, requestID, "exact-model"))
				if category == "not_sent" {
					sink.NotSent("responses", evidence.NotSentAdmissionRejected)
				} else {
					sink.Terminal(evidence.Record{Observation: evidence.Observation{Protocol: "responses", Status: "missing"}})
				}
			}
			if got := len(repo.recorded()); got != 2 {
				t.Fatalf("two attempt events collapsed under one identity: got %d", got)
			}
		})
	}
}

func TestObservationTurnDoesNotInventAttemptIdentity(t *testing.T) {
	t.Setenv("RELAY_USAGE_OBSERVATION_V1", "true")
	repo := &fakeObservationRepo{}
	ctx := WithObservationDelivery(context.Background(), NewObservationDelivery(repo, nil))
	ctx = WithUsageObservation(ctx, &Channel{ID: 7}, strings.Repeat("r", 64), "exact-model")
	turnCtx, _ := requesttrace.StartObservation(ctx, "ws_turn", "/v1/responses", "websocket", "legacy", true)
	sink, ok := ObservationTurn(turnCtx, 2)
	if !ok {
		t.Fatal("missing second-turn sink")
	}
	sink.Started("responses", "", "")
	events := repo.recorded()
	if len(events) != 1 {
		t.Fatalf("second turn event rejected: got %d", len(events))
	}
	if events[0].Send.ObservationID != requesttrace.ObservationID(turnCtx) {
		t.Fatal("second turn did not use its actual observation identity")
	}
	if events[0].Send.RequestID != "" {
		t.Fatal("second turn invented a billing attempt id instead of leaving an unknown id empty")
	}
}
