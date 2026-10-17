package service

import (
	"context"
	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/app/billing/internal/biz"
)

func (s *BillingService) ResolveLedgerRequest(ctx context.Context, req *billingv1.ResolveLedgerRequestRequest) (*billingv1.ResolveLedgerRequestResponse, error) {
	if req == nil {
		return nil, biz.ErrLedgerTraceInvalid
	}
	ref, err := s.uc.ResolveLedgerRequest(ctx, req.LedgerId)
	if err != nil {
		return nil, err
	}
	return &billingv1.ResolveLedgerRequestResponse{Applicable: ref.Applicable, UserId: ref.UserID, ReservationId: ref.ReservationID, RootRequestId: ref.RootRequestID}, nil
}
