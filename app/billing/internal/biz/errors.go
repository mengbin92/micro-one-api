package biz

import "errors"

var (
	ErrAccountNotFound         = errors.New("account not found")
	ErrLedgerNotFound          = errors.New("ledger not found")
	ErrInsufficientQuota       = errors.New("insufficient quota")
	ErrReservationNotFound     = errors.New("reservation not found")
	ErrReservationExpired      = errors.New("reservation expired")
	ErrReservationCommitted    = errors.New("reservation already committed")
	ErrReservationReleased     = errors.New("reservation already released")
	ErrRedeemCodeNotFound      = errors.New("redeem code not found")
	ErrRedeemCodeDisabled      = errors.New("redeem code disabled")
	ErrRedeemCodeUsedUp        = errors.New("redeem code used up")
	ErrReceivableDuplicate     = errors.New("receivable already exists for reservation")
	ErrCrossDBReservation      = errors.New("subscription priority deduction requires billing and subscription data on the same database")
	ErrPricingSnapshotNotFound = errors.New("pricing snapshot not found")
)
