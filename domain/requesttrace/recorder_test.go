package requesttrace

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"sync"
	"testing"
	"time"
)

func TestObservationIsolationAttemptLifetimeAndFrozenSnapshot(t *testing.T) {
	previous := otel.GetTracerProvider()
	spans := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans), sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())))
	otel.SetTracerProvider(provider)
	defer otel.SetTracerProvider(previous)
	id, _ := trace.TraceIDFromHex("11111111111111111111111111111111")
	parent, _ := trace.SpanIDFromHex("1111111111111111")
	ctx := trace.ContextWithRemoteSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: id, SpanID: parent, Remote: true}))
	ctx, r := StartObservation(ctx, "http", "/v1/responses", "sse", "legacy", true)
	BindUser(ctx, 7)
	BindRoot(ctx, "business-root")
	ctx = WithAttempt(ctx, Attempt{RootRequestID: "business-root", Number: 1, SourceKind: "channel"})
	reservation := "reservation"
	RecordAttempt(ctx, TimingAttempt{Number: 1, RequestID: "attempt", ReservationID: &reservation})
	upstream, end := Phase(ctx, "upstream.http")
	_ = upstream
	end(nil)
	for _, span := range spans.Ended() {
		require.NotEqual(t, "relay.attempt", span.Name(), "attempt cannot finish just because an upstream handle returned")
	}
	r.Write(10, nil)
	r.Content("text")
	r.Terminal(true)
	FinishAttempt(ctx, 1, "success")
	summary := r.Finish("success")
	require.NoError(t, summary.Validate())
	require.NotEqual(t, id.String(), summary.OTelTraceID)
	require.NotNil(t, summary.FirstContentMS)
	require.True(t, summary.OutputComplete)
	require.Nil(t, r.Finish("failed"))
	BindUser(ctx, 99)
	require.EqualValues(t, 7, summary.UserID)
	var root, attempt sdktrace.ReadOnlySpan
	for _, span := range spans.Ended() {
		switch span.Name() {
		case "relay.request":
			root = span
		case "relay.attempt":
			attempt = span
		}
	}
	require.NotNil(t, root)
	require.NotNil(t, attempt)
	require.Equal(t, root.SpanContext().SpanID(), attempt.Parent().SpanID())
	require.True(t, root.SpanContext().IsSampled())
}

func TestAddElapsedStageBackdatesSpanAndAccumulatesStage(t *testing.T) {
	previous := otel.GetTracerProvider()
	spans := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans), sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())))
	otel.SetTracerProvider(provider)
	defer otel.SetTracerProvider(previous)

	_, r := StartObservation(context.Background(), "ws_turn", "/v1/responses", "websocket", "legacy", true)
	started := time.Now()
	// Handshake and first-frame wait were measured before the turn observation
	// existed; they attach as backdated stages of the first turn.
	acceptStart := started.Add(-200 * time.Millisecond)
	acceptEnd := started.Add(-100 * time.Millisecond)
	firstFrameAt := started.Add(-5 * time.Millisecond)
	r.AddElapsedStage("ws.accept", acceptStart, acceptEnd, nil)
	r.AddElapsedStage("ws.first_frame", acceptEnd, firstFrameAt, nil)
	// Invalid inputs are ignored rather than corrupting the summary.
	r.AddElapsedStage("ws.accept", time.Time{}, acceptEnd, nil)
	r.AddElapsedStage("not-a-phase", acceptStart, acceptEnd, nil)
	r.AddElapsedStage("ws.accept", acceptEnd, acceptStart, nil)

	summary := r.Finish("success")
	require.NoError(t, summary.Validate())
	byPhase := map[string]TimingStage{}
	for _, stage := range summary.Stages {
		byPhase[stage.Phase] = stage
	}
	require.EqualValues(t, 1, byPhase["ws.accept"].Count)
	require.InDelta(t, 100, byPhase["ws.accept"].DurationMS, 1)
	require.EqualValues(t, 1, byPhase["ws.first_frame"].Count)
	require.InDelta(t, 95, byPhase["ws.first_frame"].DurationMS, 1)

	var root, accept sdktrace.ReadOnlySpan
	for _, span := range spans.Ended() {
		switch span.Name() {
		case "relay.request":
			root = span
		case "ws.accept":
			accept = span
		}
	}
	require.NotNil(t, root)
	require.NotNil(t, accept)
	require.Equal(t, root.SpanContext().SpanID(), accept.Parent().SpanID())
	require.Equal(t, root.SpanContext().TraceID(), accept.SpanContext().TraceID())
	require.Equal(t, acceptStart.UnixNano(), accept.StartTime().UnixNano(), "backdated span keeps the measured start")
	require.Equal(t, acceptEnd.UnixNano(), accept.EndTime().UnixNano())

	// Stages recorded after Finish must not mutate the exported snapshot.
	r.AddElapsedStage("ws.accept", acceptStart, acceptEnd, nil)
	require.EqualValues(t, 1, byPhase["ws.accept"].Count)
}

