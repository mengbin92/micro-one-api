package data

import (
	"context"
	"errors"
	"sort"

	"micro-one-api/internal/subscription/biz"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type subscriptionModel struct {
	ID                 int64   `gorm:"column:id"`
	UserID             int64   `gorm:"column:user_id"`
	GroupID            int64   `gorm:"column:group_id"`
	SubscriptionName   string  `gorm:"column:subscription_name"`
	Status             string  `gorm:"column:status"`
	StartsAt           int64   `gorm:"column:starts_at"`
	ExpiresAt          int64   `gorm:"column:expires_at"`
	DailyUsageUSD      float64 `gorm:"column:daily_usage_usd"`
	WeeklyUsageUSD     float64 `gorm:"column:weekly_usage_usd"`
	MonthlyUsageUSD    float64 `gorm:"column:monthly_usage_usd"`
	DailyWindowStart   int64   `gorm:"column:daily_window_start"`
	WeeklyWindowStart  int64   `gorm:"column:weekly_window_start"`
	MonthlyWindowStart int64   `gorm:"column:monthly_window_start"`
	Metadata           string  `gorm:"column:metadata"`
	CreatedAt          int64   `gorm:"column:created_at"`
	UpdatedAt          int64   `gorm:"column:updated_at"`
}

func (subscriptionModel) TableName() string { return "user_subscriptions" }

func NewSubscriptionRepo(repo *Repository) biz.SubscriptionRepository {
	return repo
}

func (r *Repository) CreateSubscription(ctx context.Context, subscription *biz.UserSubscription) error {
	if r.db != nil {
		return r.createSubscriptionDB(ctx, subscription)
	}
	return r.createSubscriptionMemory(ctx, subscription)
}

func (r *Repository) UpdateSubscription(ctx context.Context, subscription *biz.UserSubscription) error {
	if r.db != nil {
		return r.updateSubscriptionDB(ctx, subscription)
	}
	return r.updateSubscriptionMemory(ctx, subscription)
}

func (r *Repository) UpdateSubscriptionInTx(ctx context.Context, tx *gorm.DB, subscription *biz.UserSubscription) error {
	if r.db != nil {
		return updateSubscriptionWithTx(ctx, tx, subscription)
	}
	return r.updateSubscriptionMemory(ctx, subscription)
}

func (r *Repository) DeleteSubscription(ctx context.Context, subscriptionID int64) error {
	if r.db != nil {
		return r.deleteSubscriptionDB(ctx, subscriptionID)
	}
	return r.deleteSubscriptionMemory(ctx, subscriptionID)
}

func (r *Repository) GetSubscriptionByID(ctx context.Context, subscriptionID int64) (*biz.UserSubscription, error) {
	if r.db != nil {
		return r.getSubscriptionByIDDB(ctx, subscriptionID)
	}
	return r.getSubscriptionByIDMemory(ctx, subscriptionID)
}

func (r *Repository) ListSubscriptionsByUser(ctx context.Context, userID int64) ([]*biz.UserSubscription, error) {
	if r.db != nil {
		return r.listSubscriptionsByUserDB(ctx, userID)
	}
	return r.listSubscriptionsByUserMemory(ctx, userID)
}

func (r *Repository) ListActiveSubscriptions(ctx context.Context) ([]*biz.UserSubscription, error) {
	if r.db != nil {
		return r.listActiveSubscriptionsDB(ctx)
	}
	return r.listActiveSubscriptionsMemory(ctx)
}

func (r *Repository) ListAllSubscriptions(ctx context.Context) ([]*biz.UserSubscription, error) {
	if r.db != nil {
		return r.listAllSubscriptionsDB(ctx)
	}
	return r.listAllSubscriptionsMemory(ctx)
}

func (r *Repository) GetActiveSubscriptionByUser(ctx context.Context, userID int64) (*biz.UserSubscription, error) {
	if r.db != nil {
		return r.getActiveSubscriptionByUserDB(ctx, userID)
	}
	return r.getActiveSubscriptionByUserMemory(ctx, userID)
}

func (r *Repository) AddUsage(ctx context.Context, userID int64, costUSD float64, now int64) error {
	if r.db != nil {
		return r.addUsageDB(ctx, userID, costUSD, now)
	}
	return r.addUsageMemory(ctx, userID, costUSD, now)
}

func (r *Repository) AddUsageByIDInTx(ctx context.Context, tx *gorm.DB, subscriptionID int64, costUSD float64, now int64) error {
	if tx == nil {
		return errors.New("nil transaction")
	}
	return r.addUsageByIDInTxDB(ctx, tx, subscriptionID, costUSD, now)
}

func (r *Repository) GetByIDInTx(ctx context.Context, tx *gorm.DB, subscriptionID int64) (*biz.UserSubscription, error) {
	if tx == nil {
		return nil, errors.New("nil transaction")
	}
	return r.getByIDInTxDB(ctx, tx, subscriptionID)
}

// addUsageByIDInTxDB performs the same read-roll-increment as
// addUsageDB, but operates on an explicit subscription id (not the
// user's active subscription) and inside the caller's transaction. The
// row lock is taken on the subscription id so concurrent commits to
// the same reservation cannot double-bill the subscription's quota.
func (r *Repository) addUsageByIDInTxDB(ctx context.Context, tx *gorm.DB, subscriptionID int64, costUSD float64, now int64) error {
	var model subscriptionModel
	q := tx.WithContext(ctx).Where("id = ?", subscriptionID)
	if dialectorName(tx) != "sqlite3" {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if err := q.First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return biz.ErrSubscriptionNotFound
		}
		return err
	}
	sub := subscriptionFromModel(&model)
	rolled := biz.RollUsageWindows(&sub, now)
	rolled.DailyUsageUSD += costUSD
	rolled.WeeklyUsageUSD += costUSD
	rolled.MonthlyUsageUSD += costUSD
	return tx.Model(&subscriptionModel{}).Where("id = ?", subscriptionID).Updates(map[string]any{
		"daily_usage_usd":      rolled.DailyUsageUSD,
		"weekly_usage_usd":     rolled.WeeklyUsageUSD,
		"monthly_usage_usd":    rolled.MonthlyUsageUSD,
		"daily_window_start":   rolled.DailyWindowStart,
		"weekly_window_start":  rolled.WeeklyWindowStart,
		"monthly_window_start": rolled.MonthlyWindowStart,
		"updated_at":           now,
	}).Error
}

