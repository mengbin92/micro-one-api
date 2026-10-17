package data

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"micro-one-api/domain/requesttrace"
	"micro-one-api/pkg/jsonx"
)

func traceFixture(traceID, rootID string, start int64) map[string]any {
	return map[string]any{"resourceSpans": []any{map[string]any{
		"resource": map[string]any{"attributes": []any{map[string]any{"key": "service.name", "value": map[string]any{"stringValue": "timing-contract"}}}},
		"scopeSpans": []any{map[string]any{"scope": map[string]any{"name": "timing-contract"}, "spans": []any{
			map[string]any{"traceId": traceID, "spanId": rootID, "name": "relay.request", "kind": 2, "startTimeUnixNano": strconv.FormatInt(start, 10), "endTimeUnixNano": strconv.FormatInt(start+10000000, 10)},
			map[string]any{"traceId": traceID, "spanId": "2222222222222222", "parentSpanId": rootID, "name": "request.parse", "kind": 1, "startTimeUnixNano": strconv.FormatInt(start+1000000, 10), "endTimeUnixNano": strconv.FormatInt(start+3000000, 10), "attributes": []any{
				map[string]any{"key": "Authorization", "value": map[string]any{"stringValue": "must-not-escape"}},
				map[string]any{"key": "db.statement", "value": map[string]any{"stringValue": "private SQL"}},
			}},
			map[string]any{"traceId": traceID, "spanId": "3333333333333333", "name": "other-request", "kind": 2, "startTimeUnixNano": strconv.FormatInt(start, 10), "endTimeUnixNano": strconv.FormatInt(start+1000000, 10)},
		}}},
	}}}
}
func fixtureSummary(traceID, spanID string, start int64) *requesttrace.TimingSummary {
	s := timingEntry().RequestTiming
	s.Attempts = nil
	s.OTelTraceID = traceID
	s.SpanID = spanID
	s.CollectionState = "recorded"
	s.StartedAtMS = start / 1e6
	s.HandlerFinishedAtMS = nil
	s.HandlerDurationMS = nil
	s.Stages = []requesttrace.TimingStage{{Phase: "request.parse", Count: 1, DurationMS: 2}}
	return s
}

func TestJaegerV3ProjectionAndFailureStates(t *testing.T) {
	traceID, spanID := "11111111111111111111111111111111", "1111111111111111"
	start := time.Now().UnixNano()
	payload := map[string]any{"result": traceFixture(traceID, spanID, start)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v3/traces/"+traceID, r.URL.Path)
		_ = jsonx.NewEncoder(w).Encode(payload)
	}))
	defer server.Close()
	r := &Repository{data: &Data{traceHTTP: server.Client(), traceEndpoint: server.URL}}
	detail, err := r.ReadRequestTrace(context.Background(), fixtureSummary(traceID, spanID, start))
	require.NoError(t, err)
	require.Equal(t, "available", detail.DetailState)
	require.Len(t, detail.Spans, 2)
	encoded, err := jsonx.Marshal(detail)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "must-not-escape")
	require.NotContains(t, string(encoded), "private SQL")
	require.NotContains(t, string(encoded), "other-request")
	require.InDelta(t, 1, detail.Spans[1].StartOffsetMS, 0.0001)
	require.InDelta(t, 2, detail.Spans[1].DurationMS, 0.0001)
	for _, test := range []struct {
		age   time.Duration
		state string
	}{{0, "pending"}, {time.Minute, "partial"}, {49 * time.Hour, "expired"}} {
		s := fixtureSummary(traceID, spanID, time.Now().Add(-test.age).UnixNano())
		view := missingTrace(&requesttrace.TraceDetail{Summary: s}, time.Now())
		require.Equal(t, test.state, view.DetailState)
	}
	server.Close()
	detail, err = r.ReadRequestTrace(context.Background(), fixtureSummary(traceID, spanID, start))
	require.NoError(t, err)
	require.Equal(t, "unavailable", detail.DetailState)
}

