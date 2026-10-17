package requesttrace

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type recorderKey struct{}

// Recorder is shared by derived contexts. Finished snapshots are immutable;
// detached tasks may keep emitting spans without changing the request summary.
type Recorder struct {
	mu             sync.Mutex
	started        time.Time
	finished       bool
	summary        TimingSummary
	span           trace.Span
	lastWrite      time.Time
	writeFailed    bool
	terminal       bool
	stages         map[string]TimingStage
	activeAttempts map[int32]timedAttempt
	activeNumber   int32
	parseSpan      trace.Span
	parseStarted   time.Time
	outputSpan     trace.Span
	outputStarted  time.Time
}
type timedAttempt struct {
	ctx     context.Context
	span    trace.Span
	started time.Time
}

func Observation(ctx context.Context) *Recorder {
	r, _ := ctx.Value(recorderKey{}).(*Recorder)
	return r
}

func StartObservation(ctx context.Context, kind, endpoint, protocol, path string, newRoot bool) (context.Context, *Recorder) {
	started := time.Now()
	options := []trace.SpanStartOption{trace.WithSpanKind(trace.SpanKindServer), trace.WithTimestamp(started)}
	if newRoot {
		options = append(options, trace.WithNewRoot())
		if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
			options = append(options, trace.WithLinks(trace.Link{SpanContext: sc}))
		}
		ctx = trace.ContextWithSpanContext(ctx, trace.SpanContext{})
	}
	ctx, span := otel.Tracer("micro-one-api/relay").Start(ctx, "relay.request", options...)
	id := uuid.NewString()
	r := &Recorder{started: started, span: span, stages: make(map[string]TimingStage), summary: TimingSummary{
		Version: TimingVersion, ObservationID: id, ObservationKind: kind, Endpoint: endpoint,
		Protocol: protocol, ExecutionPath: path, Outcome: "unknown", StartedAtMS: started.UnixMilli(), CollectionState: "not_recorded",
	}}
	if sc := span.SpanContext(); sc.IsValid() {
		r.summary.OTelTraceID, r.summary.SpanID = sc.TraceID().String(), sc.SpanID().String()
		if span.IsRecording() {
			r.summary.CollectionState = "recorded"
		}
	}
	span.SetAttributes(attribute.String("observation.id", id), attribute.String("relay.endpoint", endpoint), attribute.String("relay.protocol", protocol))
	if kind == "http" || kind == "ws_turn" {
		_, r.parseSpan = otel.Tracer("micro-one-api/relay").Start(ctx, "request.parse")
		r.parseStarted = started
	}
	return context.WithValue(ctx, recorderKey{}, r), r
}

func (r *Recorder) Update(fn func(*TimingSummary)) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.finished {
		fn(&r.summary)
	}
}

func BindUser(ctx context.Context, userID int64) {
	if userID <= 0 {
		return
	}
	r := Observation(ctx)
	r.Update(func(s *TimingSummary) { s.UserID = userID })
}

func BindRoot(ctx context.Context, root string) {
	if root == "" {
		return
	}
	Observation(ctx).Update(func(s *TimingSummary) {
		if s.RootRequestID == "" {
			s.RootRequestID = root
		}
	})
}

func RecordAttempt(ctx context.Context, attempt TimingAttempt) {
	if attempt.Number <= 0 {
		attempt.Number = 1
	}
	if attempt.Outcome == "" {
		attempt.Outcome = "unknown"
	}
	Observation(ctx).Update(func(s *TimingSummary) {
		for i := range s.Attempts {
			if s.Attempts[i].Number == attempt.Number {
				a := &s.Attempts[i]
				if attempt.RequestID != "" {
					a.RequestID = attempt.RequestID
				}
				if attempt.ReservationID != nil {
					a.ReservationID = clonePointer(attempt.ReservationID)
				}
				if attempt.SourceKind != "" {
					a.SourceKind, a.SourceID = attempt.SourceKind, attempt.SourceID
				}
				if attempt.UpstreamModelID != "" {
					a.UpstreamModelID = attempt.UpstreamModelID
				}
				if attempt.Outcome != "unknown" {
					a.Outcome = attempt.Outcome
				}
				return
			}
		}
		if len(s.Attempts) < 128 {
			s.Attempts = append(s.Attempts, attempt)
		} else {
			s.CollectionState = "partial"
			s.MissingReasons = appendUnique(s.MissingReasons, "attempt_limit")
		}
	})
}

