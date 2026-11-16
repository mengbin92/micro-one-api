package service

import (
	"context"
	"errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	monitorv1 "micro-one-api/api/monitor/v1"
	"micro-one-api/app/monitor/internal/biz"
)

// SetWriteAuditUsecase wires the read-only audit projection that backs the
// admin audit console aggregation.
func (s *MonitorService) SetWriteAuditUsecase(uc *biz.WriteAuditUsecase) {
	if s != nil {
		s.writeAuditUC = uc
	}
}

// ListWriteAudits exposes the monitor owner's resource_write_audits table to
// the admin audit console. The registry admits only the admin system
// identity; the usecase re-checks the system capability for operator-carrying
// requests that somehow reached the handler.
func (s *MonitorService) ListWriteAudits(ctx context.Context, req *monitorv1.ListWriteAuditsRequest) (*monitorv1.ListWriteAuditsResponse, error) {
	if s == nil || s.writeAuditUC == nil {
		return nil, status.Error(codes.Unavailable, "write audit read unavailable")
	}
	page, err := s.writeAuditUC.List(ctx, biz.WriteAuditFilter{
		ActorUserID: req.GetActorUserId(),
		Operation:   req.GetOperation(),
		ResourceID:  req.GetResourceId(),
		Result:      req.GetResult(),
		StartTimeMS: req.GetStartTimeMs(),
		EndTimeMS:   req.GetEndTimeMs(),
		Offset:      int(req.GetPage()) * int(req.GetPageSize()),
		Limit:       int(req.GetPageSize()),
	})
	if err != nil {
		if errors.Is(err, biz.ErrWriteAuditUnavailable) {
			return nil, status.Error(codes.Unavailable, "write audit read unavailable")
		}
		if errors.Is(err, biz.ErrInvalidWriteAuditQuery) {
			return nil, status.Error(codes.InvalidArgument, "invalid write audit query")
		}
		return nil, err
	}
	items := make([]*monitorv1.WriteAuditItem, 0, len(page.Items))
	for _, row := range page.Items {
		items = append(items, &monitorv1.WriteAuditItem{
			EventId:          row.EventID,
			ActorUserId:      row.ActorUserID,
			Operation:        row.Operation,
			ResourceId:       row.ResourceID,
			DecisionVersions: row.DecisionVersions,
			Reason:           row.Reason,
			OccurredAt:       row.OccurredAt,
			Result:           row.Result,
		})
	}
	return &monitorv1.ListWriteAuditsResponse{Items: items, Total: page.Total, StoreId: page.StoreID}, nil
}
