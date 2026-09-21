package service

import (
	"context"
	billingv1 "micro-one-api/api/billing/v1"
)

func (s *BillingService) ListRequestAttempts(ctx context.Context, req *billingv1.ListRequestAttemptsRequest) (*billingv1.ListRequestAttemptsResponse, error) {
	rows, total, err := s.uc.ListRequestAttempts(ctx, req.GetUserId(), req.GetRootRequestId(), int(req.GetPage()), int(req.GetPageSize()))
	if err != nil {
		return nil, err
	}
	resp := &billingv1.ListRequestAttemptsResponse{Total: total}
	for _, row := range rows {
		resp.Items = append(resp.Items, &billingv1.RequestAttempt{RootRequestId: row.RootRequestID, RequestId: row.RequestID, AttemptNumber: row.AttemptNumber, ReservationId: row.ReservationID, Status: row.Status, ChannelId: row.ChannelID, SubscriptionAccountId: row.SubscriptionAccountID, SourceKind: row.SourceKind, UpstreamModelId: row.UpstreamModelID, ActualCost: row.ActualCost, CreatedAt: row.CreatedAt.Unix()})
	}
	return resp, nil
}
