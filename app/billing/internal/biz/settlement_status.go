package biz

import (
	"context"
	"errors"
	"time"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/domain/authorization"
)

var ErrSettlementReservationNotFound = kratoserrors.NotFound(billingv1.LedgerRequestTraceErrorReason_SETTLEMENT_RESERVATION_NOT_FOUND.String(), "settlement reservation not found")

// SettlementStatus is the authoritative current settlement state of a
// reservation, read on demand for the request-trace detail. It complements
// the request-end snapshot: a commit that was only enqueued at request end
// can be observed here as committed once the async worker finishes.
type SettlementStatus struct {
	// Status reuses the trace summary settlement vocabulary:
	// committed | released | enqueued | failed | reserved | unknown.
	Status       string
	ObservedAtMS int64
	// Source records where the answer came from:
	// reservation | reservation+settlement_task.
	Source string
}

// GetSettlementStatus resolves the current settlement state for a reservation.
// Authorization mirrors the ledger trace lookup: the caller must hold
// billing.account.ledger.read over the reservation owner. A missing task or an
// unavailable task reader never fails the query — the reservation row alone is
// still authoritative for committed/released/expired.
func (uc *BillingUsecase) GetSettlementStatus(ctx context.Context, reservationID string) (*SettlementStatus, error) {
	if reservationID == "" || len(reservationID) > 64 {
		return nil, ErrLedgerTraceInvalid
	}
	ctx, err := prepareBilling(ctx, uc.authorization, "billing.ledger.read", "billing.account.ledger.read")
	if err != nil {
		return nil, err
	}
	reservation, err := uc.reservationRepo.GetReservation(ctx, reservationID)
	if errors.Is(err, ErrReservationNotFound) {
		return nil, ErrSettlementReservationNotFound
	}
	if err != nil {
		return nil, err
	}
	if reservation == nil {
		return nil, ErrSettlementReservationNotFound
	}
	facts, factsErr := accountFacts(reservation.UserID)
	if factsErr != nil {
		return nil, factsErr
	}
	if err := authorization.Require(ctx, "billing.account.ledger.read", facts); err != nil {
		return nil, err
	}

	status := &SettlementStatus{ObservedAtMS: time.Now().UnixMilli(), Source: "reservation"}
	switch reservation.Status {
	case ReservationStatusCommitted:
		status.Status = "committed"
	case ReservationStatusReleased, ReservationStatusExpired:
		status.Status = "released"
	case ReservationStatusReserved, ReservationStatusCommitting, ReservationStatusReleasing:
		// A live reservation with a queued/in-flight async commit is reported
		// as enqueued, matching the request-end snapshot vocabulary.
		status.Status = "reserved"
	default:
		status.Status = "unknown"
	}

	if reader, ok := uc.settlementTaskReader(); ok {
		taskStatus, taskErr := reader.TaskStatusByReservation(ctx, reservationID)
		if taskErr == nil && taskStatus != "" {
			status.Source = "reservation+settlement_task"
			switch taskStatus {
			case "pending", "processing":
				if status.Status == "reserved" || status.Status == "unknown" {
					status.Status = "enqueued"
				}
			case "failed", "invalid":
				if status.Status != "committed" && status.Status != "released" {
					status.Status = "failed"
				}
			case "completed":
				// The reservation row already reports committed/released.
			}
		}
	}
	return status, nil
}

// settlementTaskStoreHolder shares the late-wired task store across by-value
// copies of the usecase (pricing capture); the store itself is set once
// during composition and never mutated afterwards.
type settlementTaskStoreHolder struct {
	store SettlementTaskStore
}

func (uc *BillingUsecase) settlementTaskReader() (SettlementTaskStatusReader, bool) {
	if uc.settlementTasks == nil || uc.settlementTasks.store == nil {
		return nil, false
	}
	reader, ok := uc.settlementTasks.store.(SettlementTaskStatusReader)
	return reader, ok
}

// SetSettlementTaskStore wires the durable settlement task store so
// GetSettlementStatus can refine live reservation states with the async task
// position. Optional: without it the reservation row alone is reported.
func (uc *BillingUsecase) SetSettlementTaskStore(store SettlementTaskStore) {
	if store == nil {
		uc.settlementTasks = nil
		return
	}
	uc.settlementTasks = &settlementTaskStoreHolder{store: store}
}
