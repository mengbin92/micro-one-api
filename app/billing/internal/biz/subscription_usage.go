package biz

import (
	"context"
	"strconv"

	subscriptionbiz "micro-one-api/domain/subscription/biz"
)

// GetSubscriptionUsage reads settled counters and reservation sums under the
// subscription row lock also used by admission and settlement.
func (uc *BillingUsecase) GetSubscriptionUsage(ctx context.Context, userID int64) (*subscriptionbiz.SubscriptionProgress, error) {
	if uc.subscription == nil {
		return nil, subscriptionbiz.ErrSubscriptionNotFound
	}
	runner := uc.resolveRunner()
	repo, ok := uc.reservationRepo.(FrozenReservationRepo)
	if runner == nil || !ok {
		return nil, ErrRequestSnapshotUnavailable
	}
	var progress *subscriptionbiz.SubscriptionProgress
	err := runner.RunInTx(ctx, func(ctx context.Context, tx subscriptionbiz.Tx) error {
		sub, err := uc.subscription.GetActiveSubscriptionForUserInTx(ctx, tx, userID)
		if err != nil {
			return err
		}
		group, err := uc.subscription.GetGroupForSubscriptionInTx(ctx, tx, sub)
		if err != nil {
			return err
		}
		progress = subscriptionbiz.BuildSubscriptionProgress(sub, group, uc.Now().Unix())
		d, w, m, err := repo.SumActiveFrozenAccountingInTx(ctx, tx, strconv.FormatInt(userID, 10), sub.ID,
			progress.DailyUsed.WindowStart, progress.WeeklyUsed.WindowStart, progress.MonthlyUsed.WindowStart, progress.RateMultiplier)
		if err != nil {
			return err
		}
		for i, dimension := range []*subscriptionbiz.QuotaDimension{progress.DailyUsed, progress.WeeklyUsed, progress.MonthlyUsed} {
			frozen := []float64{d, w, m}[i]
			dimension.Frozen = &frozen
			if dimension.Limit != nil {
				available := max(0, *dimension.Limit-dimension.Settled-frozen)
				dimension.Available = &available
				dimension.OverLimit = dimension.Settled+frozen > *dimension.Limit
			}
		}
		progress.UsageSource = "billing"
		return nil
	})
	return progress, err
}
