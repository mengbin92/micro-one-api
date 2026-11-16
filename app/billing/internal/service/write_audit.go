package service

import (
	"context"
	"errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/app/billing/internal/biz"
)

// ListWriteAudits exposes the billing owner's resource_write_audits table to
// the admin audit console. The registry admits only the admin system
// identity; the usecase re-checks the system capability for operator-carrying
// requests that somehow reached the handler.
func (s *BillingService) ListWriteAudits(ctx context.Context, req *billingv1.ListWriteAuditsRequest) (*billingv1.ListWriteAuditsResponse, error) {
	page, err := s.uc.ListWriteAudits(ctx, biz.WriteAuditFilter{
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
	items := make([]*billingv1.WriteAuditItem, 0, len(page.Items))
	for _, row := range page.Items {
		items = append(items, &billingv1.WriteAuditItem{
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
	return &billingv1.ListWriteAuditsResponse{Items: items, Total: page.Total, StoreId: page.StoreID}, nil
}