func Outcome(err error) string {
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if err != nil {
		return "failed"
	}
	return "success"
}

// Phase never puts error text, user IDs or request content into span tags.
func Phase(ctx context.Context, name string) (context.Context, func(error)) {
	if !ValidTimingPhase(name) {
		return ctx, func(error) {}
	}
	started := time.Now()
	if name == "relay.plan" || name == "auth.snapshot" {
		Observation(ctx).EndParse(nil)
	}
	if name != "relay.plan" && name != "auth.snapshot" {
		if r := Observation(ctx); r != nil {
			r.mu.Lock()
			number := FromContext(ctx).Number
			if number == 0 {
				number = r.activeNumber
			}
			if attempt, ok := r.activeAttempts[number]; ok && trace.SpanContextFromContext(ctx).SpanID() == r.span.SpanContext().SpanID() {
				ctx = trace.ContextWithSpan(ctx, attempt.span)
			}
			r.mu.Unlock()
		}
	}
	ctx, span := otel.Tracer("micro-one-api/relay").Start(ctx, name)
	if a := FromContext(ctx); a.Number > 0 {
		span.SetAttributes(attribute.Int("relay.attempt.number", int(a.Number)))
	}
	var once sync.Once
	return ctx, func(err error) {
		once.Do(func() {
			outcome := Outcome(err)
			span.SetAttributes(attribute.String("outcome", outcome))
			if err != nil {
				span.SetStatus(codes.Error, outcome)
			}
			span.End()
			if r := Observation(ctx); r != nil {
				r.mu.Lock()
				defer r.mu.Unlock()
				if !r.finished {
					stage := r.stages[name]
					stage.Phase = name
					stage.Count++
					stage.DurationMS += float64(time.Since(started)) / float64(time.Millisecond)
					r.stages[name] = stage
				}
			}
		})
	}
}

// AddElapsedStage records a phase that was measured before the observation
// existed (e.g. the WS handshake and first-frame wait, which happen before the
// first turn's recorder is created). It appends a backdated child span under
// the request root and accumulates the stage into the summary. The phase must
// be in the fixed timingPhases vocabulary. Call it at most once per phase per
// observation; later phases measured while the observation is live must use
// Phase instead.
func (r *Recorder) AddElapsedStage(name string, startedAt, endedAt time.Time, err error) {
	if r == nil || !ValidTimingPhase(name) || startedAt.IsZero() || endedAt.Before(startedAt) {
		return
	}
	r.mu.Lock()
	if r.finished {
		r.mu.Unlock()
		return
	}
	root := r.span
	r.mu.Unlock()
	_, span := otel.Tracer("micro-one-api/relay").Start(
		trace.ContextWithSpan(context.Background(), root), name,
		trace.WithTimestamp(startedAt))
	outcome := Outcome(err)
	span.SetAttributes(attribute.String("outcome", outcome))
	if err != nil {
		span.SetStatus(codes.Error, outcome)
	}
	span.End(trace.WithTimestamp(endedAt))
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return
	}
	stage := r.stages[name]
	stage.Phase = name
	stage.Count++
	stage.DurationMS += float64(endedAt.Sub(startedAt)) / float64(time.Millisecond)
	r.stages[name] = stage
}

func (r *Recorder) Write(n int, err error) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return
	}
	if err != nil {
		r.writeFailed = true
		r.summary.OutputOutcome = "write_failed"
	}
	if n > 0 {
		r.lastWrite = time.Now()
	}
}

func (r *Recorder) Content(kind string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished || r.summary.FirstContentMS != nil {
		return
	}
	v := float64(time.Since(r.started)) / float64(time.Millisecond)
	r.summary.FirstContentMS = &v
	r.span.AddEvent("output.first_content", trace.WithAttributes(attribute.String("output.kind", kind)))
}