func (r *Repository) getByIDInTxDB(ctx context.Context, tx *gorm.DB, subscriptionID int64) (*biz.UserSubscription, error) {
	var model subscriptionModel
	q := tx.WithContext(ctx).Where("id = ?", subscriptionID)
	if dialectorName(tx) != "sqlite3" {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if err := q.First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrSubscriptionNotFound
		}
		return nil, err
	}
	sub := subscriptionFromModel(&model)
	return &sub, nil
}

// addUsageDB performs the read-roll-increment as a single transaction and takes
// a row lock (SELECT ... FOR UPDATE) on engines that support it, so concurrent
// callers serialize instead of losing each other's increments. Only the usage
// and window columns are written, so it can never clobber a concurrent
// Extend/Revoke that changed status or expires_at.
func (r *Repository) addUsageDB(ctx context.Context, userID int64, costUSD float64, now int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var model subscriptionModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("user_id = ? AND status = ?", userID, string(biz.SubscriptionStatusActive)).
			Order("updated_at DESC, id DESC").
			First(&model).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return biz.ErrSubscriptionNotFound
			}
			return err
		}
		sub := subscriptionFromModel(&model)
		rolled := biz.RollUsageWindows(&sub, now)
		rolled.DailyUsageUSD += costUSD
		rolled.WeeklyUsageUSD += costUSD
		rolled.MonthlyUsageUSD += costUSD
		return tx.Model(&subscriptionModel{}).Where("id = ?", model.ID).Updates(map[string]any{
			"daily_usage_usd":      rolled.DailyUsageUSD,
			"weekly_usage_usd":     rolled.WeeklyUsageUSD,
			"monthly_usage_usd":    rolled.MonthlyUsageUSD,
			"daily_window_start":   rolled.DailyWindowStart,
			"weekly_window_start":  rolled.WeeklyWindowStart,
			"monthly_window_start": rolled.MonthlyWindowStart,
			"updated_at":           now,
		}).Error
	})
}

func (r *Repository) addUsageMemory(ctx context.Context, userID int64, costUSD float64, now int64) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	var chosen *biz.UserSubscription
	for _, subscription := range r.subscriptions {
		if subscription.UserID != userID || subscription.Status != biz.SubscriptionStatusActive {
			continue
		}
		if chosen == nil || subscription.UpdatedAt > chosen.UpdatedAt || (subscription.UpdatedAt == chosen.UpdatedAt && subscription.ID > chosen.ID) {
			chosen = subscription
		}
	}
	if chosen == nil {
		return biz.ErrSubscriptionNotFound
	}
	rolled := biz.RollUsageWindows(chosen, now)
	rolled.DailyUsageUSD += costUSD
	rolled.WeeklyUsageUSD += costUSD
	rolled.MonthlyUsageUSD += costUSD
	rolled.UpdatedAt = now
	r.subscriptions[chosen.ID] = rolled
	return nil
}

