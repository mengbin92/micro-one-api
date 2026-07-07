package biz

import (
	"context"
	"time"

	"gorm.io/gorm"
)

type AccountRepo interface {
	GetAccountSnapshot(ctx context.Context, userID string) (*Account, error)
	BatchGetAccountSnapshots(ctx context.Context, userIDs []string) (map[string]*Account, error)
	UpdateBalance(ctx context.Context, userID string, delta int64, operationType string) (int64, error)
	UpdateBalanceInTx(ctx context.Context, tx *gorm.DB, userID string, delta int64, operationType string) (int64, error)
	UpdateUsage(ctx context.Context, userID string, usedAmountDelta, requestCountDelta int64) error
	UpdateUsageInTx(ctx context.Context, tx *gorm.DB, userID string, usedAmountDelta, requestCountDelta int64) error
	UpdateFrozenAmount(ctx context.Context, userID string, delta int64) error
	// ReserveBalanceInTx atomically pre-deducts amount from the user's wallet
	// inside the caller's transaction. It performs the read-check-update of
	// balance and the matching frozen-amount update as a single UPDATE
	// statement so concurrent callers cannot lose each other's reservations
	// (the historical read-modify-write path was racy). The function returns
	// the resulting (balance, frozen) snapshot; it refuses to proceed when the
	// wallet would go negative unless allowOverdraft is true.
	ReserveBalanceInTx(ctx context.Context, tx *gorm.DB, userID string, amount int64, allowOverdraft bool) (oldBalance, newBalance, newFrozen int64, err error)
	// CommitBalanceInTx atomically releases the reservation's frozen amount
	// (reserved) and applies the actual settlement (actual). It is the dual
	// of ReserveBalanceInTx: the difference reserved - actual is refunded to
	// the wallet, the actual amount is deducted, and the frozen counter is
	// decremented by `reserved` in the same UPDATE. When allowOverdraft is
	// true the wallet can go negative; the caller captures the returned
	// (oldBalance, newBalance) pair to detect the new overdraft and record
	// the matching receivable. Callers must call this exactly once per
	// reservation; it is the only path that combines the balance and frozen
	// mutations in a single statement.
	CommitBalanceInTx(ctx context.Context, tx *gorm.DB, userID string, reserved, actual int64, allowOverdraft bool) (oldBalance, newBalance int64, err error)
	// ReleaseBalanceInTx is the release counterpart of ReserveBalanceInTx.
	// It refunds `reserved` to the wallet and decrements the frozen counter
	// in a single UPDATE. The result is the post-refund wallet balance.
	ReleaseBalanceInTx(ctx context.Context, tx *gorm.DB, userID string, reserved int64) (newBalance int64, err error)
}

type ReservationRepo interface {
	CreateReservation(ctx context.Context, reservation *Reservation) error
	GetReservation(ctx context.Context, reservationID string) (*Reservation, error)
	FindByRequestID(ctx context.Context, requestID string) (*Reservation, error)
	UpdateReservationStatus(ctx context.Context, reservationID string, status string) error
	GetExpiredReservations(ctx context.Context) ([]*Reservation, error)
	// CreateReservationInTx inserts a reservation in the caller's transaction.
	// Used by the dual-track pre-deduction flow so the reservation row and
	// the wallet pre-deduction commit or roll back together.
	CreateReservationInTx(ctx context.Context, tx *gorm.DB, reservation *Reservation) error
	// GetReservationInTx reads a reservation inside the caller's transaction.
	// Used by the CAS commit/release pipeline so the read sees the row with
	// the row lock acquired.
	GetReservationInTx(ctx context.Context, tx *gorm.DB, reservationID string) (*Reservation, error)
	// CASReservationStatus atomically transitions a reservation's status from
	// `from` to `to`. Returns true when the update affected a row (the caller
	// won the race), false when another transaction has already moved the
	// row to a non-`from` state. The function performs the read of the
	// current status inside the caller's transaction so it pairs with the
	// locking SELECT FOR UPDATE the caller must have already taken.
	CASReservationStatus(ctx context.Context, tx *gorm.DB, reservationID, from, to string) (bool, error)
	// LockSubscriptionRow takes a row lock on the user_subscriptions row
	// identified by id. Used by the dual-track pre-deduction flow to
	// serialise concurrent reservations against the same subscription so
	// the absorber check cannot oversell the daily/weekly/monthly window.
	// The function is a no-op when the subscription table lives in a
	// different database from the caller's transaction (caller is
	// responsible for verifying same-DB deployment before opening the
	// dual-track flow).
	LockSubscriptionRow(ctx context.Context, tx *gorm.DB, subscriptionID int64) error
	// SumActiveFrozenInTx aggregates the subscription-side pre-deduction
	// USD of every active reservation belonging to a user whose
	// subscription matches the given id and whose window-start snapshot
	// matches the current window. The result is in the original (un-
	// multiplied) USD; the caller multiplies by RateMultiplier when it
	// needs accounting USD. Returned booleans flag (dailyHit, weeklyHit,
	// monthlyHit) so the caller can short-circuit when any window has
	// already been exceeded without paying the SUM cost. The query is
	// designed to be cheap enough to run inside the locking transaction
	// of the dual-track pre-deduction flow.
	SumActiveFrozenInTx(ctx context.Context, tx *gorm.DB, userID string, subscriptionID, dailyStart, weeklyStart, monthlyStart int64) (dailyUSD, weeklyUSD, monthlyUSD float64, count int64, err error)
}

