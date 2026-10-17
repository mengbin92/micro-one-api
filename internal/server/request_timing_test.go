package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace/noop"
	"micro-one-api/domain/requesttrace"
)

func timingSink(t *testing.T) (*requesttrace.Exporter, <-chan *requesttrace.TimingSummary) {
	t.Helper()
	summaries := make(chan *requesttrace.TimingSummary, 32)
	exporter := requesttrace.NewExporter(func(_ context.Context, s *requesttrace.TimingSummary) error { summaries <- s; return nil }, nil)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		require.NoError(t, exporter.Close(ctx))
	})
	return exporter, summaries
}
func awaitTiming(t *testing.T, summaries <-chan *requesttrace.TimingSummary) *requesttrace.TimingSummary {
	t.Helper()
	select {
	case summary := <-summaries:
		require.NoError(t, summary.Validate())
		return summary
	case <-time.After(time.Second):
		t.Fatal("request timing was not exported")
		return nil
	}
}

func TestTimingHTTPBoundaryIgnoresHeartbeatAndExcludesPostOutputSettlement(t *testing.T) {
	exporter, summaries := timingSink(t)
	server := &HTTPServer{timingExporter: exporter}
	var heartbeatFinished int64
	handler := server.wrapRoute("/v1/timing-test", executionRoute("/v1/timing-test", "legacy"), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n: ping\n\n")
		w.(http.Flusher).Flush()
		heartbeatFinished = time.Now().UnixMilli()
		time.Sleep(3 * time.Millisecond)
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\ndata: [DONE]\n\n")
		w.(http.Flusher).Flush()
		_, end := requesttrace.Phase(r.Context(), "quota.commit")
		time.Sleep(4 * time.Millisecond)
		end(nil)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/timing-test", nil))
	summary := awaitTiming(t, summaries)
	require.True(t, summary.OutputComplete)
	require.NotNil(t, summary.FirstContentMS)
	require.GreaterOrEqual(t, summary.StartedAtMS+int64(*summary.FirstContentMS), heartbeatFinished)
	require.Greater(t, *summary.HandlerDurationMS, *summary.OutputDurationMS)
	require.Equal(t, "sse", summary.Protocol)
}

func TestTimingEarlyRejectsAreUniqueWithoutOTelAndPreflightIsExcluded(t *testing.T) {
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(noop.NewTracerProvider())
	defer otel.SetTracerProvider(previous)
	exporter, summaries := timingSink(t)
	server := &HTTPServer{timingExporter: exporter}
	handler := server.wrapRoute("/v1/timing-test", executionRoute("/v1/timing-test", "raw"), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = io.WriteString(w, `{"error":"unauthorized"}`)
	}))
	for range 2 {
		request := httptest.NewRequest(http.MethodPost, "/v1/timing-test", nil)
		request.Header.Set("traceparent", "00-11111111111111111111111111111111-1111111111111111-00")
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}
	first, second := awaitTiming(t, summaries), awaitTiming(t, summaries)
	require.NotEqual(t, first.ObservationID, second.ObservationID)
	require.Empty(t, first.OTelTraceID)
	require.Equal(t, "not_recorded", first.CollectionState)
	require.EqualValues(t, 0, first.UserID)
	require.Equal(t, "rejected", first.Outcome)
	request := httptest.NewRequest(http.MethodOptions, "/v1/timing-test", nil)
	request.Header.Set("Access-Control-Request-Method", "POST")
	handler.ServeHTTP(httptest.NewRecorder(), request)
	select {
	case <-summaries:
		t.Fatal("preflight generated a request timing summary")
	default:
	}
}

type failedTimingWriter struct{ http.ResponseWriter }

func (f failedTimingWriter) Write([]byte) (int, error) { return 0, errors.New("client left") }
func (f failedTimingWriter) FlushError() error         { return errors.New("client left") }

func TestTimingWriteFailureDoesNotInventFirstContentOrCompletedOutput(t *testing.T) {
	exporter, summaries := timingSink(t)
	server := &HTTPServer{timingExporter: exporter}
	handler := server.observeRequestTiming(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n")
		_ = http.NewResponseController(w).Flush()
	}), "/v1/responses", "legacy")
	handler.ServeHTTP(failedTimingWriter{httptest.NewRecorder()}, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
	summary := awaitTiming(t, summaries)
	require.Nil(t, summary.FirstContentMS)
	require.False(t, summary.OutputComplete)
}

