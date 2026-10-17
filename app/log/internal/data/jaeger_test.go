package data

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
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