func (r *Recorder) Terminal(success bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return
	}
	r.terminal = true
	if !success && r.summary.Outcome != "incomplete" {
		r.summary.Outcome = "failed"
	}
	r.span.AddEvent("output.terminal")
}

func (r *Recorder) UpstreamTerminal(failed bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return
	}
	if failed && r.summary.Outcome != "incomplete" {
		r.summary.Outcome = "failed"
	}
	r.span.AddEvent("upstream.terminal")
}

func (r *Recorder) BeginOutput(start time.Time) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished || r.outputSpan != nil || r.summary.ObservationKind != "http" {
		return
	}
	_, r.outputSpan = otel.Tracer("micro-one-api/relay").Start(trace.ContextWithSpan(context.Background(), r.span), "response.stream", trace.WithTimestamp(start))
	r.outputStarted = start
}

func (r *Recorder) HandlerFinished(start time.Time) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return
	}
	now := time.Now()
	end, duration := now.UnixMilli(), float64(now.Sub(start))/1e6
	r.summary.HandlerFinishedAtMS = &end
	r.summary.HandlerDurationMS = &duration
	r.span.AddEvent("handler.finished", trace.WithTimestamp(now))
}

func (r *Recorder) Finish(outcome string) *TimingSummary {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return nil
	}
	r.finished = true
	var parseErr error
	if outcome != "success" {
		parseErr = errors.New(outcome)
	}
	r.endParseLocked(parseErr)
	for number := range r.activeAttempts {
		r.finishAttemptLocked(number, outcome)
	}
	now := time.Now()
	end := now.UnixMilli()
	duration := float64(now.Sub(r.started)) / float64(time.Millisecond)
	s := &r.summary
	if s.Outcome == "unknown" {
		s.Outcome = outcome
	}
	if outcome == "cancelled" {
		s.Outcome = "cancelled"
	}
	switch s.ObservationKind {
	case "http": // Handler timing is measured separately, only when invoked.
	case "ws_turn":
		s.TurnFinishedAtMS = &end
		s.TurnDurationMS = &duration
	case "grpc":
		s.RPCFinishedAtMS = &end
		s.RPCDurationMS = &duration
	}
	if !r.lastWrite.IsZero() && s.ObservationKind != "grpc" {
		written := r.lastWrite.UnixMilli()
		output := float64(r.lastWrite.Sub(r.started)) / float64(time.Millisecond)
		s.OutputFinishedAtMS, s.OutputDurationMS = &written, &output
		s.OutputComplete = !r.writeFailed && (s.Protocol == "http" || r.terminal)
		if s.OutputOutcome == "" {
			if s.OutputComplete {
				s.OutputOutcome = "complete"
			} else {
				s.OutputOutcome = "incomplete"
			}
		}
		if !s.OutputComplete && s.Outcome == "success" {
			s.Outcome = "incomplete"
		}
	}
	if r.writeFailed && s.Outcome == "success" {
		s.Outcome = "incomplete"
	}
	if r.outputSpan != nil {
		finished := r.lastWrite
		if finished.IsZero() {
			finished = now
		}
		outputOutcome := "success"
		if !s.OutputComplete {
			outputOutcome = "incomplete"
		}
		r.outputSpan.SetAttributes(attribute.String("outcome", outputOutcome))
		if !s.OutputComplete {
			r.outputSpan.SetStatus(codes.Error, "incomplete")
		}
		r.outputSpan.End(trace.WithTimestamp(finished))
		stage := r.stages["response.stream"]
		stage.Phase = "response.stream"
		stage.Count++
		stage.DurationMS += float64(finished.Sub(r.outputStarted)) / 1e6
		r.stages[stage.Phase] = stage
	}
	if s.ObservationKind == "ws_turn" {
		r.span.AddEvent("turn.finished", trace.WithTimestamp(now))
	}
	if s.ObservationKind == "grpc" {
		r.span.AddEvent("rpc.finished", trace.WithTimestamp(now))
	}
	for _, stage := range r.stages {
		s.Stages = append(s.Stages, stage)
	}
	sort.Slice(s.Stages, func(i, j int) bool { return s.Stages[i].Phase < s.Stages[j].Phase })
	sort.Slice(s.Attempts, func(i, j int) bool { return s.Attempts[i].Number < s.Attempts[j].Number })
	r.span.SetAttributes(attribute.String("outcome", s.Outcome), attribute.String("relay.execution_path", s.ExecutionPath))
	if !r.lastWrite.IsZero() {
		r.span.AddEvent("output.finished", trace.WithTimestamp(r.lastWrite))
	}
	if s.Outcome != "success" {
		r.span.SetStatus(codes.Error, s.Outcome)
	}
	r.span.End(trace.WithTimestamp(now))
	return s.Clone()
}

