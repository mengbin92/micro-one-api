package service

import (
	"context"
	"errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	logv1 "micro-one-api/api/log/v1"
	"micro-one-api/app/log/internal/biz"
)

// SetWriteAuditUsecase wires the read-only audit projection that backs the
// admin audit console aggregation.
func (s *LogService) SetWriteAuditUsecase(uc *biz.WriteAuditUsecase) {
	if s != nil {
		s.writeAuditUC = uc
	}
}

// ListWriteAudits exposes the log owner's resource_write_audits table to
// the admin audit console. The registry admits only the admin system
// identity; the usecase re-checks the system capability for operator-carrying
// requests that somehow reached the handler.
func (s *LogService) ListWriteAudits(ctx context.Context, req *logv1.ListWriteAuditsRequest) (*logv1.ListWriteAuditsResponse, error) {
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
	items := make([]*logv1.WriteAuditItem, 0, len(page.Items))
	for _, row := range page.Items {
		items = append(items, &logv1.WriteAuditItem{
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
	return &logv1.ListWriteAuditsResponse{Items: items, Total: page.Total, StoreId: page.StoreID}, nil
}
