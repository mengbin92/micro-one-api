package biz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	gormdb "gorm.io/gorm"
)

// gormDB is a thin alias that keeps the package import line short while
// the type still resolves unambiguously. The alias is local to this
// file so other files in the package do not need to import gorm to
// reference the dual-track methods.
type gormDB = gormdb.DB

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
	// Enforce a single active subscription per user. GetActiveSubscriptionByUser
	// and the quota accounting assume one active row per user; allowing a second
	// (even in a different group) would split usage unpredictably. A genuine DB
	// error must not be swallowed and treated as "no active subscription".
	active, err := uc.repo.GetActiveSubscriptionByUser(ctx, req.UserID)
	if err != nil && !errors.Is(err, ErrSubscriptionNotFound) {
		return nil, err
	}
	if active != nil {
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

func (uc *SubscriptionUsecase) AssignOrExtend(ctx context.Context, req *AssignSubscriptionRequest) (*UserSubscription, bool, error) {
	if req == nil {
		return nil, false, fmt.Errorf("nil request")
	}
	group, err := uc.groupRepo.GetGroupByID(ctx, req.GroupID)
	if err != nil {
		return nil, false, err
	}
	if group.Status != SubscriptionGroupStatusEnabled {
		return nil, false, ErrSubscriptionGroupDisabled
	}
	active, err := uc.repo.GetActiveSubscriptionByUser(ctx, req.UserID)
	if err != nil && !errors.Is(err, ErrSubscriptionNotFound) {
		return nil, false, err
	}
	if active == nil {
		sub, err := uc.Assign(ctx, req)
		return sub, false, err
	}
	if active.GroupID != req.GroupID {
		pending, ok := pendingChangeMetadata(active.Metadata)
		if !ok || pending.ToGroupID != req.GroupID {
			return nil, true, ErrSubscriptionAlreadyAssigned
		}
		active.GroupID = req.GroupID
		active.Metadata = clearPendingChangeMetadata(active.Metadata)
	}
	if req.ExpiresAt <= active.ExpiresAt {
		duration := req.ExpiresAt - req.StartsAt
		if duration <= 0 {
			return active, true, nil
		}
		req.ExpiresAt = active.ExpiresAt + duration
	}
	active.ExpiresAt = req.ExpiresAt
	if req.SubscriptionName != "" {
		active.SubscriptionName = req.SubscriptionName
	}
	active.Metadata = mergeSubscriptionMetadata(active.Metadata, req.Metadata)
	active.UpdatedAt = uc.now().Unix()
	if err := uc.repo.UpdateSubscription(ctx, active); err != nil {
		return nil, true, err
	}
	return active, true, nil
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
	subscription.Metadata = mergeMetadataReason(subscription.Metadata, reason)
	return uc.repo.UpdateSubscription(ctx, subscription)
}

func (uc *SubscriptionUsecase) RevokeInTx(ctx context.Context, tx *gormDB, id int64, reason string) error {
	subscription, err := uc.repo.GetByIDInTx(ctx, tx, id)
	if err != nil {
		return err
	}
	if subscription.Status == SubscriptionStatusRevoked {
		return nil
	}
	subscription.Status = SubscriptionStatusRevoked
	subscription.UpdatedAt = uc.now().Unix()
	subscription.Metadata = mergeMetadataReason(subscription.Metadata, reason)
	return uc.repo.UpdateSubscriptionInTx(ctx, tx, subscription)
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

// Shorten pulls a subscription's expires_at back by subtractSeconds, used by
// the refund/reversal flow when a prorated refund claws back part of the
// entitlement. It refuses to operate on a revoked subscription and clamps the
// new expiry at now so a refund cannot push expires_at into the future.
func (uc *SubscriptionUsecase) Shorten(ctx context.Context, id int64, subtractSeconds int64) error {
	if subtractSeconds <= 0 {
		return errors.New("subtract_seconds must be positive")
	}
	subscription, err := uc.repo.GetSubscriptionByID(ctx, id)
	if err != nil {
		return err
	}
	if subscription.Status == SubscriptionStatusRevoked {
		return ErrSubscriptionRevoked
	}
	now := uc.now().Unix()
	newExpiry := subscription.ExpiresAt - subtractSeconds
	if newExpiry < now {
		newExpiry = now
	}
	subscription.ExpiresAt = newExpiry
	subscription.UpdatedAt = now
	return uc.repo.UpdateSubscription(ctx, subscription)
}

func (uc *SubscriptionUsecase) ShortenInTx(ctx context.Context, tx *gormDB, id int64, subtractSeconds int64) error {
	if subtractSeconds <= 0 {
		return errors.New("subtract_seconds must be positive")
	}
	subscription, err := uc.repo.GetByIDInTx(ctx, tx, id)
	if err != nil {
		return err
	}
	if subscription.Status == SubscriptionStatusRevoked {
		return ErrSubscriptionRevoked
	}
	now := uc.now().Unix()
	newExpiry := subscription.ExpiresAt - subtractSeconds
	if newExpiry < now {
		newExpiry = now
	}
	subscription.ExpiresAt = newExpiry
	subscription.UpdatedAt = now
	return uc.repo.UpdateSubscriptionInTx(ctx, tx, subscription)
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
	// Apply the group's billing multiplier so recorded spend matches what the
	// quota check charges. A zero/unset multiplier means "no scaling" (1.0).
	effectiveCost := costUSD
	if group, gerr := uc.groupRepo.GetGroupByID(ctx, subscription.GroupID); gerr == nil && group != nil && group.RateMultiplier > 0 {
		effectiveCost = costUSD * group.RateMultiplier
	}
	// Delegate the read-roll-increment to the repository so it happens atomically
	// (single transaction / lock). Doing it here would be a lost-update race:
	// concurrent requests read the same base row and clobber each other's
	// increment, letting users blow past their quota.
	return uc.repo.AddUsage(ctx, userID, effectiveCost, uc.now().Unix())
}

// RecordUsageForSubscriptionInTx is the row-locked variant of RecordUsage.
// It is the canonical write path for the dual-track commit pipeline: it
// takes a *gorm.DB owned by the caller so the subscription write commits
// in the same transaction as the wallet side-effects. costUSD is the
// *original* (un-multiplied) USD cost; this function multiplies by the
// group's RateMultiplier before storing so the running usage matches
// the limit/usage accounting space.
func (uc *SubscriptionUsecase) RecordUsageForSubscriptionInTx(ctx context.Context, tx *gormDB, subscriptionID int64, costUSD float64, now int64) error {
	if costUSD < 0 {
		return fmt.Errorf("negative usage")
	}
	subscription, err := uc.repo.GetByIDInTx(ctx, tx, subscriptionID)
	if err != nil {
		return err
	}
	if subscription == nil {
		return ErrSubscriptionNotFound
	}
	effectiveCost := costUSD
	if group, gerr := uc.groupRepo.GetGroupByID(ctx, subscription.GroupID); gerr == nil && group != nil && group.RateMultiplier > 0 {
		effectiveCost = costUSD * group.RateMultiplier
	}
	return uc.repo.AddUsageByIDInTx(ctx, tx, subscriptionID, effectiveCost, now)
}

// GetActiveSubscriptionForUser is the read-only variant of
// GetActiveSubscriptionByUser. The billing domain uses it during
// pre-deduction so the reservation is bound to a specific subscription
// row id (the same id the dual-track commit pipeline later writes the
// actual cost to).
func (uc *SubscriptionUsecase) GetActiveSubscriptionForUser(ctx context.Context, userID int64) (*UserSubscription, error) {
	return uc.repo.GetActiveSubscriptionByUser(ctx, userID)
}

// GetGroupForSubscription is a convenience wrapper used by the billing
// domain to load the limits + multiplier for a subscription's group.
// It deliberately does not return early when the subscription is in a
// non-active state: the billing domain still needs the limits to decide
// how much to absorb when the subscription has been revoked mid-window
// (the row-lock guarantees we still see a consistent snapshot).
func (uc *SubscriptionUsecase) GetGroupForSubscription(ctx context.Context, subscription *UserSubscription) (*SubscriptionGroup, error) {
	if subscription == nil {
		return nil, ErrSubscriptionNotFound
	}
	return uc.groupRepo.GetGroupByID(ctx, subscription.GroupID)
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
	// Surface the group's limits so Remaining is meaningful. Without them every
	// dimension reported Remaining=0, indistinguishable from "quota exhausted".
	var dailyLimit, weeklyLimit, monthlyLimit *float64
	if group, gerr := uc.groupRepo.GetGroupByID(ctx, rolled.GroupID); gerr == nil && group != nil {
		dailyLimit, weeklyLimit, monthlyLimit = group.DailyLimitUSD, group.WeeklyLimitUSD, group.MonthlyLimitUSD
	}
	return &SubscriptionProgress{
		ID:               rolled.ID,
		Status:           rolled.Status,
		StartsAt:         rolled.StartsAt,
		ExpiresAt:        rolled.ExpiresAt,
		DailyUsed:        makeDimension(rolled.DailyUsageUSD, dailyLimit),
		WeeklyUsed:       makeDimension(rolled.WeeklyUsageUSD, weeklyLimit),
		MonthlyUsed:      makeDimension(rolled.MonthlyUsageUSD, monthlyLimit),
		RemainingSeconds: maxInt64(0, rolled.ExpiresAt-now),
	}, nil
}

func (uc *SubscriptionUsecase) ListByUser(ctx context.Context, userID int64) ([]*UserSubscription, error) {
	return uc.repo.ListSubscriptionsByUser(ctx, userID)
}

func (uc *SubscriptionUsecase) List(ctx context.Context) ([]*UserSubscription, error) {
	return uc.repo.ListAllSubscriptions(ctx)
}

func (uc *SubscriptionUsecase) rollWindows(subscription *UserSubscription) *UserSubscription {
	return RollUsageWindows(subscription, uc.now().Unix())
}

// RollUsageWindows returns a copy of subscription with any usage window that has
// aged past its period reset to zero, relative to now (unix seconds). It is a
// pure function so both the usecase and the data layer's atomic AddUsage can
// share one definition of the rolling rules.
func RollUsageWindows(subscription *UserSubscription, now int64) *UserSubscription {
	if subscription == nil {
		return nil
	}
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
	// Scale the projected cost by the group's billing multiplier so the quota
	// check charges the same amount RecordUsage will later record.
	if group != nil && group.RateMultiplier > 0 {
		estimated *= group.RateMultiplier
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

// mergeMetadataReason records a revoke reason into the subscription's metadata
// without corrupting an existing opaque payload. It only injects the reason when
// metadata is empty or a JSON object; otherwise the original value is preserved.
func mergeMetadataReason(metadata, reason string) string {
	if reason == "" {
		return metadata
	}
	trimmed := strings.TrimSpace(metadata)
	if trimmed == "" {
		if b, err := json.Marshal(map[string]string{"revoke_reason": reason}); err == nil {
			return string(b)
		}
		return metadata
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &obj); err != nil || obj == nil {
		// Not a JSON object we can safely extend; leave it untouched.
		return metadata
	}
	if b, err := json.Marshal(reason); err == nil {
		obj["revoke_reason"] = b
		if merged, err := json.Marshal(obj); err == nil {
			return string(merged)
		}
	}
	return metadata
}

func mergeSubscriptionMetadata(existing, next string) string {
	if strings.TrimSpace(next) == "" {
		return existing
	}
	if strings.TrimSpace(existing) == "" {
		return next
	}
	return next
}
