package service

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	billingv1 "micro-one-api/api/billing/v1"
	logv1 "micro-one-api/api/log/v1"
	"micro-one-api/domain/requesttrace"
	"micro-one-api/platform/timingdto"
)

func (s *AdminService) SetLogClient(client logv1.LogServiceClient) { s.logClient = client }

type RequestTraceList struct {
	Items         []*requesttrace.TimingSummary `json:"items"`
	NextPageToken string                        `json:"next_page_token"`
	Applicable    *bool                         `json:"applicable,omitempty"`
}

func (s *AdminService) ListRequestTraces(ctx context.Context, req *logv1.ListRequestTracesRequest) (*RequestTraceList, error) {
	if s.logClient == nil {
		return nil, status.Error(codes.Unavailable, "request trace service unavailable")
	}
	ctx, cancel := context.WithTimeout(operatorRPCContext(ctx), 5*time.Second)
	defer cancel()
	reply, err := s.logClient.ListRequestTraces(ctx, req)
	if err != nil {
		return nil, err
	}
	view := &RequestTraceList{Items: make([]*requesttrace.TimingSummary, 0, len(reply.Items)), NextPageToken: reply.NextPageToken}
	for _, item := range reply.Items {
		summary, err := timingdto.FromProto(item)
		if err != nil {
			return nil, status.Error(codes.DataLoss, "invalid request summary")
		}
		view.Items = append(view.Items, summary)
	}
	return view, nil
}
func (s *AdminService) GetRequestTrace(ctx context.Context, observationID string) (*requesttrace.TraceDetail, error) {
	if s.logClient == nil {
		return nil, status.Error(codes.Unavailable, "request trace service unavailable")
	}
	ctx, cancel := context.WithTimeout(operatorRPCContext(ctx), 5*time.Second)
	defer cancel()
	reply, err := s.logClient.GetRequestTrace(ctx, &logv1.GetRequestTraceRequest{ObservationId: observationID})
	if err != nil {
		return nil, err
	}
	summary, err := timingdto.FromProto(reply.Summary)
	if err != nil {
		return nil, status.Error(codes.DataLoss, "invalid request summary")
	}
	view := &requesttrace.TraceDetail{Summary: summary, DetailState: reply.DetailState, MissingReasons: reply.MissingReasons, ObservedAtMS: reply.ObservedAtMs, DetailRetentionHours: reply.DetailRetentionHours, Truncated: reply.Truncated, Spans: []requesttrace.TraceSpan{}, Milestones: []requesttrace.TraceMilestone{}}
	for _, span := range reply.Spans {
		view.Spans = append(view.Spans, requesttrace.TraceSpan{ID: span.Id, ParentID: span.ParentId, Service: span.Service, Phase: span.Phase, AttemptNumber: span.AttemptNumber, StartOffsetMS: span.StartOffsetMs, DurationMS: span.DurationMs, Outcome: span.Outcome, SafeAttributes: span.SafeAttributes})
	}
	for _, m := range reply.Milestones {
		view.Milestones = append(view.Milestones, requesttrace.TraceMilestone{Kind: m.Kind, OffsetMS: m.OffsetMs, OutputKind: m.OutputKind})
	}
	for _, operation := range reply.RelatedOperations {
		view.RelatedOperations = append(view.RelatedOperations, requesttrace.RelatedOperation{TraceID: operation.TraceId, SpanID: operation.SpanId, Phase: operation.Phase, StartOffsetMS: operation.StartOffsetMs, DurationMS: operation.DurationMs, Outcome: operation.Outcome, Shared: operation.Shared})
	}
	s.attachSettlementCurrent(ctx, view)
	return view, nil
}

// attachSettlementCurrent enriches the detail with the live settlement state
// read from the billing owner for the last reserved attempt. The query never
// blocks on it: permission denial, billing unavailability, an unknown
// reservation or the absence of a reservation leaves the field absent, and
// the request-end snapshot remains the fallback.
func (s *AdminService) attachSettlementCurrent(ctx context.Context, view *requesttrace.TraceDetail) {
	if s.billingClient == nil || view == nil || view.Summary == nil {
		return
	}
	reservationID := ""
	for i := len(view.Summary.Attempts) - 1; i >= 0; i-- {
		if id := view.Summary.Attempts[i].ReservationID; id != nil && *id != "" {
			reservationID = *id
			break
		}
	}
	if reservationID == "" {
		return
	}
	billingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	reply, err := s.billingClient.GetSettlementStatus(billingCtx, &billingv1.GetSettlementStatusRequest{ReservationId: reservationID})
	if err != nil || reply == nil || reply.Status == "" {
		return
	}
	view.SettlementCurrent = &requesttrace.SettlementCurrent{Status: reply.Status, ObservedAtMS: reply.ObservedAtMs, Source: reply.Source}
}
func (s *AdminService) LedgerRequestTraces(ctx context.Context, ledgerID int64, pageToken string) (*RequestTraceList, error) {
	ctx, cancel := context.WithTimeout(operatorRPCContext(ctx), 5*time.Second)
	defer cancel()
	ref, err := s.billingClient.ResolveLedgerRequest(ctx, &billingv1.ResolveLedgerRequestRequest{LedgerId: ledgerID})
	if err != nil {
		return nil, err
	}
	applicable := ref.Applicable
	if !applicable {
		return &RequestTraceList{Items: []*requesttrace.TimingSummary{}, Applicable: &applicable}, nil
	}
	// Owner association is authoritative. No frontend or ledger-reference guess.
	// The owner uses the exact user/reservation association index across retention.
	// The owner chooses the first page's upper bound and restores it from its
	// cursor on later pages, preserving one consistent reservation snapshot.
	view, err := s.ListRequestTraces(ctx, &logv1.ListRequestTracesRequest{UserId: &ref.UserId, ReservationId: ref.ReservationId, PageSize: 200, PageToken: pageToken})
	if err != nil {
		return nil, err
	}
	view.Applicable = &applicable
	return view, nil
}
