package service

import (
	"context"
	"fmt"
	"time"

	logv1 "micro-one-api/api/log/v1"
	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/pkg/jsonx"
	applogger "micro-one-api/platform/logging"
	"micro-one-api/platform/metrics"

	"go.uber.org/zap"
)

type selectionAuditPayload struct {
	TraceID         string   `json:"trace_id,omitempty"`
	OTelTraceID     string   `json:"otel_trace_id,omitempty"`
	CandidateKinds  []string `json:"candidate_kinds,omitempty"`
	StickyHit       bool     `json:"sticky_hit"`
	PriorityTier    int64    `json:"priority_tier"`
	SelectionReason string   `json:"selection_reason,omitempty"`
	Fallback        bool     `json:"fallback"`
	FallbackReason  string   `json:"fallback_reason,omitempty"`
	Result          string   `json:"result"`
	ProviderFamily  string   `json:"provider_family"`
	Planned         bool     `json:"planned"`
}

type selectionAuditRecorder struct {
	metrics relaybiz.SelectionRecorder
	logs    logv1.LogServiceClient
}

// NewSelectionAuditRecorder keeps the existing routing metrics/log record and
// adds a durable, request-scoped audit projection in log-service.
func NewSelectionAuditRecorder(metricRecorder relaybiz.SelectionRecorder, logs logv1.LogServiceClient) relaybiz.SelectionRecorder {
	return &selectionAuditRecorder{metrics: metricRecorder, logs: logs}
}

func (r *selectionAuditRecorder) RecordSelection(ctx context.Context, event relaybiz.SelectionEvent) {
	if r == nil {
		return
	}
	if r.metrics != nil {
		r.metrics.RecordSelection(ctx, event)
	}
	if r.logs == nil || event.UserID <= 0 {
		return
	}
	rootID := event.RootRequestID
	if rootID == "" {
		rootID = event.RequestID
	}
	if rootID == "" {
		return
	}
	payload, err := jsonx.Marshal(selectionAuditPayload{
		TraceID: event.TraceID, OTelTraceID: event.OTelTraceID,
		CandidateKinds: event.CandidateKinds, StickyHit: event.StickyHit,
		PriorityTier: event.PriorityTier, SelectionReason: event.SelectionReason,
		Fallback: event.Fallback, FallbackReason: event.FallbackReason,
		Result: event.Result, ProviderFamily: event.ProviderFamily, Planned: event.Planned,
	})
	if err != nil {
		metrics.RoutingAuditWritesTotal.WithLabelValues("encode_error").Inc()
		applogger.Current().Warn("routing audit encode failed", zap.Error(err))
		return
	}
	phase := "outcome"
	if event.Planned {
		phase = "planned"
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	_, err = r.logs.IngestLog(writeCtx, &logv1.IngestLogRequest{
		Level:                 "audit",
		Message:               string(payload),
		Source:                "routing-selection",
		RequestId:             event.RequestID,
		RootRequestId:         rootID,
		UserId:                event.UserID,
		ModelName:             event.Model,
		SourceKind:            event.FinalKind,
		ChannelId:             channelIDForSelection(event),
		SubscriptionAccountId: subscriptionAccountIDForSelection(event),
		ElapsedTime:           event.ElapsedMS,
		DedupeKey:             fmt.Sprintf("routing-selection:%d:%s:%s", event.UserID, rootID, phase),
	})
	if err != nil {
		metrics.RoutingAuditWritesTotal.WithLabelValues("error").Inc()
		applogger.Current().Warn("routing audit persistence failed", zap.String("root_request_id", rootID), zap.String("phase", phase), zap.Error(err))
		return
	}
	metrics.RoutingAuditWritesTotal.WithLabelValues("success").Inc()
}

func channelIDForSelection(event relaybiz.SelectionEvent) int64 {
	if event.FinalKind == relaybiz.UpstreamRouteChannel.String() {
		return event.FinalSourceID
	}
	return 0
}

func subscriptionAccountIDForSelection(event relaybiz.SelectionEvent) int64 {
	if event.FinalKind == relaybiz.UpstreamRouteSubscription.String() {
		return event.FinalSourceID
	}
	return 0
}

var _ relaybiz.SelectionRecorder = (*selectionAuditRecorder)(nil)