func TestWSTimingFinishesEachTurnBeforeConnectionCloses(t *testing.T) {
	exporter, summaries := timingSink(t)
	server := &HTTPServer{timingExporter: exporter}
	ctx, session := server.startWSTimings(context.Background(), []byte(`{"type":"response.create","model":"test"}`))
	defer session.Close()
	session.Identity(7, "existing-business-root")
	ctx = session.Send(ctx, []byte(`{"type":"response.create","model":"test"}`))
	session.Sent(ctx, nil)
	session.Terminal("response-1", false)
	// Synchronous settlement occurs before this last frame is written.
	_, end := requesttrace.Phase(ctx, "quota.commit")
	end(nil)
	session.Written("response-1", []byte(`{"type":"response.completed","response":{"id":"response-1"}}`), true, nil)
	first := awaitTiming(t, summaries)
	require.True(t, first.OutputComplete)
	require.Equal(t, "ws_turn", first.ObservationKind)
	require.Nil(t, first.HandlerDurationMS)
	next := session.Send(ctx, []byte(`{"type":"response.create","model":"test"}`))
	requesttrace.BindRoot(next, "second-business-root")
	session.Sent(next, nil)
	session.Written("response-2", []byte(`{"type":"response.output_text.delta","response_id":"response-2","delta":"hello"}`), false, nil)
	session.Terminal("response-2", false)
	session.Written("response-2", []byte(`{"type":"response.completed","response":{"id":"response-2"}}`), true, errors.New("client write failed"))
	second := awaitTiming(t, summaries)
	require.NotEqual(t, first.ObservationID, second.ObservationID)
	require.Equal(t, "second-business-root", second.RootRequestID)
	require.EqualValues(t, 7, second.UserID)
	require.False(t, second.OutputComplete)
	require.Equal(t, "incomplete", second.Outcome)
	if first.OTelTraceID != "" {
		require.NotEqual(t, first.OTelTraceID, second.OTelTraceID)
	}
	_, noTurn := server.startWSTimings(context.Background(), []byte(`{"type":"ping"}`))
	require.Nil(t, noTurn)
}

func TestWSTimingConnectionMilestonesAttachToFirstTurnOnly(t *testing.T) {
	exporter, summaries := timingSink(t)
	server := &HTTPServer{timingExporter: exporter}
	acceptStart := time.Now().Add(-2 * time.Second)
	acceptEnd := acceptStart.Add(120 * time.Millisecond)
	firstFrameAt := acceptEnd.Add(900 * time.Millisecond)
	ctx, session := server.startWSTimings(context.Background(), []byte(`{"type":"response.create","model":"test"}`))
	defer session.Close()
	session.ConnectionMilestones(acceptStart, acceptEnd, firstFrameAt)
	// Replaying the same milestones (e.g. a second caller) must not double-count.
	session.ConnectionMilestones(acceptStart, acceptEnd, firstFrameAt)
	session.Identity(7, "root")
	ctx = session.Send(ctx, []byte(`{"type":"response.create","model":"test"}`))
	session.Sent(ctx, nil)
	session.Terminal("response-1", false)
	session.Written("response-1", []byte(`{"type":"response.completed","response":{"id":"response-1"}}`), true, nil)
	first := awaitTiming(t, summaries)
	stagePhases := map[string]float64{}
	for _, stage := range first.Stages {
		stagePhases[stage.Phase] = stage.DurationMS
	}
	require.InDelta(t, 120, stagePhases["ws.accept"], 5)
	require.InDelta(t, 900, stagePhases["ws.first_frame"], 5)

	// The next turn carries no connection stages: handshake/dial are charged to
	// the turn in which they actually happened.
	next := session.Send(ctx, []byte(`{"type":"response.create","model":"test"}`))
	session.Sent(next, nil)
	session.Terminal("response-2", false)
	session.Written("response-2", []byte(`{"type":"response.completed","response":{"id":"response-2"}}`), true, nil)
	second := awaitTiming(t, summaries)
	for _, stage := range second.Stages {
		require.NotContains(t, []string{"ws.accept", "ws.first_frame"}, stage.Phase)
	}
	require.NoError(t, first.Validate())
	require.NoError(t, second.Validate())
}

