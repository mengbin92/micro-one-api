package timingdto

import (
	"fmt"
	logv1 "micro-one-api/api/log/v1"
	"micro-one-api/domain/requesttrace"
)

func FromProto(p *logv1.RequestTimingSummary) (*requesttrace.TimingSummary, error) {
	if p == nil {
		return nil, nil
	}
	s := &requesttrace.TimingSummary{
		ConnectionID: p.ConnectionId, Version: p.Version, ObservationID: p.ObservationId, ObservationKind: p.ObservationKind,
		OTelTraceID: p.OtelTraceId, SpanID: p.SpanId, CompatibilityTraceID: p.CompatibilityTraceId,
		RootRequestID: p.RootRequestId, UserID: p.UserId, Endpoint: p.Endpoint,
		Protocol: p.Protocol, ExecutionPath: p.ExecutionPath, Outcome: p.Outcome,
		StartedAtMS: p.StartedAtMs, OutputFinishedAtMS: p.OutputFinishedAtMs,
		HandlerFinishedAtMS: p.HandlerFinishedAtMs, TurnFinishedAtMS: p.TurnFinishedAtMs,
		RPCFinishedAtMS: p.RpcFinishedAtMs, OutputDurationMS: p.OutputDurationMs,
		HandlerDurationMS: p.HandlerDurationMs, TurnDurationMS: p.TurnDurationMs,
		RPCDurationMS: p.RpcDurationMs, FirstContentMS: p.FirstContentMs,
		OutputComplete: p.OutputComplete, OutputOutcome: p.OutputOutcome, CollectionState: p.CollectionState, MissingReasons: p.MissingReasons,
	}
	for _, a := range p.Attempts {
		if a == nil {
			return nil, fmt.Errorf("invalid request timing")
		}
		s.Attempts = append(s.Attempts, requesttrace.TimingAttempt{
			Number: a.Number, RequestID: a.RequestId, ReservationID: a.ReservationId,
			SourceKind: a.SourceKind, SourceID: a.SourceId, UpstreamModelID: a.UpstreamModelId, Outcome: a.Outcome,
		})
	}
	for _, stage := range p.Stages {
		if stage == nil {
			return nil, fmt.Errorf("invalid request timing")
		}
		s.Stages = append(s.Stages, requesttrace.TimingStage{Phase: stage.Phase, DurationMS: stage.DurationMs, Count: stage.Count})
	}
	if p.SettlementSnapshot != nil {
		s.SettlementSnapshot = &requesttrace.TimingSettlement{Status: p.SettlementSnapshot.Status, ObservedAtMS: p.SettlementSnapshot.ObservedAtMs}
	}
	if err := s.Validate(); err != nil {
		return nil, fmt.Errorf("invalid request timing")
	}
	return s.Clone(), nil
}

func ToProto(s *requesttrace.TimingSummary) *logv1.RequestTimingSummary {
	if s == nil {
		return nil
	}
	s = s.Clone()
	p := &logv1.RequestTimingSummary{
		ConnectionId: s.ConnectionID, Version: s.Version, ObservationId: s.ObservationID, ObservationKind: s.ObservationKind,
		OtelTraceId: s.OTelTraceID, SpanId: s.SpanID, CompatibilityTraceId: s.CompatibilityTraceID,
		RootRequestId: s.RootRequestID, UserId: s.UserID, Endpoint: s.Endpoint,
		Protocol: s.Protocol, ExecutionPath: s.ExecutionPath, Outcome: s.Outcome,
		StartedAtMs: s.StartedAtMS, OutputFinishedAtMs: s.OutputFinishedAtMS,
		HandlerFinishedAtMs: s.HandlerFinishedAtMS, TurnFinishedAtMs: s.TurnFinishedAtMS,
		RpcFinishedAtMs: s.RPCFinishedAtMS, OutputDurationMs: s.OutputDurationMS,
		HandlerDurationMs: s.HandlerDurationMS, TurnDurationMs: s.TurnDurationMS,
		RpcDurationMs: s.RPCDurationMS, FirstContentMs: s.FirstContentMS,
		OutputComplete: s.OutputComplete, OutputOutcome: s.OutputOutcome, CollectionState: s.CollectionState, MissingReasons: s.MissingReasons,
	}
	for _, a := range s.Attempts {
		p.Attempts = append(p.Attempts, &logv1.RequestTimingAttempt{
			Number: a.Number, RequestId: a.RequestID, ReservationId: a.ReservationID,
			SourceKind: a.SourceKind, SourceId: a.SourceID, UpstreamModelId: a.UpstreamModelID, Outcome: a.Outcome,
		})
	}
	for _, stage := range s.Stages {
		p.Stages = append(p.Stages, &logv1.RequestTimingStage{Phase: stage.Phase, DurationMs: stage.DurationMS, Count: stage.Count})
	}
	if s.SettlementSnapshot != nil {
		p.SettlementSnapshot = &logv1.RequestTimingSettlement{Status: s.SettlementSnapshot.Status, ObservedAtMs: s.SettlementSnapshot.ObservedAtMS}
	}
	return p
}
