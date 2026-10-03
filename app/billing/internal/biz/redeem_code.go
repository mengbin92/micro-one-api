package biz

import "time"

const (
	RedeemCodeStatusDisabled = int32(0)
	RedeemCodeStatusEnabled  = int32(1)
	RedeemCodeStatusUsed     = int32(2)
)

type RedeemCode struct {
	Revision  int64
	ID        int64
	Code      string
	Name      string
	Amount    int64
	Count     int32
	Status    int32
	CreatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (r *RedeemCode) IsEnabled() bool {
	return r.Status == RedeemCodeStatusEnabled
}

func (r *RedeemCode) IsAvailable() bool {
	return r.IsEnabled() && r.Count > 0
}

type RedeemRecord struct {
	ID            uint
	UserID        string
	Code          string
	Amount        int64
	BalanceBefore int64
	BalanceAfter  int64
	CreatedAt     time.Time
}
