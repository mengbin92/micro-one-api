package biz

import "context"

type QuotaChecker struct {
	uc *SubscriptionUsecase
}

func NewQuotaChecker(uc *SubscriptionUsecase) *QuotaChecker {
	return &QuotaChecker{uc: uc}
}

func (c *QuotaChecker) CheckQuota(ctx context.Context, userID int64, estimatedCost float64) (*QuotaCheckResult, error) {
	if c == nil || c.uc == nil {
		return &QuotaCheckResult{Allowed: true}, nil
	}
	return c.uc.CheckQuota(ctx, userID, estimatedCost)
}
