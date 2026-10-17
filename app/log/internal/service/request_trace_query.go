package service

import (
	"context"
	logv1 "micro-one-api/api/log/v1"
	"micro-one-api/app/log/internal/biz"
	"micro-one-api/platform/timingdto"
)

func (s *LogService) ListRequestTraces(ctx context.Context, req *logv1.ListRequestTracesRequest) (*logv1.ListRequestTracesResponse, error) {
	if req == nil {
		return nil, biz.ErrInvalidRequestTiming
	}
	items, token, err := s.uc.ListRequestTraces(ctx, biz.RequestTraceFilter{StartMS: req.StartTimeMs, EndMS: req.EndTimeMs, Outcome: req.Outcome, UserID: req.UserId, RootRequestID: req.RootRequestId, ReservationID: req.ReservationId, PageSize: req.PageSize, PageToken: req.PageToken})
	if err != nil {
		return nil, err
	}
	reply := &logv1.ListRequestTracesResponse{NextPageToken: token}
	for _, item := range items {
		reply.Items = append(reply.Items, timingdto.ToProto(item))
	}
	return reply, nil
}
func (s *LogService) GetRequestTrace(ctx context.Context, req *logv1.GetRequestTraceRequest) (*logv1.RequestTrace, error) {
	if req == nil {
		return nil, biz.ErrInvalidRequestTiming
	}
	detail, err := s.uc.GetRequestTrace(ctx, req.ObservationId)
	if err != nil {
		return nil, err
	}
	reply := &logv1.RequestTrace{Summary: timingdto.ToProto(detail.Summary), DetailState: detail.DetailState, MissingReasons: detail.MissingReasons, ObservedAtMs: detail.ObservedAtMS, DetailRetentionHours: detail.DetailRetentionHours, Truncated: detail.Truncated}
	for _, span := range detail.Spans {
		reply.Spans = append(reply.Spans, &logv1.RequestTraceSpan{Id: span.ID, ParentId: span.ParentID, Service: span.Service, Phase: span.Phase, AttemptNumber: span.AttemptNumber, StartOffsetMs: span.StartOffsetMS, DurationMs: span.DurationMS, Outcome: span.Outcome, SafeAttributes: span.SafeAttributes})
	}
	for _, milestone := range detail.Milestones {
		reply.Milestones = append(reply.Milestones, &logv1.RequestTraceMilestone{Kind: milestone.Kind, OffsetMs: milestone.OffsetMS, OutputKind: milestone.OutputKind})
	}
	for _, operation := range detail.RelatedOperations {
		reply.RelatedOperations = append(reply.RelatedOperations, &logv1.RequestTraceRelatedOperation{TraceId: operation.TraceID, SpanId: operation.SpanID, Phase: operation.Phase, StartOffsetMs: operation.StartOffsetMS, DurationMs: operation.DurationMS, Outcome: operation.Outcome, Shared: operation.Shared})
	}
	return reply, nil
}
