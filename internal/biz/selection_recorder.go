package biz

import (
	"context"
	"time"

	"go.uber.org/zap"

	applogger "micro-one-api/platform/logging"
	"micro-one-api/platform/metrics"
)

// ── v0.11.0 Phase 3 §3.5: metrics + structured-log selection recorder ──────
//
// MetricsSelectionRecorder implements SelectionRecorder. It emits Prometheus
// metrics (low-cardinality labels only) AND a structured log entry (full
// detail including channel/account/model ids). It is the production recorder
// wired by the server layer via RelayUsecase.SetSelectionRecorder.

// MetricsSelectionRecorder turns SelectionEvents into Prometheus metrics and
// structured log entries.
type MetricsSelectionRecorder struct {
	log *zap.Logger
}

// NewMetricsSelectionRecorder creates a production recorder. log may be nil;
// it falls back to the app logger.
func NewMetricsSelectionRecorder(log *zap.Logger) *MetricsSelectionRecorder {
	if log == nil {
		log = applogger.Log
	}
	return &MetricsSelectionRecorder{log: log}
}

// RecordSelection emits the Prometheus metrics and the structured log entry.
// It never returns an error and never panics: the hot path must not block on
// observability.
func (r *MetricsSelectionRecorder) RecordSelection(ctx context.Context, event SelectionEvent) {
	if r == nil {
		return
	}
	sourceKind := event.FinalKind
	if sourceKind == "" {
		sourceKind = "unknown"
	}
	result := event.Result
	if result == "" {
		// A selection event without an execution result is a successful
		// selection (the execution boundary may fill Result later).
		result = "success"
	}

	// ── Prometheus metrics (low-cardinality labels only) ──
	if metrics.RoutingSelectionTotal != nil {
		metrics.RoutingSelectionTotal.WithLabelValues(sourceKind, result, event.ProviderFamily).Inc()
	}
	if event.StickyHit && metrics.RoutingStickyHitTotal != nil {
		metrics.RoutingStickyHitTotal.WithLabelValues(event.ProviderFamily).Inc()
	}
	if event.Fallback && metrics.RoutingFallbackTotal != nil {
		reason := event.FallbackReason
		if reason == "" {
			reason = "unknown"
		}
		metrics.RoutingFallbackTotal.WithLabelValues(reason, event.ProviderFamily).Inc()
	}
	if event.ElapsedMS > 0 && metrics.RoutingSelectionDuration != nil {
		metrics.RoutingSelectionDuration.WithLabelValues(sourceKind).
			Observe(float64(event.ElapsedMS) / 1000.0)
	}

	// ── Structured log (full detail, including high-cardinality ids) ──
	if r.log != nil {
		fields := []zap.Field{
			zap.String("request_id", event.RequestID),
			zap.String("group", event.Group),
			zap.String("model", event.Model),
			zap.String("source_kind", sourceKind),
			zap.Int64("source_id", event.FinalSourceID),
			zap.String("result", result),
			zap.String("provider_family", event.ProviderFamily),
			zap.Bool("sticky_hit", event.StickyHit),
			zap.Int64("priority_tier", event.PriorityTier),
			zap.Strings("candidate_kinds", event.CandidateKinds),
		}
		if event.SelectionReason != "" {
			fields = append(fields, zap.String("selection_reason", event.SelectionReason))
		}
		if event.Fallback {
			fields = append(fields, zap.String("fallback_reason", event.FallbackReason))
		}
		if event.ElapsedMS > 0 {
			fields = append(fields, zap.Int64("elapsed_ms", event.ElapsedMS))
		}
		if !event.At.IsZero() {
			fields = append(fields, zap.Time("at", event.At))
		}
		r.log.Info("routing selection", fields...)
	}
}

// Compile-time assertion: MetricsSelectionRecorder implements SelectionRecorder.
var _ SelectionRecorder = (*MetricsSelectionRecorder)(nil)

// FinalizeSelectionResult fills in the execution-boundary fields (Result,
// Fallback, FallbackReason, ElapsedMS) of a SelectionEvent and re-emits it.
// The server layer calls this after the upstream call returns so the
// selection + execution boundaries share one event for correlation. The
// selection-boundary event is emitted in Plan(); this second emission carries
// the execution outcome.
func FinalizeSelectionResult(recorder SelectionRecorder, event SelectionEvent, result, fallbackReason string, fallback bool, elapsed time.Duration) {
	if recorder == nil {
		return
	}
	event.Result = result
	event.Fallback = fallback
	event.FallbackReason = fallbackReason
	event.ElapsedMS = elapsed.Milliseconds()
	recorder.RecordSelection(context.Background(), event)
}
