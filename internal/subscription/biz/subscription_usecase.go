package biz

import (
	"context"
	"fmt"
	"time"
)

const (
	quotaDailyWindow   = 24 * time.Hour
	quotaWeeklyWindow  = 7 * 24 * time.Hour
	quotaMonthlyWindow = 30 * 24 * time.Hour
)

type SubscriptionUsecase struct {
	repo      SubscriptionRepository
	groupRepo GroupRepository
	now       func() time.Time
}

func NewSubscriptionUsecase(repo SubscriptionRepository, groupRepo GroupRepository) *SubscriptionUsecase {
	return &SubscriptionUsecase{
		repo:      repo,
		groupRepo: groupRepo,
		now:       time.Now,
	}
}

func (uc *SubscriptionUsecase) Assign(ctx context.Context, req *AssignSubscriptionRequest) (*UserSubscription, error) {
	if req == nil {
		return nil, fmt.Errorf("nil request")
	}
	group, err := uc.groupRepo.GetGroupByID(ctx, req.GroupID)
	if err != nil {
		return nil, err
	}
	if group.Status != SubscriptionGroupStatusEnabled {
		return nil, ErrSubscriptionGroupDisabled
	}
	active, err := uc.repo.GetActiveSubscriptionByUser(ctx, req.UserID)
	if err == nil && active != nil && active.GroupID == req.GroupID {
		return nil, ErrSubscriptionAlreadyAssigned
	}
	now := uc.now().Unix()
	startsAt := req.StartsAt
	if startsAt == 0 {
		startsAt = now
	}
	subscription := &UserSubscription{
		UserID:             req.UserID,
		GroupID:            req.GroupID,
		SubscriptionName:   req.SubscriptionName,
		Status:             SubscriptionStatusActive,
		StartsAt:           startsAt,
		ExpiresAt:          req.ExpiresAt,
		Metadata:           req.Metadata,
		CreatedAt:          now,
		UpdatedAt:          now,
		DailyWindowStart:   startsAt,
		WeeklyWindowStart:  startsAt,
		MonthlyWindowStart: startsAt,
	}
	if err := uc.repo.CreateSubscription(ctx, subscription); err != nil {
		return nil, err
	}
	return subscription, nil
}

func (uc *SubscriptionUsecase) Revoke(ctx context.Context, id int64, reason string) error {
	subscription, err := uc.repo.GetSubscriptionByID(ctx, id)
	if err != nil {
		return err
	}
	if subscription.Status == SubscriptionStatusRevoked {
		return nil
	}
	subscription.Status = SubscriptionStatusRevoked
	subscription.UpdatedAt = uc.now().Unix()
	return uc.repo.UpdateSubscription(ctx, subscription)
}

func (uc *SubscriptionUsecase) Extend(ctx context.Context, id int64, newExpiresAt int64) error {
	subscription, err := uc.repo.GetSubscriptionByID(ctx, id)
	if err != nil {
		return err
	}
	if subscription.Status == SubscriptionStatusRevoked {
		return ErrSubscriptionRevoked
	}
	subscription.ExpiresAt = newExpiresAt
	subscription.UpdatedAt = uc.now().Unix()
	return uc.repo.UpdateSubscription(ctx, subscription)
}

func (uc *SubscriptionUsecase) ResetQuota(ctx context.Context, id int64, scope string) error {
	subscription, err := uc.repo.GetSubscriptionByID(ctx, id)
	if err != nil {
		return err
	}
	now := uc.now().Unix()
	switch scope {
	case "daily":
		subscription.DailyUsageUSD = 0
		subscription.DailyWindowStart = now
	case "weekly":
		subscription.WeeklyUsageUSD = 0
		subscription.WeeklyWindowStart = now
	case "monthly":
		subscription.MonthlyUsageUSD = 0
		subscription.MonthlyWindowStart = now
	case "all":
		subscription.DailyUsageUSD = 0
		subscription.WeeklyUsageUSD = 0
		subscription.MonthlyUsageUSD = 0
		subscription.DailyWindowStart = now
		subscription.WeeklyWindowStart = now
		subscription.MonthlyWindowStart = now
	default:
		return ErrInvalidQuotaScope
	}
	subscription.UpdatedAt = now
	return uc.repo.UpdateSubscription(ctx, subscription)
}

func (uc *SubscriptionUsecase) RecordUsage(ctx context.Context, userID int64, costUSD float64) error {
	if costUSD < 0 {
		return fmt.Errorf("negative usage")
	}
	subscription, err := uc.repo.GetActiveSubscriptionByUser(ctx, userID)
	if err != nil {
		return err
	}
	updated := uc.rollWindows(subscription)
	updated.DailyUsageUSD += costUSD
	updated.WeeklyUsageUSD += costUSD
	updated.MonthlyUsageUSD += costUSD
	updated.UpdatedAt = uc.now().Unix()
	return uc.repo.UpdateSubscription(ctx, updated)
}