func (r *Repository) createSubscriptionDB(ctx context.Context, subscription *biz.UserSubscription) error {
	model := subscriptionToModel(subscription)
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return err
	}
	subscription.ID = model.ID
	return nil
}

func (r *Repository) updateSubscriptionDB(ctx context.Context, subscription *biz.UserSubscription) error {
	return updateSubscriptionWithTx(ctx, r.db.WithContext(ctx), subscription)
}

func updateSubscriptionWithTx(ctx context.Context, tx *gorm.DB, subscription *biz.UserSubscription) error {
	model := subscriptionToModel(subscription)
	return tx.WithContext(ctx).Model(&subscriptionModel{}).Where("id = ?", subscription.ID).Updates(map[string]any{
		"user_id":              model.UserID,
		"group_id":             model.GroupID,
		"subscription_name":    model.SubscriptionName,
		"status":               model.Status,
		"starts_at":            model.StartsAt,
		"expires_at":           model.ExpiresAt,
		"daily_usage_usd":      model.DailyUsageUSD,
		"weekly_usage_usd":     model.WeeklyUsageUSD,
		"monthly_usage_usd":    model.MonthlyUsageUSD,
		"daily_window_start":   model.DailyWindowStart,
		"weekly_window_start":  model.WeeklyWindowStart,
		"monthly_window_start": model.MonthlyWindowStart,
		"metadata":             model.Metadata,
		"updated_at":           model.UpdatedAt,
	}).Error
}

func (r *Repository) deleteSubscriptionDB(ctx context.Context, subscriptionID int64) error {
	return r.db.WithContext(ctx).Delete(&subscriptionModel{}, subscriptionID).Error
}

func (r *Repository) getSubscriptionByIDDB(ctx context.Context, subscriptionID int64) (*biz.UserSubscription, error) {
	var model subscriptionModel
	if err := r.db.WithContext(ctx).Where("id = ?", subscriptionID).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrSubscriptionNotFound
		}
		return nil, err
	}
	subscription := subscriptionFromModel(&model)
	return &subscription, nil
}

func (r *Repository) listSubscriptionsByUserDB(ctx context.Context, userID int64) ([]*biz.UserSubscription, error) {
	var rows []subscriptionModel
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]*biz.UserSubscription, 0, len(rows))
	for i := range rows {
		subscription := subscriptionFromModel(&rows[i])
		result = append(result, &subscription)
	}
	return result, nil
}

func (r *Repository) getActiveSubscriptionByUserDB(ctx context.Context, userID int64) (*biz.UserSubscription, error) {
	var model subscriptionModel
	if err := r.db.WithContext(ctx).
		Where("user_id = ? AND status = ?", userID, string(biz.SubscriptionStatusActive)).
		Order("updated_at DESC, id DESC").
		First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrSubscriptionNotFound
		}
		return nil, err
	}
	subscription := subscriptionFromModel(&model)
	return &subscription, nil
}

