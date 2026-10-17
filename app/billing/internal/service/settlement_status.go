package service

import (
	"context"

	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/app/billing/internal/biz"
)

func (s *BillingService) GetSettlementStatus(ctx context.Context, req *billingv1.GetSettlementStatusRequest) (*billingv1.GetSettlementStatusResponse, error) {
	if req == nil {
		return nil, biz.ErrLedgerTraceInvalid
	}
	status, err := s.uc.GetSettlementStatus(ctx, req.ReservationId)
	if err != nil {
		return nil, err
	}
	return &billingv1.GetSettlementStatusResponse{Status: status.Status, ObservedAtMs: status.ObservedAtMS, Source: status.Source}, nil
}