func TestJaegerRequiresObservedMilestonesForCompleteDetail(t *testing.T) {
	traceID, spanID := "11111111111111111111111111111111", "1111111111111111"
	start := time.Now().Add(-time.Minute).UnixNano()
	payload := map[string]any{"result": traceFixture(traceID, spanID, start)}
	root := payload["result"].(map[string]any)["resourceSpans"].([]any)[0].(map[string]any)["scopeSpans"].([]any)[0].(map[string]any)["spans"].([]any)[0].(map[string]any)
	var payloadMu sync.RWMutex
	setEvents := func(events []any) {
		payloadMu.Lock()
		defer payloadMu.Unlock()
		root["events"] = events
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		payloadMu.RLock()
		defer payloadMu.RUnlock()
		_ = jsonx.NewEncoder(w).Encode(payload)
	}))
	defer server.Close()
	r := &Repository{data: &Data{traceHTTP: server.Client(), traceEndpoint: server.URL}}
	for _, milestone := range []string{"output.finished", "handler.finished", "turn.finished", "rpc.finished", "output.first_content"} {
		t.Run(milestone, func(t *testing.T) {
			setEvents(nil)
			s := fixtureSummary(traceID, spanID, start)
			finished, duration := start/1e6+10, float64(10)
			switch milestone {
			case "output.finished":
				s.OutputFinishedAtMS, s.OutputDurationMS = &finished, &duration
			case "handler.finished":
				s.HandlerFinishedAtMS, s.HandlerDurationMS = &finished, &duration
			case "turn.finished":
				s.ObservationKind, s.Protocol = "ws_turn", "websocket"
				s.TurnFinishedAtMS, s.TurnDurationMS = &finished, &duration
			case "rpc.finished":
				s.ObservationKind, s.Protocol = "grpc", "grpc"
				s.RPCFinishedAtMS, s.RPCDurationMS = &finished, &duration
			case "output.first_content":
				s.FirstContentMS = &duration
			}
			detail, err := r.ReadRequestTrace(context.Background(), s)
			require.NoError(t, err)
			require.Equal(t, "partial", detail.DetailState, "summary proves this milestone happened but Jaeger omitted it")
			require.Contains(t, detail.MissingReasons, "missing_"+strings.ReplaceAll(milestone, ".", "_"))
			setEvents([]any{map[string]any{"name": milestone, "timeUnixNano": "invalid"}})
			detail, err = r.ReadRequestTrace(context.Background(), s)
			require.NoError(t, err)
			require.Equal(t, "partial", detail.DetailState)
			require.Empty(t, detail.Milestones, "malformed timestamps must not masquerade as recorded milestones")
			setEvents([]any{map[string]any{"name": milestone, "timeUnixNano": strconv.FormatInt(start+10e6, 10)}})
			detail, err = r.ReadRequestTrace(context.Background(), s)
			require.NoError(t, err)
			require.Equal(t, "available", detail.DetailState)
		})
	}
}

func TestJaegerCompletenessMatchesDistinctAttemptNumbers(t *testing.T) {
	traceID, spanID := "11111111111111111111111111111111", "1111111111111111"
	start := time.Now().Add(-time.Minute).UnixNano()
	fixture := traceFixture(traceID, spanID, start)
	spans := fixture["resourceSpans"].([]any)[0].(map[string]any)["scopeSpans"].([]any)[0].(map[string]any)["spans"].([]any)
	for _, span := range spans[1:] {
		span := span.(map[string]any)
		span["name"], span["parentSpanId"] = "relay.attempt", spanID
		span["attributes"] = []any{map[string]any{"key": "relay.attempt.number", "value": map[string]any{"intValue": "1"}}}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = jsonx.NewEncoder(w).Encode(map[string]any{"result": fixture})
	}))
	defer server.Close()
	r := &Repository{data: &Data{traceHTTP: server.Client(), traceEndpoint: server.URL}}
	s := fixtureSummary(traceID, spanID, start)
	s.Stages = []requesttrace.TimingStage{{Phase: "relay.attempt", Count: 2, DurationMS: 3}}
	s.Attempts = []requesttrace.TimingAttempt{{Number: 1, Outcome: "failed"}, {Number: 2, Outcome: "success"}}
	detail, err := r.ReadRequestTrace(context.Background(), s)
	require.NoError(t, err)
	require.Equal(t, "partial", detail.DetailState, "duplicate spans for one attempt cannot cover another missing attempt")
	require.Contains(t, detail.MissingReasons, "missing_attempt_spans")
}

func TestJaegerAllowsWebSocketConnectionStagesBeforeTurn(t *testing.T) {
	traceID, spanID := "11111111111111111111111111111111", "1111111111111111"
	start := time.Now().Add(-time.Minute).UnixNano()
	fixture := traceFixture(traceID, spanID, start)
	spans := fixture["resourceSpans"].([]any)[0].(map[string]any)["scopeSpans"].([]any)[0].(map[string]any)["spans"].([]any)
	spans[1].(map[string]any)["name"] = "ws.accept"
	spans[1].(map[string]any)["startTimeUnixNano"] = strconv.FormatInt(start-5e6, 10)
	spans[1].(map[string]any)["endTimeUnixNano"] = strconv.FormatInt(start-3e6, 10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = jsonx.NewEncoder(w).Encode(map[string]any{"result": fixture})
	}))
	defer server.Close()
	r := &Repository{data: &Data{traceHTTP: server.Client(), traceEndpoint: server.URL}}
	s := fixtureSummary(traceID, spanID, start)
	s.ObservationKind, s.Protocol = "ws_turn", "websocket"
	s.Stages = []requesttrace.TimingStage{{Phase: "ws.accept", Count: 1, DurationMS: 2}}
	detail, err := r.ReadRequestTrace(context.Background(), s)
	require.NoError(t, err)
	require.Equal(t, "available", detail.DetailState)
	require.NotContains(t, detail.MissingReasons, "clock_skew", "the first turn intentionally includes handshake timing before its own start")
}

