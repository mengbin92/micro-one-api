package biz

import (
	"context"
	"errors"
	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/domain/authorization"
	"strconv"
)

var ErrLedgerTraceInvalid = kratoserrors.BadRequest(billingv1.LedgerRequestTraceErrorReason_INVALID_LEDGER_TRACE_REQUEST.String(), "invalid ledger reference request")
var ErrLedgerTraceNotFound = kratoserrors.NotFound(billingv1.LedgerRequestTraceErrorReason_LEDGER_TRACE_NOT_FOUND.String(), "ledger entry not found")

type LedgerRequestReference struct {
	Applicable                   bool
	UserID                       int64
	ReservationID, RootRequestID string
}

// ResolveLedgerRequest reads the authoritative ledger/reservation association
// independently of pricing snapshots and cost-field visibility.
func (uc *BillingUsecase) ResolveLedgerRequest(ctx context.Context, id int64) (*LedgerRequestReference, error) {
	if id <= 0 {
		return nil, ErrLedgerTraceInvalid
	}
	ctx, err := prepareBilling(ctx, uc.authorization, "billing.ledger.read", "billing.account.ledger.read")
	if err != nil {
		return nil, err
	}
	ledger, err := uc.ledgerRepo.GetLedgerByID(ctx, id)
	if errors.Is(err, ErrLedgerNotFound) {
		return nil, ErrLedgerTraceNotFound
	}
	if err != nil {
		return nil, err
	}
	facts, factsErr := accountFacts(ledger.UserID)
	if factsErr != nil {
		return nil, factsErr
	}
	if err := authorization.Require(ctx, "billing.account.ledger.read", facts); err != nil {
		return nil, err
	}
	ref := &LedgerRequestReference{}
	if ledger.Type != "consume" || ledger.ReferenceID == "" || ledger.ModelName == "" {
		return ref, nil
	}
	owner, err := strconv.ParseInt(ledger.UserID, 10, 64)
	if err != nil || owner <= 0 {
		return ref, nil
	}
	reservation, err := uc.reservationRepo.GetReservation(ctx, ledger.ReferenceID)
	if errors.Is(err, ErrReservationNotFound) {
		return ref, nil
	}
	if err != nil {
		return nil, err
	}
	if reservation == nil || reservation.UserID != ledger.UserID {
		return ref, nil
	}
	ref.Applicable = true
	ref.UserID = owner
	ref.ReservationID = reservation.ReservationID
	ref.RootRequestID = reservation.RootRequestID
	return ref, nil
}
