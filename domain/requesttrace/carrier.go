package requesttrace

import (
	"context"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"sync/atomic"
	"time"
)

// Carrier belongs to task envelopes/columns, never financial event JSON.
type Carrier struct {
	ObservationID string
	TraceParent   string
	TraceState    string
	AcceptedAtMS  int64
	QueuedAtMS    int64
}
type carrierKey struct{}

var invalidCarriers atomic.Int64

func InvalidCarriers() int64 { return invalidCarriers.Load() }

func Capture(ctx context.Context) Carrier {
	if ctx == nil {
		ctx = context.Background()
	}
	c, _ := ctx.Value(carrierKey{}).(Carrier)
	if r := Observation(ctx); r != nil {
		r.mu.Lock()
		c.ObservationID = r.summary.ObservationID
		r.mu.Unlock()
	}
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	c.TraceParent, c.TraceState = carrier.Get("traceparent"), carrier.Get("tracestate")
	now := time.Now().UnixMilli()
	if c.AcceptedAtMS == 0 {
		c.AcceptedAtMS = now
	}
	c.QueuedAtMS = now
	return c
}

func Restore(ctx context.Context, c Carrier) (context.Context, bool) {
	if c.TraceParent == "" {
		return context.WithValue(ctx, carrierKey{}, c), true
	}
	if len(c.TraceParent) > 128 || len(c.TraceState) > 512 {
		invalidCarriers.Add(1)
		return ctx, false
	}
	restored := propagation.TraceContext{}.Extract(trace.ContextWithSpanContext(ctx, trace.SpanContext{}), propagation.MapCarrier{"traceparent": c.TraceParent, "tracestate": c.TraceState})
	if !trace.SpanContextFromContext(restored).IsValid() {
		invalidCarriers.Add(1)
		return ctx, false
	}
	return context.WithValue(restored, carrierKey{}, c), true
}

func StartTask(ctx context.Context, c Carrier, name string) (context.Context, func(error)) {
	ctx, valid := Restore(ctx, c)
	ctx, span := otel.Tracer("micro-one-api/tasks").Start(ctx, name)
	span.SetAttributes(attribute.Bool("telemetry.carrier.valid", valid))
	if c.ObservationID != "" {
		span.SetAttributes(attribute.String("observation.id", c.ObservationID))
	}
	if c.QueuedAtMS > 0 {
		span.SetAttributes(attribute.Float64("queue.wait_ms", float64(time.Now().UnixMilli()-c.QueuedAtMS)))
	}
	return ctx, func(err error) { span.SetAttributes(attribute.String("outcome", Outcome(err))); span.End() }
}

func WithObservationID(ctx context.Context, values []string) context.Context {
	if len(values) != 1 {
		return ctx
	}
	id, err := uuid.Parse(values[0])
	if err != nil || id.String() != values[0] {
		return ctx
	}
	c, _ := ctx.Value(carrierKey{}).(Carrier)
	c.ObservationID = values[0]
	return context.WithValue(ctx, carrierKey{}, c)
}
