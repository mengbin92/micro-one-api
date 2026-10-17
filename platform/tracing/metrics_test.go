package xtrace

import (
	"context"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"micro-one-api/platform/metrics"
	"testing"
	"time"
)

type blockedTraceExporter struct {
	entered chan struct{}
	release chan struct{}
}

func (e *blockedTraceExporter) ExportSpans(ctx context.Context, _ []sdktrace.ReadOnlySpan) error {
	select {
	case e.entered <- struct{}{}:
	default:
	}
	select {
	case <-e.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (*blockedTraceExporter) Shutdown(context.Context) error { return nil }

func TestSDKQueueDropsReachPrometheusWithoutBlocking(t *testing.T) {
	t.Setenv("OTEL_GO_X_OBSERVABILITY", "true")
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(newSDKMetricsProvider())
	defer otel.SetMeterProvider(previous)
	exporter := &blockedTraceExporter{make(chan struct{}, 1), make(chan struct{})}
	processor := sdktrace.NewBatchSpanProcessor(exporter, sdktrace.WithMaxQueueSize(1), sdktrace.WithMaxExportBatchSize(1), sdktrace.WithBatchTimeout(time.Hour))
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(processor))
	_, span := provider.Tracer("test").Start(context.Background(), "first")
	span.End()
	<-exporter.entered
	before := testutil.ToFloat64(metrics.TraceQueueDroppedTotal)
	for range 20 {
		_, span = provider.Tracer("test").Start(context.Background(), "queued")
		span.End()
	}
	require.Greater(t, testutil.ToFloat64(metrics.TraceQueueDroppedTotal), before)
	close(exporter.release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, provider.Shutdown(ctx))
}