func TestTimingBodyLimitEarlyReturnStillHasSummary(t *testing.T) {
	exporter, summaries := timingSink(t)
	server := &HTTPServer{timingExporter: exporter}
	called := false
	handler := server.wrapRoute("/v1/chat/completions", executionRoute("/v1/chat/completions", "legacy"), http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	request.ContentLength = jsonRequestBodyLimit + 1
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	require.False(t, called)
	require.Equal(t, 413, response.Code)
	summary := awaitTiming(t, summaries)
	require.Equal(t, "rejected", summary.Outcome)
	require.Nil(t, summary.HandlerDurationMS)
}

func TestWSTimingUpstreamTerminalAloneDoesNotCompleteClientOutput(t *testing.T) {
	exporter, summaries := timingSink(t)
	server := &HTTPServer{timingExporter: exporter}
	ctx, session := server.startWSTimings(context.Background(), []byte(`{"type":"response.create","model":"test"}`))
	ctx = session.Send(ctx, []byte(`{"type":"response.create","model":"test"}`))
	session.Sent(ctx, nil)
	session.Written("response", []byte(`{"type":"response.output_text.delta","delta":"hello"}`), false, nil)
	session.Terminal("response", false)
	// The connection exits after upstream terminal, before writing that frame.
	session.Close()
	summary := awaitTiming(t, summaries)
	require.False(t, summary.OutputComplete)
	require.Equal(t, "incomplete", summary.OutputOutcome)
}

func TestWSTimingFailoverReplaysFirstFrameInSameObservation(t *testing.T) {
	exporter, summaries := timingSink(t)
	server := &HTTPServer{timingExporter: exporter}
	firstFrame := []byte(`{"type":"response.create","model":"test"}`)
	ctx, session := server.startWSTimings(context.Background(), firstFrame)
	session.Identity(7, "first-root")
	ctx = requesttrace.WithAttempt(ctx, requesttrace.Attempt{RootRequestID: "first-root", Number: 1, SourceKind: "channel", SourceID: 1})
	client := newFakeFrameConn(8)
	first := newFakeFrameConn(8)
	first.in <- fakeFrame{err: errors.New("upstream reset")}
	_, exit := relayOpenAIWSFrames(ctx, client, first, firstFrame, openAIWSRelayOptions{timings: session})
	require.False(t, exit.wroteDownstream)
	ctx = requesttrace.WithAttempt(ctx, requesttrace.Attempt{RootRequestID: "first-root", Number: 2, SourceKind: "channel", SourceID: 2})
	second := newFakeFrameConn(8)
	second.in <- fakeFrame{msgType: coderws.MessageText, payload: []byte(`{"type":"response.completed","response":{"id":"response-1","status":"completed"}}`)}
	second.in <- fakeFrame{err: errors.New("upstream closed")}
	_, _ = relayOpenAIWSFrames(ctx, client, second, firstFrame, openAIWSRelayOptions{timings: session})
	session.Close()
	require.NoError(t, exporter.Close(context.Background()))
	require.EqualValues(t, 1, exporter.Stats().Accepted, "failover is another attempt of one inbound turn")
	summary := awaitTiming(t, summaries)
	require.Equal(t, "success", summary.Outcome)
	require.NotContains(t, summary.MissingReasons, "concurrent_ws_turns")
	require.Len(t, summary.Attempts, 2)
}

func TestWSTimingLaterTurnUsesItsOwnBusinessRoot(t *testing.T) {
	exporter, summaries := timingSink(t)
	server := &HTTPServer{timingExporter: exporter}
	frame := []byte(`{"type":"response.create","model":"test"}`)
	ctx, session := server.startWSTimings(context.Background(), frame)
	defer session.Close()
	session.Identity(7, "first-root")
	ctx = session.Send(ctx, frame)
	session.Sent(ctx, nil)
	session.Written("first", []byte(`{"type":"response.completed"}`), true, nil)
	_ = awaitTiming(t, summaries)
	next := session.Send(ctx, frame)
	// routingWSTurns.admit creates a new billing root for later requests.
	next = channelAttemptContext(next, "second-root", 1, nil, "test")
	session.Sent(next, nil)
	session.Written("second", []byte(`{"type":"response.completed"}`), true, nil)
	require.Equal(t, "second-root", awaitTiming(t, summaries).RootRequestID)
}

func TestTimingCancelledAndIncompleteSSETerminalsRetainOutcome(t *testing.T) {
	for _, outcome := range []string{"cancelled", "incomplete"} {
		t.Run(outcome, func(t *testing.T) {
			exporter, summaries := timingSink(t)
			server := &HTTPServer{timingExporter: exporter}
			handler := server.observeRequestTiming(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, `data: {"type":"response.`+outcome+`","response":{"status":"`+outcome+`"}}`+"\n\n")
				w.(http.Flusher).Flush()
			}), "/v1/responses", "legacy")
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
			summary := awaitTiming(t, summaries)
			require.True(t, summary.OutputComplete, "an unsuccessful terminal frame may still be fully written")
			require.Equal(t, outcome, summary.Outcome)
		})
	}
}

func TestWSTimingPreservesCurrentRelayCancellationForAdmission(t *testing.T) {
	exporter, _ := timingSink(t)
	server := &HTTPServer{timingExporter: exporter}
	frame := []byte(`{"type":"response.create","model":"test"}`)
	ctx, session := server.startWSTimings(context.Background(), frame)
	defer session.Close()
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	var admissionErr error
	_, exit := relayOpenAIWSFrames(ctx, newFakeFrameConn(8), newFakeFrameConn(8), frame, openAIWSRelayOptions{
		timings: session,
		beforeWriteUp: func(ctx context.Context, _ coderws.MessageType, payload []byte) ([]byte, error) {
			admissionErr = ctx.Err()
			return nil, context.Canceled
		},
	})
	require.ErrorIs(t, exit.err, context.Canceled)
	require.ErrorIs(t, admissionErr, context.Canceled, "timing context must retain current relay cancellation")
}

func TestTimingRequestedStreamReturningJSONIsCompletedHTTPOutput(t *testing.T) {
	exporter, summaries := timingSink(t)
	server := &HTTPServer{timingExporter: exporter}
	handler := server.observeRequestTiming(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveObservedRelay(w, r, "/v1/responses", "legacy", "true", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"error":"forbidden"}`)
		})
	}), "/v1/responses", "legacy")
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
	summary := awaitTiming(t, summaries)
	require.Equal(t, "http", summary.Protocol)
	require.True(t, summary.OutputComplete)
	require.Equal(t, "rejected", summary.Outcome)
}