func TestJaegerOversizedRelatedResponseExhaustsCumulativeBudget(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(strings.Repeat(" ", 100)))
	}))
	defer server.Close()
	r := &Repository{data: &Data{traceHTTP: server.Client(), traceEndpoint: server.URL}}
	budget := &traceBudget{bytes: 32, spans: 2000}
	for range 8 {
		_, truncated, err := r.fetchTrace(context.Background(), "11111111111111111111111111111111", budget)
		require.NoError(t, err)
		require.True(t, truncated)
	}
	require.EqualValues(t, 1, calls.Load(), "an oversized response consumes the remaining cumulative budget")
	require.Zero(t, budget.bytes)
}

func TestJaegerProjectsConnectionAndCacheReuseAttributes(t *testing.T) {
	traceID, spanID := "11111111111111111111111111111111", "1111111111111111"
	start := time.Now().Add(-time.Minute).UnixNano()
	fixture := traceFixture(traceID, spanID, start)
	spans := fixture["resourceSpans"].([]any)[0].(map[string]any)["scopeSpans"].([]any)[0].(map[string]any)["spans"].([]any)
	spans[1].(map[string]any)["attributes"] = []any{
		map[string]any{"key": "ws.pool.reused", "value": map[string]any{"boolValue": true}},
		map[string]any{"key": "routing.reused", "value": map[string]any{"boolValue": false}},
		map[string]any{"key": "cache.name", "value": map[string]any{"stringValue": "auth"}},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = jsonx.NewEncoder(w).Encode(map[string]any{"result": fixture})
	}))
	defer server.Close()
	r := &Repository{data: &Data{traceHTTP: server.Client(), traceEndpoint: server.URL}}
	detail, err := r.ReadRequestTrace(context.Background(), fixtureSummary(traceID, spanID, start))
	require.NoError(t, err)
	require.Equal(t, map[string]string{"ws.pool.reused": "true", "routing.reused": "false", "cache.name": "auth"}, detail.Spans[1].SafeAttributes)
}

func TestJaegerMissingTraceAgeUsesCompletionOfLongRequest(t *testing.T) {
	traceID, spanID := "11111111111111111111111111111111", "1111111111111111"
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	r := &Repository{data: &Data{traceHTTP: server.Client(), traceEndpoint: server.URL}}
	for _, kind := range []string{"http", "ws_turn", "grpc"} {
		t.Run(kind, func(t *testing.T) {
			for _, test := range []struct {
				finishedAgo time.Duration
				state       string
			}{{time.Second, "pending"}, {time.Hour, "partial"}, {49 * time.Hour, "expired"}} {
				s := fixtureSummary(traceID, spanID, time.Now().Add(-50*time.Hour).UnixNano())
				finished := time.Now().Add(-test.finishedAgo).UnixMilli()
				duration := float64(finished - s.StartedAtMS)
				switch kind {
				case "http":
					s.HandlerFinishedAtMS, s.HandlerDurationMS = &finished, &duration
				case "ws_turn":
					s.ObservationKind, s.Protocol = kind, "websocket"
					s.TurnFinishedAtMS, s.TurnDurationMS = &finished, &duration
				case "grpc":
					s.ObservationKind, s.Protocol = kind, "grpc"
					s.RPCFinishedAtMS, s.RPCDurationMS = &finished, &duration
				}
				detail, err := r.ReadRequestTrace(context.Background(), s)
				require.NoError(t, err)
				require.Equal(t, test.state, detail.DetailState, "a long-lived request exports its finished spans at completion")
			}
		})
	}
}

// Run against the repository-pinned Jaeger 2.20 container; no supplier or
// financial services are involved in this contract fixture.
func TestJaegerV3RealContract(t *testing.T) {
	endpoint, otlp := os.Getenv("TIMING_TEST_JAEGER_QUERY"), os.Getenv("TIMING_TEST_JAEGER_OTLP")
	if endpoint == "" || otlp == "" {
		t.Skip("local Jaeger contract endpoints not supplied")
	}
	traceID := strings.ReplaceAll(uuid.NewString(), "-", "")
	spanID := "1111111111111111"
	start := time.Now().UnixNano()
	encoded, err := jsonx.Marshal(traceFixture(traceID, spanID, start))
	require.NoError(t, err)
	resp, err := http.Post(otlp+"/v1/traces", "application/json", bytes.NewReader(encoded))
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, 200, resp.StatusCode)
	r := &Repository{data: &Data{traceHTTP: &http.Client{Timeout: time.Second}, traceEndpoint: endpoint}}
	var detail *requesttrace.TraceDetail
	require.Eventually(t, func() bool {
		detail, err = r.ReadRequestTrace(context.Background(), fixtureSummary(traceID, spanID, start))
		return err == nil && detail.DetailState == "available"
	}, 10*time.Second, 100*time.Millisecond)
	require.Len(t, detail.Spans, 2)
	require.Equal(t, "timing-contract", detail.Spans[0].Service)
	require.InDelta(t, 2, detail.Spans[1].DurationMS, 0.01)
}
