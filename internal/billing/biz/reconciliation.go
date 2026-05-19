package biz

import (
	"context"
	"fmt"
	"time"
)

// ReconciliationRepo provides data access for reconciliation tasks.
type ReconciliationRepo interface {
	// ListAllAccounts returns all accounts for consistency checks.
	ListAllAccounts(ctx context.Context) ([]*Account, error)
	// SumLedgerAmounts returns the net ledger amount for a user (sum of all amounts).
	SumLedgerAmounts(ctx context.Context, userID string) (int64, error)
	// ListReservationsByStatus returns reservations with the given status.
	ListReservationsByStatus(ctx context.Context, status string) ([]*Reservation, error)
}

// ReconciliationRunStore persists historical reconciliation runs so admins can review them.
type ReconciliationRunStore interface {
	SaveRun(ctx context.Context, result *ReconciliationResult) (int64, error)
	ListRuns(ctx context.Context, page, pageSize int32) ([]*ReconciliationResult, int64, error)
	GetRun(ctx context.Context, runID int64) (*ReconciliationResult, error)
}

// ReconciliationResult holds the outcome of a reconciliation run.
type ReconciliationResult struct {
	RunID                  int64                  `json:"run_id,omitempty"`
	RunAt                  time.Time              `json:"run_at"`
	ExpiredCleaned         int                    `json:"expired_cleaned"`
	AccountInconsistencies []AccountInconsistency `json:"account_inconsistencies,omitempty"`
	TotalAccounts          int                    `json:"total_accounts"`
	TotalReservations      int                    `json:"total_reservations"`
}

// AccountInconsistency describes a quota mismatch for a single account.
type AccountInconsistency struct {
	UserID          string `json:"user_id"`
	ExpectedQuota   int64  `json:"expected_quota"`
	ActualQuota     int64  `json:"actual_quota"`
	LedgerNetAmount int64  `json:"ledger_net_amount"`
	FrozenQuota     int64  `json:"frozen_quota"`
}

// ReconciliationUsecase runs billing reconciliation tasks.
type ReconciliationUsecase struct {
	accountRepo     AccountRepo
	reservationRepo ReservationRepo
	reconRepo       ReconciliationRepo
	runStore        ReconciliationRunStore
}

// NewReconciliationUsecase creates a new ReconciliationUsecase. runStore is optional —
// when nil the runs are not persisted (matches legacy behavior).
func NewReconciliationUsecase(
	accountRepo AccountRepo,
	reservationRepo ReservationRepo,
	reconRepo ReconciliationRepo,
	runStore ReconciliationRunStore,
) *ReconciliationUsecase {
	return &ReconciliationUsecase{
		accountRepo:     accountRepo,
		reservationRepo: reservationRepo,
		reconRepo:       reconRepo,
		runStore:        runStore,
	}
}

// RunReconciliation performs a full reconciliation: cleans expired reservations and checks quota consistency.
func (uc *ReconciliationUsecase) RunReconciliation(ctx context.Context) (*ReconciliationResult, error) {
	result := &ReconciliationResult{
		RunAt: time.Now(),
	}

	// Step 1: Clean up expired reservations
	expired, err := uc.reservationRepo.GetExpiredReservations(ctx)
	if err != nil {
		return nil, fmt.Errorf("get expired reservations: %w", err)
	}

	for _, res := range expired {
		if res.IsReserved() {
			// Release the expired reservation
			if err := uc.reservationRepo.UpdateReservationStatus(ctx, res.ReservationID, ReservationStatusExpired); err != nil {
				continue
			}
			// Unfreeze the quota
			_ = uc.accountRepo.UpdateFrozenQuota(ctx, res.UserID, -res.Amount)
			// Return the quota
			_, _ = uc.accountRepo.UpdateQuota(ctx, res.UserID, res.Amount, LedgerTypeRefund)
			result.ExpiredCleaned++
		}
	}

	// Step 2: Check account quota consistency
	accounts, err := uc.reconRepo.ListAllAccounts(ctx)
	if err != nil {
		return result, fmt.Errorf("list accounts: %w", err)
	}
	result.TotalAccounts = len(accounts)

	for _, account := range accounts {
		ledgerNet, err := uc.reconRepo.SumLedgerAmounts(ctx, account.UserID)
		if err != nil {
			continue
		}

		// The account's quota should roughly match the ledger net amount
		// Allow a tolerance of 100 units for rounding
		diff := account.Quota - ledgerNet
		if diff < 0 {
			diff = -diff
		}
		if diff > 100 {
			result.AccountInconsistencies = append(result.AccountInconsistencies, AccountInconsistency{
				UserID:          account.UserID,
				ExpectedQuota:   ledgerNet,
				ActualQuota:     account.Quota,
				LedgerNetAmount: ledgerNet,
				FrozenQuota:     account.FrozenQuota,
			})
		}
	}

	// Count reserved reservations
	reserved, _ := uc.reconRepo.ListReservationsByStatus(ctx, ReservationStatusReserved)
	result.TotalReservations = len(reserved)

	if uc.runStore != nil {
		if runID, err := uc.runStore.SaveRun(ctx, result); err == nil {
			result.RunID = runID
		}
	}

	return result, nil
}

// ListReconciliationRuns returns paginated historical runs, newest first.
// Returns an empty slice when no runStore is configured.
func (uc *ReconciliationUsecase) ListReconciliationRuns(ctx context.Context, page, pageSize int32) ([]*ReconciliationResult, int64, error) {
	if uc.runStore == nil {
		return nil, 0, nil
	}
	return uc.runStore.ListRuns(ctx, page, pageSize)
}

// GetReconciliationRun returns a single historical run by ID.
// Returns nil, nil when no runStore is configured.
func (uc *ReconciliationUsecase) GetReconciliationRun(ctx context.Context, runID int64) (*ReconciliationResult, error) {
	if uc.runStore == nil {
		return nil, nil
	}
	return uc.runStore.GetRun(ctx, runID)
}
