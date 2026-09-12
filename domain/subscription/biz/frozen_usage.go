package biz

import (
	"context"
	"fmt"
	"math"
)

// FrozenWindowCharge records settlement against the exact pre-deduction
// windows. Its accounting amount is already multiplied by the captured Q.
type FrozenWindowCharge struct {
	ReservationID      string
	SubscriptionID     int64
	QuotaPolicyID      int64
	DailyWindowStart   int64
	WeeklyWindowStart  int64
	MonthlyWindowStart int64
	AccountingUSD      float64
	CreatedAt          int64
}

type FrozenUsageRepository interface {
	AddFrozenUsageInTx(context.Context, Tx, FrozenWindowCharge) error
}

func (uc *SubscriptionUsecase) RecordFrozenUsageInTx(ctx context.Context, tx Tx, charge FrozenWindowCharge) error {
	if charge.ReservationID == "" || charge.SubscriptionID <= 0 || charge.QuotaPolicyID <= 0 || charge.AccountingUSD < 0 || math.IsNaN(charge.AccountingUSD) || math.IsInf(charge.AccountingUSD, 0) {
		return fmt.Errorf("invalid frozen usage")
	}
	repo, ok := uc.repo.(FrozenUsageRepository)
	if !ok {
		return fmt.Errorf("frozen subscription usage capability unavailable")
	}
	return repo.AddFrozenUsageInTx(ctx, tx, charge)
}