func (uc *SubscriptionUsecase) CheckQuota(ctx context.Context, userID int64, estimatedCost float64) (*QuotaCheckResult, error) {
	subscription, err := uc.repo.GetActiveSubscriptionByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	group, err := uc.groupRepo.GetGroupByID(ctx, subscription.GroupID)
	if err != nil {
		return nil, err
	}
	rolled := uc.rollWindows(subscription)
	return checkQuotaAgainstGroup(rolled, group, estimatedCost), nil
}

func (uc *SubscriptionUsecase) GetProgress(ctx context.Context, userID int64) (*SubscriptionProgress, error) {
	subscription, err := uc.repo.GetActiveSubscriptionByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	rolled := uc.rollWindows(subscription)
	now := uc.now().Unix()
	return &SubscriptionProgress{
		ID:               rolled.ID,
		Status:           rolled.Status,
		StartsAt:         rolled.StartsAt,
		ExpiresAt:        rolled.ExpiresAt,
		DailyUsed:        makeDimension(rolled.DailyUsageUSD, nil),
		WeeklyUsed:       makeDimension(rolled.WeeklyUsageUSD, nil),
		MonthlyUsed:      makeDimension(rolled.MonthlyUsageUSD, nil),
		RemainingSeconds: maxInt64(0, rolled.ExpiresAt-now),
	}, nil
}

func (uc *SubscriptionUsecase) ListByUser(ctx context.Context, userID int64) ([]*UserSubscription, error) {
	return uc.repo.ListSubscriptionsByUser(ctx, userID)
}

func (uc *SubscriptionUsecase) rollWindows(subscription *UserSubscription) *UserSubscription {
	if subscription == nil {
		return nil
	}
	now := uc.now().Unix()
	cloned := *subscription
	if cloned.DailyWindowStart == 0 {
		cloned.DailyWindowStart = now
	}
	if cloned.WeeklyWindowStart == 0 {
		cloned.WeeklyWindowStart = now
	}
	if cloned.MonthlyWindowStart == 0 {
		cloned.MonthlyWindowStart = now
	}
	if now-cloned.DailyWindowStart >= int64(quotaDailyWindow.Seconds()) {
		cloned.DailyUsageUSD = 0
		cloned.DailyWindowStart = now
	}
	if now-cloned.WeeklyWindowStart >= int64(quotaWeeklyWindow.Seconds()) {
		cloned.WeeklyUsageUSD = 0
		cloned.WeeklyWindowStart = now
	}
	if now-cloned.MonthlyWindowStart >= int64(quotaMonthlyWindow.Seconds()) {
		cloned.MonthlyUsageUSD = 0
		cloned.MonthlyWindowStart = now
	}
	return &cloned
}

func checkQuotaAgainstGroup(subscription *UserSubscription, group *SubscriptionGroup, estimatedCost float64) *QuotaCheckResult {
	estimated := estimatedCost
	if estimated < 0 {
		estimated = 0
	}
	result := &QuotaCheckResult{Allowed: true}
	result.Daily = makeDimension(subscription.DailyUsageUSD+estimated, group.DailyLimitUSD)
	result.Weekly = makeDimension(subscription.WeeklyUsageUSD+estimated, group.WeeklyLimitUSD)
	result.Monthly = makeDimension(subscription.MonthlyUsageUSD+estimated, group.MonthlyLimitUSD)
	result.Reasons = make([]string, 0, 3)
	if result.Daily.Limit != nil && result.Daily.Used > *result.Daily.Limit {
		result.Allowed = false
		result.Reasons = append(result.Reasons, "daily quota exceeded")
	}
	if result.Weekly.Limit != nil && result.Weekly.Used > *result.Weekly.Limit {
		result.Allowed = false
		result.Reasons = append(result.Reasons, "weekly quota exceeded")
	}
	if result.Monthly.Limit != nil && result.Monthly.Used > *result.Monthly.Limit {
		result.Allowed = false
		result.Reasons = append(result.Reasons, "monthly quota exceeded")
	}
	return result
}

func makeDimension(used float64, limit *float64) *QuotaDimension {
	remaining := 0.0
	if limit != nil {
		remaining = *limit - used
	}
	return &QuotaDimension{
		Used:      used,
		Limit:     limit,
		Remaining: remaining,
	}
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