func (r *Repository) listActiveSubscriptionsDB(ctx context.Context) ([]*biz.UserSubscription, error) {
	var rows []subscriptionModel
	if err := r.db.WithContext(ctx).
		Where("status = ?", string(biz.SubscriptionStatusActive)).
		Order("expires_at ASC, id ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]*biz.UserSubscription, 0, len(rows))
	for i := range rows {
		subscription := subscriptionFromModel(&rows[i])
		result = append(result, &subscription)
	}
	return result, nil
}

func (r *Repository) listAllSubscriptionsDB(ctx context.Context) ([]*biz.UserSubscription, error) {
	var rows []subscriptionModel
	if err := r.db.WithContext(ctx).
		Order("created_at DESC, id DESC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]*biz.UserSubscription, 0, len(rows))
	for i := range rows {
		subscription := subscriptionFromModel(&rows[i])
		result = append(result, &subscription)
	}
	return result, nil
}

func subscriptionToModel(subscription *biz.UserSubscription) subscriptionModel {
	if subscription == nil {
		return subscriptionModel{}
	}
	return subscriptionModel{
		ID:                 subscription.ID,
		UserID:             subscription.UserID,
		GroupID:            subscription.GroupID,
		SubscriptionName:   subscription.SubscriptionName,
		Status:             string(subscription.Status),
		StartsAt:           subscription.StartsAt,
		ExpiresAt:          subscription.ExpiresAt,
		DailyUsageUSD:      subscription.DailyUsageUSD,
		WeeklyUsageUSD:     subscription.WeeklyUsageUSD,
		MonthlyUsageUSD:    subscription.MonthlyUsageUSD,
		DailyWindowStart:   subscription.DailyWindowStart,
		WeeklyWindowStart:  subscription.WeeklyWindowStart,
		MonthlyWindowStart: subscription.MonthlyWindowStart,
		Metadata:           subscription.Metadata,
		CreatedAt:          subscription.CreatedAt,
		UpdatedAt:          subscription.UpdatedAt,
	}
}

func subscriptionFromModel(model *subscriptionModel) biz.UserSubscription {
	if model == nil {
		return biz.UserSubscription{}
	}
	return biz.UserSubscription{
		ID:                 model.ID,
		UserID:             model.UserID,
		GroupID:            model.GroupID,
		SubscriptionName:   model.SubscriptionName,
		Status:             biz.SubscriptionStatus(model.Status),
		StartsAt:           model.StartsAt,
		ExpiresAt:          model.ExpiresAt,
		DailyUsageUSD:      model.DailyUsageUSD,
		WeeklyUsageUSD:     model.WeeklyUsageUSD,
		MonthlyUsageUSD:    model.MonthlyUsageUSD,
		DailyWindowStart:   model.DailyWindowStart,
		WeeklyWindowStart:  model.WeeklyWindowStart,
		MonthlyWindowStart: model.MonthlyWindowStart,
		Metadata:           model.Metadata,
		CreatedAt:          model.CreatedAt,
		UpdatedAt:          model.UpdatedAt,
	}
}

func (r *Repository) createSubscriptionMemory(ctx context.Context, subscription *biz.UserSubscription) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	subscription.ID = r.nextSubID
	r.nextSubID++
	cloned := *subscription
	r.subscriptions[subscription.ID] = &cloned
	return nil
}

func (r *Repository) updateSubscriptionMemory(ctx context.Context, subscription *biz.UserSubscription) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	cloned := *subscription
	r.subscriptions[subscription.ID] = &cloned
	return nil
}

func (r *Repository) deleteSubscriptionMemory(ctx context.Context, subscriptionID int64) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	delete(r.subscriptions, subscriptionID)
	return nil
}

func (r *Repository) getSubscriptionByIDMemory(ctx context.Context, subscriptionID int64) (*biz.UserSubscription, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	subscription, ok := r.subscriptions[subscriptionID]
	if !ok {
		return nil, biz.ErrSubscriptionNotFound
	}
	cloned := *subscription
	return &cloned, nil
}

func (r *Repository) listSubscriptionsByUserMemory(ctx context.Context, userID int64) ([]*biz.UserSubscription, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	result := make([]*biz.UserSubscription, 0)
	for _, subscription := range r.subscriptions {
		if subscription.UserID != userID {
			continue
		}
		cloned := *subscription
		result = append(result, &cloned)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})
	return result, nil
}

func (r *Repository) listActiveSubscriptionsMemory(ctx context.Context) ([]*biz.UserSubscription, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	result := make([]*biz.UserSubscription, 0)
	for _, subscription := range r.subscriptions {
		if subscription.Status != biz.SubscriptionStatusActive {
			continue
		}
		cloned := *subscription
		result = append(result, &cloned)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ExpiresAt == result[j].ExpiresAt {
			return result[i].ID < result[j].ID
		}
		return result[i].ExpiresAt < result[j].ExpiresAt
	})
	return result, nil
}

func (r *Repository) listAllSubscriptionsMemory(ctx context.Context) ([]*biz.UserSubscription, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	result := make([]*biz.UserSubscription, 0, len(r.subscriptions))
	for _, subscription := range r.subscriptions {
		cloned := *subscription
		result = append(result, &cloned)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt == result[j].CreatedAt {
			return result[i].ID > result[j].ID
		}
		return result[i].CreatedAt > result[j].CreatedAt
	})
	return result, nil
}

func (r *Repository) getActiveSubscriptionByUserMemory(ctx context.Context, userID int64) (*biz.UserSubscription, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	var chosen *biz.UserSubscription
	for _, subscription := range r.subscriptions {
		if subscription.UserID != userID || subscription.Status != biz.SubscriptionStatusActive {
			continue
		}
		if chosen == nil || subscription.UpdatedAt > chosen.UpdatedAt || (subscription.UpdatedAt == chosen.UpdatedAt && subscription.ID > chosen.ID) {
			cloned := *subscription
			chosen = &cloned
		}
	}
	if chosen == nil {
		return nil, biz.ErrSubscriptionNotFound
	}
	return chosen, nil
}
