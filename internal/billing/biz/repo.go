package biz

import (
	"context"
	"time"
)

type AccountRepo interface {
	GetAccountSnapshot(ctx context.Context, userID string) (*Account, error)
	BatchGetAccountSnapshots(ctx context.Context, userIDs []string) (map[string]*Account, error)
	UpdateQuota(ctx context.Context, userID string, delta int64, operationType string) (int64, error)
	UpdateUsage(ctx context.Context, userID string, usedQuotaDelta, requestCountDelta int64) error
	UpdateFrozenQuota(ctx context.Context, userID string, delta int64) error
}

type ReservationRepo interface {
	CreateReservation(ctx context.Context, reservation *Reservation) error
	GetReservation(ctx context.Context, reservationID string) (*Reservation, error)
	FindByRequestID(ctx context.Context, requestID string) (*Reservation, error)
	UpdateReservationStatus(ctx context.Context, reservationID string, status string) error
	GetExpiredReservations(ctx context.Context) ([]*Reservation, error)
}

type LedgerRepo interface {
	CreateLedger(ctx context.Context, ledger *Ledger) error
	ListLedgers(ctx context.Context, userID string, page, pageSize int32) ([]*Ledger, int64, error)
	ListLedgersWithTimeRange(ctx context.Context, userID string, page, pageSize int32, startTime, endTime time.Time) ([]*Ledger, int64, error)
	ListLedgersWithFilters(ctx context.Context, userID string, page, pageSize int32, ledgerType string, startTime, endTime time.Time) ([]*Ledger, int64, error)
	AggregateLedgerByDate(ctx context.Context, userID string, ledgerType string, startTime, endTime time.Time) ([]*DailyAggregate, []*ModelAggregate, error)
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