type LedgerRepo interface {
	CreateLedger(ctx context.Context, ledger *Ledger) error
	CreateLedgerInTx(ctx context.Context, tx *gorm.DB, ledger *Ledger) error
	ListLedgers(ctx context.Context, userID string, page, pageSize int32) ([]*Ledger, int64, error)
	ListLedgersWithTimeRange(ctx context.Context, userID string, page, pageSize int32, startTime, endTime time.Time) ([]*Ledger, int64, error)
	ListLedgersWithFilters(ctx context.Context, userID string, page, pageSize int32, ledgerType string, startTime, endTime time.Time) ([]*Ledger, int64, error)
	ListLedgersBySubscriptionAccount(ctx context.Context, subscriptionAccountID int64, page, pageSize int32) ([]*Ledger, int64, error)
	AggregateLedgerByDate(ctx context.Context, userID string, ledgerType string, startTime, endTime time.Time) ([]*DailyAggregate, []*ModelAggregate, error)
	AggregateUsage(ctx context.Context, filter UsageFilter) ([]*UsageBucket, *UsageTotals, error)
	// FindByDedupeKey returns the ledger entry with the given dedupe key or
	// nil if none exists. Used by the CAS commit pipeline to detect
	// pre-existing entries left by an earlier failed attempt.
	FindByDedupeKey(ctx context.Context, tx *gorm.DB, key string) (*Ledger, error)
	// SumSubscriptionCostByReservation returns the total subscription_cost
	// recorded against the given reservation IDs. Used by reconciliation to
	// verify the subscription-side ledger matches the per-reservation actual
	// absorption.
	SumSubscriptionCostByReservation(ctx context.Context, reservationIDs []string) (int64, error)
}

type ReceivableRepo interface {
	// CreateInTx inserts a pending receivable inside the caller's transaction.
	// Caller is responsible for honouring the reservation_id unique
	// constraint: the function returns ErrReceivableDuplicate when the row
	// already exists so the CAS pipeline can short-circuit.
	CreateInTx(ctx context.Context, tx *gorm.DB, recv *AccountReceivable) error
	// ListPendingByUser returns the user's pending receivables, oldest first.
	ListPendingByUser(ctx context.Context, userID string) ([]*AccountReceivable, error)
	// SettleOldestForUserInTx settles up to `amount` quota of the user's
	// pending receivables inside the caller's transaction. Returns the
	// actually-settled quota (sum of overdue_quota of all rows transitioned
	// to settled) so the caller can detect shortfalls.
	SettleOldestForUserInTx(ctx context.Context, tx *gorm.DB, userID string, amount int64) (settled int64, err error)
	// SumOverduePendingByUser returns the total pending overdue_quota for
	// the user. Used by the wallet-overdraft check in TopUpQuota.
	SumOverduePendingByUser(ctx context.Context, userID string) (int64, error)
}

type RedeemRepo interface {
	CreateRedeemCode(ctx context.Context, code *RedeemCode) error
	CreateRedeemCodesBatch(ctx context.Context, codes []*RedeemCode) error
	GetRedeemCode(ctx context.Context, code string) (*RedeemCode, error)
	ListRedeemCodes(ctx context.Context, page, pageSize int32) ([]*RedeemCode, int64, error)
	SearchRedeemCodes(ctx context.Context, keyword string) ([]*RedeemCode, error)
	UpdateRedeemCode(ctx context.Context, code *RedeemCode) error
	UpdateRedeemCodeCount(ctx context.Context, code string, delta int) error
	DeleteRedeemCode(ctx context.Context, code string) error
	CreateRedeemRecord(ctx context.Context, record *RedeemRecord) error
}

type PricingConfigStore interface {
	GetPricingConfig(ctx context.Context) (PricingConfig, error)
}
