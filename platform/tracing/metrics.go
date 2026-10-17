package xtrace

import (
	"context"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"micro-one-api/domain/requesttrace"
	"micro-one-api/platform/metrics"
)

// The application exports Prometheus metrics directly. Bridge the SDK's own
// queue-full counter into that registry without exporting dynamic processor,
// trace, user or request identifiers as metric labels.
type sdkMetricsProvider struct{ metric.MeterProvider }

func newSDKMetricsProvider() metric.MeterProvider { return sdkMetricsProvider{noop.NewMeterProvider()} }
func (p sdkMetricsProvider) Meter(name string, options ...metric.MeterOption) metric.Meter {
	meter := p.MeterProvider.Meter(name, options...)
	if name == "go.opentelemetry.io/otel/sdk/trace/internal/observ" {
		return sdkMetricsMeter{meter}
	}
	return meter
}

type sdkMetricsMeter struct{ metric.Meter }

func (m sdkMetricsMeter) Int64Counter(name string, options ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	counter, err := m.Meter.Int64Counter(name, options...)
	if name == "otel.sdk.processor.span.processed" {
		return sdkQueueCounter{counter}, err
	}
	return counter, err
}

type sdkQueueCounter struct{ metric.Int64Counter }

func (c sdkQueueCounter) Add(ctx context.Context, n int64, options ...metric.AddOption) {
	config := metric.NewAddConfig(options)
	attributes := config.Attributes()
	if value, ok := attributes.Value(attribute.Key("error.type")); ok && value.AsString() == "queue_full" {
		metrics.TraceQueueDroppedTotal.Add(float64(n))
	}
}

type measuredExporter struct{ sdktrace.SpanExporter }

func (e measuredExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	err := e.SpanExporter.ExportSpans(ctx, spans)
	result := "success"
	if err != nil {
		result = "error"
	}
	metrics.TraceExportSpansTotal.WithLabelValues(result).Add(float64(len(spans)))
	return err
}

type phaseMetricProcessor struct{}

func (phaseMetricProcessor) OnStart(context.Context, sdktrace.ReadWriteSpan) {}
func (phaseMetricProcessor) OnEnd(span sdktrace.ReadOnlySpan) {
	if requesttrace.ValidTimingPhase(span.Name()) {
		metrics.RequestTimingStageDuration.WithLabelValues(span.Name()).Observe(span.EndTime().Sub(span.StartTime()).Seconds())
	}
}
func (phaseMetricProcessor) Shutdown(context.Context) error   { return nil }
func (phaseMetricProcessor) ForceFlush(context.Context) error { return nil }