func (r *Recorder) EndParse(err error) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.endParseLocked(err)
}
func (r *Recorder) endParseLocked(err error) {
	if r.parseSpan == nil {
		return
	}
	if err != nil {
		r.parseSpan.SetStatus(codes.Error, Outcome(err))
	}
	r.parseSpan.End()
	r.parseSpan = nil
	stage := r.stages["request.parse"]
	stage.Phase = "request.parse"
	stage.Count++
	stage.DurationMS += float64(time.Since(r.parseStarted)) / 1e6
	r.stages[stage.Phase] = stage
}

func EnsureAttempt(ctx context.Context, number int32) context.Context {
	r := Observation(ctx)
	if r == nil {
		return ctx
	}
	if number <= 0 {
		number = 1
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return ctx
	}
	r.endParseLocked(nil)
	if r.activeAttempts == nil {
		r.activeAttempts = map[int32]timedAttempt{}
	}
	if current, ok := r.activeAttempts[number]; ok {
		return trace.ContextWithSpan(ctx, current.span)
	}
	started := time.Now()
	parent := trace.ContextWithSpan(ctx, r.span)
	child, span := otel.Tracer("micro-one-api/relay").Start(parent, "relay.attempt", trace.WithAttributes(attribute.Int("relay.attempt.number", int(number))))
	r.activeAttempts[number] = timedAttempt{child, span, started}
	r.activeNumber = number
	return trace.ContextWithSpan(ctx, span)
}
func FinishAttempt(ctx context.Context, number int32, outcome string) {
	r := Observation(ctx)
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if number <= 0 {
		number = r.activeNumber
	}
	r.finishAttemptLocked(number, outcome)
}

func AttemptNumberForReservation(ctx context.Context, reservation string) int32 {
	r := Observation(ctx)
	if r == nil {
		return FromContext(ctx).Number
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, attempt := range r.summary.Attempts {
		if attempt.ReservationID != nil && *attempt.ReservationID == reservation {
			return attempt.Number
		}
	}
	if number := FromContext(ctx).Number; number > 0 {
		return number
	}
	return r.activeNumber
}
func (r *Recorder) finishAttemptLocked(number int32, outcome string) {
	attempt, ok := r.activeAttempts[number]
	if !ok {
		return
	}
	attempt.span.SetAttributes(attribute.String("outcome", outcome))
	if outcome != "success" {
		attempt.span.SetStatus(codes.Error, outcome)
	}
	attempt.span.End()
	stage := r.stages["relay.attempt"]
	stage.Phase = "relay.attempt"
	stage.Count++
	stage.DurationMS += float64(time.Since(attempt.started)) / float64(time.Millisecond)
	r.stages[stage.Phase] = stage
	delete(r.activeAttempts, number)
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func Missing(ctx context.Context, reason string) {
	if !validReasonCode(reason) {
		return
	}
	Observation(ctx).Update(func(summary *TimingSummary) {
		if summary.CollectionState != "not_recorded" {
			summary.CollectionState = "partial"
		}
		if len(summary.MissingReasons) < 32 {
			summary.MissingReasons = appendUnique(summary.MissingReasons, reason)
		}
	})
}

func (r *Recorder) Missing(reason string) {
	if !validReasonCode(reason) {
		return
	}
	r.Update(func(s *TimingSummary) {
		if s.CollectionState != "not_recorded" {
			s.CollectionState = "partial"
		}
		if len(s.MissingReasons) < 32 {
			s.MissingReasons = appendUnique(s.MissingReasons, reason)
		}
	})
}