func TestNewPhaseNamesAreWhitelisted(t *testing.T) {
	for _, phase := range []string{"ws.accept", "ws.first_frame", "ws.pool.acquire", "ws.upstream.dial", "cache.l1"} {
		require.True(t, ValidTimingPhase(phase), phase)
	}
}

func TestExporterSaturationIsNonblockingAndShutdownIsBounded(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	exporter := NewExporter(func(ctx context.Context, s *TimingSummary) error {
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}, nil)
	ctx, recorder := StartObservation(context.Background(), "http", "/v1/responses", "http", "legacy", true)
	summary := recorder.Finish("rejected")
	require.True(t, exporter.Submit(ctx, summary))
	require.True(t, exporter.Submit(ctx, summary))
	<-started
	<-started
	for range 256 {
		require.True(t, exporter.Submit(ctx, summary))
	}
	done := make(chan bool, 1)
	go func() { done <- exporter.Submit(ctx, summary) }()
	select {
	case accepted := <-done:
		require.False(t, accepted)
	case <-time.After(time.Second):
		t.Fatal("full exporter blocked the model request")
	}
	closeCtx, cancel := context.WithCancel(context.Background())
	cancel()
	require.Error(t, exporter.Close(closeCtx))
	close(release)
	require.False(t, exporter.Submit(ctx, summary))
	require.EqualValues(t, 2, exporter.Stats().Dropped)
	ctxClose, finish := context.WithTimeout(context.Background(), time.Second)
	defer finish()
	require.NoError(t, exporter.Close(ctxClose))
}

func TestRecorderConcurrentBindingsAndInvalidCarrierNeverRejectsTasks(t *testing.T) {
	ctx, r := StartObservation(context.Background(), "http", "/v1/responses", "http", "raw", true)
	var wg sync.WaitGroup
	for i := 1; i <= 12; i++ {
		number := int32(i)
		wg.Go(func() {
			BindUser(ctx, 7)
			RecordAttempt(ctx, TimingAttempt{Number: number, Outcome: "rejected"})
			_, end := Phase(ctx, "admission.check")
			end(errors.New("local rejection"))
		})
	}
	wg.Wait()
	summary := r.Finish("rejected")
	require.NoError(t, summary.Validate())
	require.Len(t, summary.Attempts, 12)
	_, valid := Restore(context.Background(), Carrier{TraceParent: "damaged"})
	require.False(t, valid)
	_, end := StartTask(context.Background(), Carrier{TraceParent: "damaged"}, "settlement.worker")
	end(nil)
}

func TestContentProbeIgnoresControlFramesAndBoundsMemory(t *testing.T) {
	p := ContentProbe{}
	p.Feed([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n: ping\n\n"))
	require.Empty(t, p.Kind)
	p.Feed([]byte("data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"reason\"}}]}\n\n"))
	require.Equal(t, "reasoning", p.Kind)
	p.Feed([]byte("data: [DONE]\n\n"))
	require.True(t, p.Terminal)
	p = ContentProbe{}
	p.Feed(make([]byte, 128<<10))
	require.True(t, p.Truncated)
	require.LessOrEqual(t, len(p.buffer), 64<<10)
	p = ContentProbe{}
	p.Frame([]byte(`{"type":"response.completed","response":{"status":"failed","error":{"code":"failed"}}}`))
	require.True(t, p.Terminal)
	require.True(t, p.Failed)
}

func TestFailedBusinessTerminalCanStillBeCompletelyWritten(t *testing.T) {
	_, recorder := StartObservation(context.Background(), "http", "/v1/responses", "sse", "legacy", true)
	recorder.Write(100, nil)
	recorder.Terminal(false)
	summary := recorder.Finish("success")
	require.NoError(t, summary.Validate())
	require.Equal(t, "failed", summary.Outcome)
	require.True(t, summary.OutputComplete)
	require.Equal(t, "complete", summary.OutputOutcome)
}

func TestContentProbeRecognizesCompletionTextAndCancellation(t *testing.T) {
	p := ContentProbe{}
	p.Feed([]byte("data: {\"choices\":[{\"text\":\"hello\"}]}\n\ndata: [DONE]\n\n"))
	require.Equal(t, "text", p.Kind)
	require.True(t, p.Terminal)
	for _, eventType := range []string{"response.cancelled", "response.canceled"} {
		p = ContentProbe{}
		p.Frame([]byte(`{"type":"` + eventType + `","response":{"status":"cancelled"}}`))
		require.True(t, p.Terminal, eventType)
		require.True(t, p.Failed, eventType)
	}
}
