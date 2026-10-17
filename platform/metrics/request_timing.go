package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"micro-one-api/domain/requesttrace"
)

var RequestTimingExportTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "relay_request_timing_export_total", Help: "Request timing summary queue/export outcomes.",
}, []string{"result"})

var TraceQueueDroppedTotal = prometheus.NewCounter(prometheus.CounterOpts{Name: "otel_trace_queue_dropped_total", Help: "Spans dropped by the SDK batch queue."})
var TraceInvalidCarriers = prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "relay_async_trace_carrier_invalid_total", Help: "Invalid task trace carriers; financial processing is unaffected."}, func() float64 { return float64(requesttrace.InvalidCarriers()) })
var TraceExportSpansTotal = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "otel_trace_export_spans_total", Help: "Spans passed to the OTLP exporter."}, []string{"result"})
var RequestTimingStageDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "relay_request_stage_duration_seconds", Help: "Bounded relay phase duration, overlapping phases must not be added.", Buckets: prometheus.ExponentialBuckets(.001, 4, 10)}, []string{"phase"})

func init() {
	prometheus.MustRegister(RequestTimingExportTotal, TraceQueueDroppedTotal, TraceExportSpansTotal, RequestTimingStageDuration, TraceInvalidCarriers)
}
