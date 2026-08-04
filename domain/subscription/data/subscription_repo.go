package data

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"micro-one-api/domain/subscription/biz"

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
	RenewalStrategy    string  `gorm:"column:renewal_strategy"`
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

// CreateSubscriptionInTx inserts a subscription inside the caller's
// transaction (code-review 2026-07-30 billing-H2). The in-memory path has
// no transaction concept so it falls back to the memory create.
func (r *Repository) CreateSubscriptionInTx(ctx context.Context, tx biz.Tx, subscription *biz.UserSubscription) error {
	if tx == nil {
		return errors.New("nil transaction")
	}
	if r.db != nil {
		return r.createSubscriptionInTxDB(ctx, txDB(tx), subscription)
	}
	return r.createSubscriptionMemory(ctx, subscription)
}

func (r *Repository) UpdateSubscription(ctx context.Context, subscription *biz.UserSubscription) error {
	if r.db != nil {
		return r.updateSubscriptionDB(ctx, subscription)
	}
	return r.updateSubscriptionMemory(ctx, subscription)
}

func (r *Repository) UpdateSubscriptionInTx(ctx context.Context, tx biz.Tx, subscription *biz.UserSubscription) error {
	if r.db != nil {
		return updateSubscriptionWithTx(ctx, txDB(tx), subscription)
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

// GetActiveSubscriptionByUserInTx is the row-locked variant used by the
// payment assigner's in-tx path. The lock serialises concurrent
// grant/extend calls so two renewals for the same user cannot both
// observe "no active subscription" and both insert (code-review
// 2026-07-30 billing-H2 / domain-H1).
func (r *Repository) GetActiveSubscriptionByUserInTx(ctx context.Context, tx biz.Tx, userID int64) (*biz.UserSubscription, error) {
	if tx == nil {
		return nil, errors.New("nil transaction")
	}
	if r.db != nil {
		return r.getActiveSubscriptionByUserInTxDB(ctx, txDB(tx), userID)
	}
	return r.getActiveSubscriptionByUserMemory(ctx, userID)
}

func (r *Repository) AddUsage(ctx context.Context, userID int64, costUSD float64, now int64) error {
	if r.db != nil {
		return r.addUsageDB(ctx, userID, costUSD, now)
	}
	return r.addUsageMemory(ctx, userID, costUSD, now)
}

func (r *Repository) AddUsageByIDInTx(ctx context.Context, tx biz.Tx, subscriptionID int64, costUSD float64, now int64) error {
	if tx == nil {
		return errors.New("nil transaction")
	}
	return r.addUsageByIDInTxDB(ctx, txDB(tx), subscriptionID, costUSD, now)
}

func (r *Repository) GetByIDInTx(ctx context.Context, tx biz.Tx, subscriptionID int64) (*biz.UserSubscription, error) {
	if tx == nil {
		return nil, errors.New("nil transaction")
	}
	return r.getByIDInTxDB(ctx, txDB(tx), subscriptionID)
}

// addUsageByIDInTxDB performs the same read-roll-increment as
// addUsageDB, but operates on an explicit subscription id (not the
// user's active subscription) and inside the caller's transaction. The
// row lock is taken on the subscription id so concurrent commits to
// the same reservation cannot double-bill the subscription's quota.
func (r *Repository) addUsageByIDInTxDB(ctx context.Context, tx *gorm.DB, subscriptionID int64, costUSD float64, now int64) error {
	var model subscriptionModel
	q := tx.WithContext(ctx).Where("id = ?", subscriptionID)
	if !isSQLite(dialectorName(tx)) {
		q = q.Clauses(forUpdateClause(dialectorName(tx)))
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
	if !isSQLite(dialectorName(tx)) {
		q = q.Clauses(forUpdateClause(dialectorName(tx)))
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
		// The unique index on (active_user_id) (review H10) makes a concurrent
		// duplicate-active creation collide here. Map it to the sentinel so the
		// usecase layer returns ErrSubscriptionAlreadyAssigned instead of a
		// raw driver error.
		if isDuplicateKeyErr(err) {
			return biz.ErrSubscriptionAlreadyAssigned
		}
		return err
	}
	subscription.ID = model.ID
	return nil
}

// createSubscriptionInTxDB is the in-transaction variant of
// createSubscriptionDB. It shares the duplicate-key -> already-assigned
// mapping so a concurrent grant inside the same tx shape surfaces the
// sentinel error to the caller.
func (r *Repository) createSubscriptionInTxDB(ctx context.Context, tx *gorm.DB, subscription *biz.UserSubscription) error {
	model := subscriptionToModel(subscription)
	if err := tx.WithContext(ctx).Create(&model).Error; err != nil {
		if isDuplicateKeyErr(err) {
			return biz.ErrSubscriptionAlreadyAssigned
		}
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

// subscriptionFieldColumns maps the semantic biz.SubscriptionField tags to the
// concrete storage columns a selective update should write. It is the data-side
// counterpart of biz.SubscriptionField and keeps the biz package free of column
// names (code-review 2026-07-30 domain-H1).
func subscriptionFieldColumns(fields []biz.SubscriptionField) map[string]any {
	seen := make(map[biz.SubscriptionField]struct{}, len(fields))
	for _, f := range fields {
		seen[f] = struct{}{}
	}
	now := true
	cols := make(map[string]any)
	for f := range seen {
		switch f {
		case biz.SubscriptionFieldStatus:
			cols["status"] = nil
		case biz.SubscriptionFieldExpiresAt:
			cols["expires_at"] = nil
		case biz.SubscriptionFieldSubscriptionName:
			cols["subscription_name"] = nil
		case biz.SubscriptionFieldGroupID:
			cols["group_id"] = nil
		case biz.SubscriptionFieldMetadata:
			cols["metadata"] = nil
		case biz.SubscriptionFieldRenewalStrategy:
			cols["renewal_strategy"] = nil
		case biz.SubscriptionFieldUsageAll:
			cols["daily_usage_usd"] = nil
			cols["weekly_usage_usd"] = nil
			cols["monthly_usage_usd"] = nil
			cols["daily_window_start"] = nil
			cols["weekly_window_start"] = nil
			cols["monthly_window_start"] = nil
		default:
			// Unknown tag: ignore rather than panic so a stale caller never
			// breaks the write.
			now = false
		}
	}
	if now {
		cols["updated_at"] = nil
	}
	return cols
}

// updateSubscriptionFieldsWithTx writes ONLY the columns named by fields (plus
// updated_at) for the given subscription id, using the DO's current values. It
// never touches columns that are not listed, so a concurrent AddUsage
// increment committed between the caller's read and this write is preserved
// (code-review 2026-07-30 domain-H1).
func updateSubscriptionFieldsWithTx(ctx context.Context, tx *gorm.DB, subscription *biz.UserSubscription, fields []biz.SubscriptionField) error {
	if subscription == nil {
		return errors.New("nil subscription")
	}
	cols := subscriptionFieldColumns(fields)
	// "updated_at" is always injected by subscriptionFieldColumns, so subtract
	// it when deciding whether any concrete column was selected.
	_, hasUpdatedAt := cols["updated_at"]
	if len(cols) == 0 || (len(cols) == 1 && hasUpdatedAt) {
		// No concrete columns selected: nothing to do.
		return nil
	}
	model := subscriptionToModel(subscription)
	values := make(map[string]any, len(cols))
	for col := range cols {
		switch col {
		case "status":
			values[col] = model.Status
		case "expires_at":
			values[col] = model.ExpiresAt
		case "subscription_name":
			values[col] = model.SubscriptionName
		case "group_id":
			values[col] = model.GroupID
		case "metadata":
			values[col] = model.Metadata
		case "renewal_strategy":
			values[col] = model.RenewalStrategy
		case "daily_usage_usd":
			values[col] = model.DailyUsageUSD
		case "weekly_usage_usd":
			values[col] = model.WeeklyUsageUSD
		case "monthly_usage_usd":
			values[col] = model.MonthlyUsageUSD
		case "daily_window_start":
			values[col] = model.DailyWindowStart
		case "weekly_window_start":
			values[col] = model.WeeklyWindowStart
		case "monthly_window_start":
			values[col] = model.MonthlyWindowStart
		case "updated_at":
			values[col] = model.UpdatedAt
		}
	}
	res := tx.WithContext(ctx).Model(&subscriptionModel{}).Where("id = ?", subscription.ID).Updates(values)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return biz.ErrSubscriptionNotFound
	}
	return nil
}

// UpdateSubscriptionFields is the selective-write variant of UpdateSubscription.
// See updateSubscriptionFieldsWithTx for the column-narrowing rationale.
func (r *Repository) UpdateSubscriptionFields(ctx context.Context, subscription *biz.UserSubscription, fields []biz.SubscriptionField) error {
	if r.db != nil {
		return updateSubscriptionFieldsWithTx(ctx, r.db.WithContext(ctx), subscription, fields)
	}
	return r.updateSubscriptionFieldsMemory(ctx, subscription, fields)
}

// UpdateSubscriptionFieldsInTx is the in-transaction selective-write variant.
func (r *Repository) UpdateSubscriptionFieldsInTx(ctx context.Context, tx biz.Tx, subscription *biz.UserSubscription, fields []biz.SubscriptionField) error {
	if tx == nil {
		return errors.New("nil transaction")
	}
	if r.db != nil {
		return updateSubscriptionFieldsWithTx(ctx, txDB(tx), subscription, fields)
	}
	return r.updateSubscriptionFieldsMemory(ctx, subscription, fields)
}

func (r *Repository) updateSubscriptionFieldsMemory(ctx context.Context, subscription *biz.UserSubscription, fields []biz.SubscriptionField) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	existing, ok := r.subscriptions[subscription.ID]
	if !ok {
		return biz.ErrSubscriptionNotFound
	}
	// Start from the stored row so unlisted columns keep their live values.
	merged := *existing
	now := true
	seen := make(map[biz.SubscriptionField]struct{}, len(fields))
	for _, f := range fields {
		seen[f] = struct{}{}
	}
	for f := range seen {
		switch f {
		case biz.SubscriptionFieldStatus:
			merged.Status = subscription.Status
		case biz.SubscriptionFieldExpiresAt:
			merged.ExpiresAt = subscription.ExpiresAt
		case biz.SubscriptionFieldSubscriptionName:
			merged.SubscriptionName = subscription.SubscriptionName
		case biz.SubscriptionFieldGroupID:
			merged.GroupID = subscription.GroupID
		case biz.SubscriptionFieldMetadata:
			merged.Metadata = subscription.Metadata
		case biz.SubscriptionFieldRenewalStrategy:
			merged.RenewalStrategy = subscription.RenewalStrategy
		case biz.SubscriptionFieldUsageAll:
			merged.DailyUsageUSD = subscription.DailyUsageUSD
			merged.WeeklyUsageUSD = subscription.WeeklyUsageUSD
			merged.MonthlyUsageUSD = subscription.MonthlyUsageUSD
			merged.DailyWindowStart = subscription.DailyWindowStart
			merged.WeeklyWindowStart = subscription.WeeklyWindowStart
			merged.MonthlyWindowStart = subscription.MonthlyWindowStart
		default:
			now = false
		}
	}
	if now {
		merged.UpdatedAt = subscription.UpdatedAt
	}
	cloned := merged
	r.subscriptions[subscription.ID] = &cloned
	return nil
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
	// Code-review 2026-07-30 domain-C1: defence-in-depth. The
	// SubscriptionExpiryChecker is the primary mechanism that flips an active
	// subscription to expired, but it is best-effort and hourly. A read path
	// that only filters on status = 'active' would keep serving a subscription
	// whose expires_at has already passed (free quota) for up to an hour after
	// expiry, and forever if the checker is ever mis-wired or delayed. We
	// therefore also require expires_at > now here so the active set is correct
	// regardless of the checker. The dedicated expiry filter still runs in the
	// checker to actually persist the status transition for reporting.
	if err := r.db.WithContext(ctx).
		Where("user_id = ? AND status = ? AND expires_at > ?", userID, string(biz.SubscriptionStatusActive), time.Now().Unix()).
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

// getActiveSubscriptionByUserInTxDB is the row-locked variant of
// getActiveSubscriptionByUserDB. It takes a SELECT ... FOR UPDATE lock
// on the active row so the subsequent extend happens against a stable
// snapshot (code-review 2026-07-30 billing-H2 / domain-H1).
func (r *Repository) getActiveSubscriptionByUserInTxDB(ctx context.Context, tx *gorm.DB, userID int64) (*biz.UserSubscription, error) {
	var model subscriptionModel
	// domain-C1: same expires_at > now defence-in-depth as
	// getActiveSubscriptionByUserDB (see comment there).
	q := tx.WithContext(ctx).
		Where("user_id = ? AND status = ? AND expires_at > ?", userID, string(biz.SubscriptionStatusActive), time.Now().Unix()).
		Order("updated_at DESC, id DESC")
	if !isSQLite(dialectorName(tx)) {
		q = q.Clauses(forUpdateClause(dialectorName(tx)))
	}
	if err := q.First(&model).Error; err != nil {
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
		RenewalStrategy:    subscription.RenewalStrategy,
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
		RenewalStrategy:    model.RenewalStrategy,
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
	now := time.Now().Unix()
	var chosen *biz.UserSubscription
	for _, subscription := range r.subscriptions {
		// domain-C1: same expires_at > now defence-in-depth as the DB path.
		if subscription.UserID != userID || subscription.Status != biz.SubscriptionStatusActive || subscription.ExpiresAt <= now {
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

// isDuplicateKeyErr reports whether err is a unique-constraint violation,
// covering MySQL, SQLite and Postgres drivers used by this project. Mirrors
// the helper in internal/channel/data.
func isDuplicateKeyErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "Duplicate entry") ||
		strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "duplicate key value") ||
		strings.Contains(msg, "uniq_user_subs_active_user_id")
}
