package data

import (
	"context"
	"fmt"

	"micro-one-api/domain/subscription/biz"
)

type windowChargeModel struct {
	ReservationID      string  `gorm:"column:reservation_id;primaryKey"`
	SubscriptionID     int64   `gorm:"column:subscription_id"`
	QuotaPolicyID      int64   `gorm:"column:quota_policy_id"`
	DailyWindowStart   int64   `gorm:"column:daily_window_start"`
	WeeklyWindowStart  int64   `gorm:"column:weekly_window_start"`
	MonthlyWindowStart int64   `gorm:"column:monthly_window_start"`
	AccountingUSD      float64 `gorm:"column:accounting_usd"`
	CreatedAt          int64   `gorm:"column:created_at"`
}

func (windowChargeModel) TableName() string { return "subscription_window_charges" }

func (r *Repository) AddFrozenUsageInTx(ctx context.Context, tx biz.Tx, c biz.FrozenWindowCharge) error {
	if tx == nil {
		return fmt.Errorf("nil frozen usage transaction")
	}
	sub, err := r.GetByIDInTx(ctx, tx, c.SubscriptionID)
	if err != nil {
		return err
	}
	if sub == nil {
		return biz.ErrSubscriptionNotFound
	}
	db := txDB(tx).WithContext(ctx)
	if err := db.Create(&windowChargeModel{ReservationID: c.ReservationID, SubscriptionID: c.SubscriptionID, QuotaPolicyID: c.QuotaPolicyID, DailyWindowStart: c.DailyWindowStart, WeeklyWindowStart: c.WeeklyWindowStart, MonthlyWindowStart: c.MonthlyWindowStart, AccountingUSD: c.AccountingUSD, CreatedAt: c.CreatedAt}).Error; err != nil {
		return err
	}
	// History survives a roll, revoke or contract replacement. A charge whose
	// frozen window matches the current window (or initializes it) increments
	// that window; when the window rolled between reserve and commit, the
	// admitted spend counts against the new window so its limits reflect
	// reality. Each charge increments exactly once (PK reservation_id).
	if sub.GroupID != c.QuotaPolicyID {
		return nil
	}
	sub = biz.RollUsageWindowsPure(sub, c.CreatedAt)
	updates := map[string]any{"daily_window_start": sub.DailyWindowStart, "weekly_window_start": sub.WeeklyWindowStart, "monthly_window_start": sub.MonthlyWindowStart, "daily_usage_usd": sub.DailyUsageUSD, "weekly_usage_usd": sub.WeeklyUsageUSD, "monthly_usage_usd": sub.MonthlyUsageUSD}
	if sub.DailyWindowStart == c.DailyWindowStart || sub.DailyWindowStart == 0 {
		updates["daily_usage_usd"] = sub.DailyUsageUSD + c.AccountingUSD
		updates["daily_window_start"] = c.DailyWindowStart
	} else if c.DailyWindowStart > 0 && c.DailyWindowStart < sub.DailyWindowStart {
		updates["daily_usage_usd"] = sub.DailyUsageUSD + c.AccountingUSD
	}
	if sub.WeeklyWindowStart == c.WeeklyWindowStart || sub.WeeklyWindowStart == 0 {
		updates["weekly_usage_usd"] = sub.WeeklyUsageUSD + c.AccountingUSD
		updates["weekly_window_start"] = c.WeeklyWindowStart
	} else if c.WeeklyWindowStart > 0 && c.WeeklyWindowStart < sub.WeeklyWindowStart {
		updates["weekly_usage_usd"] = sub.WeeklyUsageUSD + c.AccountingUSD
	}
	if sub.MonthlyWindowStart == c.MonthlyWindowStart || sub.MonthlyWindowStart == 0 {
		updates["monthly_usage_usd"] = sub.MonthlyUsageUSD + c.AccountingUSD
		updates["monthly_window_start"] = c.MonthlyWindowStart
	} else if c.MonthlyWindowStart > 0 && c.MonthlyWindowStart < sub.MonthlyWindowStart {
		updates["monthly_usage_usd"] = sub.MonthlyUsageUSD + c.AccountingUSD
	}
	updates["updated_at"] = c.CreatedAt
	return db.Model(&subscriptionModel{}).Where("id = ?", c.SubscriptionID).Updates(updates).Error
}
