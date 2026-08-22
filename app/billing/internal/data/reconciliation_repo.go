package data

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"micro-one-api/app/billing/internal/biz"
)

type reconciliationRepo struct {
	data *Data
}

// NewReconciliationRepo creates a new ReconciliationRepo.
func NewReconciliationRepo(data *Data) biz.ReconciliationRepo {
	return &reconciliationRepo{data: data}
}

func (r *reconciliationRepo) ListAllAccounts(ctx context.Context) ([]*biz.Account, error) {
	var models []accountModel
	if err := r.data.db.WithContext(ctx).Find(&models).Error; err != nil {
		return nil, err
	}

	accounts := make([]*biz.Account, len(models))
	for i, m := range models {
		accounts[i] = &biz.Account{
			UserID:       fmt.Sprintf("%d", m.ID),
			Username:     m.Username,
			DisplayName:  m.DisplayName,
			Group:        m.Group,
			Balance:      m.Balance,
			UsedAmount:   m.UsedAmount,
			RequestCount: m.RequestCount,
			FrozenAmount: m.FrozenAmount,
			Status:       m.Status,
		}
	}
	return accounts, nil
}

func (r *reconciliationRepo) LatestLedgerBalanceAfter(ctx context.Context, userID string) (int64, bool, error) {
	var row struct {
		BalanceAfter int64 `gorm:"column:balance_after"`
	}
	err := r.data.db.WithContext(ctx).Model(&ledgerModel{}).
		Select("balance_after").
		Where("user_id = ?", userID).
		Order("created_at DESC, id DESC").
		Take(&row).Error
	if err == gorm.ErrRecordNotFound {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return row.BalanceAfter, true, nil
}

type reconciliationChannelUsageModel struct {
	ID        int64 `gorm:"column:id"`
	UsedQuota int64 `gorm:"column:used_quota"`
}

func (reconciliationChannelUsageModel) TableName() string { return "channels" }

func (r *reconciliationRepo) ListChannelUsage(ctx context.Context) ([]*biz.ChannelUsageSnapshot, error) {
	var models []reconciliationChannelUsageModel
	if err := r.data.db.WithContext(ctx).
		Select("id, used_quota").
		Find(&models).Error; err != nil {
		return nil, err
	}
	out := make([]*biz.ChannelUsageSnapshot, len(models))
	for i, m := range models {
		out[i] = &biz.ChannelUsageSnapshot{
			ChannelID: m.ID,
			UsedQuota: m.UsedQuota,
		}
	}
	return out, nil
}

func (r *reconciliationRepo) SumConsumeLedgerUsageByChannel(ctx context.Context) ([]*biz.ChannelLedgerUsage, error) {
	type row struct {
		ChannelID    int64 `gorm:"column:channel_id"`
		Quota        int64 `gorm:"column:quota"`
		UpstreamCost int64 `gorm:"column:upstream_cost"`
	}
	var rows []row
	if err := r.data.db.WithContext(ctx).
		Table("(?) AS request_usage", r.data.db.Model(&ledgerModel{}).
			Select("reference_id, MAX(channel_id) AS channel_id, MAX(quota) AS quota, SUM(upstream_cost) AS upstream_cost").
			Where("type = ? AND channel_id > 0 AND source_kind = ?", biz.LedgerTypeConsume, biz.CostSourceChannel).
			Group("reference_id"),
		).
		Select("channel_id, COALESCE(SUM(quota), 0) AS quota, COALESCE(SUM(upstream_cost), 0) AS upstream_cost").
		Group("channel_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]*biz.ChannelLedgerUsage, len(rows))
	for i, row := range rows {
		out[i] = &biz.ChannelLedgerUsage{
			ChannelID:    row.ChannelID,
			Quota:        row.Quota,
			UpstreamCost: row.UpstreamCost,
		}
	}
	return out, nil
}

func (r *reconciliationRepo) GetLedgerConsumeSummary(ctx context.Context) (*biz.ConsumeSummary, error) {
	var summary biz.ConsumeSummary
	if err := r.data.db.WithContext(ctx).
		Table("(?) AS consumes", r.data.db.Model(&ledgerModel{}).
			Select("reference_id, MAX(quota) AS quota").
			Where("type = ?", biz.LedgerTypeConsume).
			Group("reference_id")).
		Select("COUNT(*) AS count, COALESCE(SUM(quota), 0) AS quota").
		Scan(&summary).Error; err != nil {
		return nil, err
	}
	return &summary, nil
}

type reconciliationLogModel struct {
	ID    int64 `gorm:"column:id"`
	Quota int64 `gorm:"column:quota"`
}

func (reconciliationLogModel) TableName() string { return "logs" }

func (r *reconciliationRepo) GetLogConsumeSummary(ctx context.Context) (*biz.ConsumeSummary, error) {
	var summary biz.ConsumeSummary
	if err := r.data.db.WithContext(ctx).
		Model(&reconciliationLogModel{}).
		Select("COUNT(*) AS count, COALESCE(SUM(quota), 0) AS quota").
		Where("level = ?", biz.LedgerTypeConsume).
		Scan(&summary).Error; err != nil {
		return nil, err
	}
	return &summary, nil
}

func (r *reconciliationRepo) ListReservationsByStatus(ctx context.Context, status string) ([]*biz.Reservation, error) {
	var models []reservationModel
	if err := r.data.db.WithContext(ctx).
		Where("status = ?", status).
		Find(&models).Error; err != nil {
		return nil, err
	}

	reservations := make([]*biz.Reservation, len(models))
	for i, m := range models {
		reservations[i] = &biz.Reservation{
			ReservationID: m.ReservationID,
			UserID:        m.UserID,
			RequestID:     m.RequestID,
			Amount:        m.Amount,
			Status:        m.Status,
			Model:         stringFromPtr(m.Model),
			ChannelID:     stringFromPtr(m.ChannelID),
			CreatedAt:     m.CreatedAt,
			UpdatedAt:     m.UpdatedAt,
			ExpiredAt:     timeFromPtr(m.ExpiredAt),
		}
	}
	return reservations, nil
}

// subscriptionRow is the bare-bones read of the subscription table. The
// billing domain only needs the per-window usage columns for
// reconciliation; it never writes to the subscription table. The struct
// intentionally mirrors the columns we care about, not the entire
// subscription model (which lives in the subscription domain).
type subscriptionRow struct {
	ID                 int64   `gorm:"column:id"`
	UserID             int64   `gorm:"column:user_id"`
	GroupID            int64   `gorm:"column:group_id"`
	Status             string  `gorm:"column:status"`
	DailyUsageUSD      float64 `gorm:"column:daily_usage_usd"`
	WeeklyUsageUSD     float64 `gorm:"column:weekly_usage_usd"`
	MonthlyUsageUSD    float64 `gorm:"column:monthly_usage_usd"`
	DailyWindowStart   int64   `gorm:"column:daily_window_start"`
	WeeklyWindowStart  int64   `gorm:"column:weekly_window_start"`
	MonthlyWindowStart int64   `gorm:"column:monthly_window_start"`
	RateMultiplier     float64 `gorm:"column:rate_multiplier"`
}

func (subscriptionRow) TableName() string { return "user_subscriptions" }

func (r *reconciliationRepo) ListActiveSubscriptions(ctx context.Context) ([]*biz.SubscriptionUsageSnapshot, error) {
	var rows []subscriptionRow
	if err := r.data.db.WithContext(ctx).
		Table("user_subscriptions AS s").
		Select("s.*, COALESCE(g.rate_multiplier, 1) AS rate_multiplier").
		Joins("LEFT JOIN subscription_groups g ON g.id = s.group_id").
		Where("s.status = ?", "active").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]*biz.SubscriptionUsageSnapshot, len(rows))
	for i, row := range rows {
		out[i] = &biz.SubscriptionUsageSnapshot{
			SubscriptionID:     row.ID,
			UserID:             row.UserID,
			GroupID:            row.GroupID,
			Status:             row.Status,
			DailyUsageUSD:      row.DailyUsageUSD,
			WeeklyUsageUSD:     row.WeeklyUsageUSD,
			MonthlyUsageUSD:    row.MonthlyUsageUSD,
			DailyWindowStart:   row.DailyWindowStart,
			WeeklyWindowStart:  row.WeeklyWindowStart,
			MonthlyWindowStart: row.MonthlyWindowStart,
			RateMultiplier:     row.RateMultiplier,
		}
	}
	return out, nil
}

func (r *reconciliationRepo) SumSubscriptionCostSince(ctx context.Context, subscriptionID int64, since time.Time) (int64, error) {
	var total int64
	err := r.data.db.WithContext(ctx).
		Table("billing_ledgers AS l").
		Joins("JOIN billing_reservations r ON r.reservation_id = l.reference_id").
		Where("r.subscription_id = ?", subscriptionID).
		Where("l.type = ? AND l.cost_source = ?", biz.LedgerTypeConsume, biz.CostSourceSubscription).
		Where("l.created_at >= ?", since).
		Select("COALESCE(SUM(l.subscription_cost), 0)").
		Scan(&total).Error
	return total, err
}

func (r *reconciliationRepo) SumPendingReceivables(ctx context.Context) (int64, error) {
	var total int64
	if err := r.data.db.WithContext(ctx).
		Model(&accountReceivableModel{}).
		Where("status = ?", biz.ReceivableStatusPending).
		Select("COALESCE(SUM(overdue_quota), 0)").
		Scan(&total).Error; err != nil {
		return 0, err
	}
	return total, nil
}

func (r *reconciliationRepo) SumOverdraftBalances(ctx context.Context) (int64, error) {
	var total int64
	if err := r.data.db.WithContext(ctx).
		Table("users").
		Where("balance < 0").
		Select("COALESCE(SUM(-balance), 0)").
		Scan(&total).Error; err != nil {
		return 0, err
	}
	return total, nil
}

// SumReversalLedgerAmounts returns the total amount of reversal ledger entries
// (type=refund, cost_source=reversal). This is the wallet-side view of refunds
// used by the phase 2.3 reconciliation coverage check.
func (r *reconciliationRepo) SumReversalLedgerAmounts(ctx context.Context) (int64, error) {
	if r.data == nil || r.data.db == nil {
		return 0, nil
	}
	var total int64
	err := r.data.db.WithContext(ctx).Table("billing_ledgers").
		Select("COALESCE(SUM(amount), 0)").
		Where("type = ? AND cost_source = ?", biz.LedgerTypeRefund, biz.CostSourceReversal).
		Scan(&total).Error
	return total, err
}

// CountRefundedOrders returns the count and total money_cents of payment orders
// in the refunded terminal state (phase 2.3 reconciliation coverage).
func (r *reconciliationRepo) CountRefundedOrders(ctx context.Context) (int64, int64, error) {
	if r.data == nil || r.data.db == nil {
		return 0, 0, nil
	}
	type agg struct {
		Count int64 `gorm:"column:cnt"`
		Total int64 `gorm:"column:total_cents"`
	}
	var row agg
	err := r.data.db.WithContext(ctx).Table("payment_orders").
		Select("COUNT(*) AS cnt, COALESCE(SUM(money_cents), 0) AS total_cents").
		Where("status = ?", biz.PaymentOrderStatusRefunded).
		Scan(&row).Error
	return row.Count, row.Total, err
}

// ListStuckIssuedOrders returns paid SUBSCRIPTION payment orders whose asset
// issuance was claimed (asset_issue_status=issued) but never fulfilled
// (subscription_id=0). These are stuck orders produced when the admin
// CompleteSubscriptionPurchase compensation (Unmark) fails after a fulfilment
// failure, leaving the order issued-but-unfulfilled (code-review M1). The
// reconciler reports them so an operator can re-trigger completion; it does
// not auto-repair.
//
// Balance orders (asset_type=balance) are deliberately excluded: their
// subscription_id is always 0 by design (the asset is wallet credit issued
// inline at MarkOrderPaid, not a subscription grant), so the
// paid+issued+subscription_id=0 predicate would otherwise false-positive on
// every paid balance order (v0.18 P1 C6 finding, 2026-08-10).
func (r *reconciliationRepo) ListStuckIssuedOrders(ctx context.Context) ([]*biz.PaymentOrder, error) {
	if r.data == nil || r.data.db == nil {
		return nil, nil
	}
	var rows []PaymentOrder
	if err := r.data.db.WithContext(ctx).
		Where("status = ? AND asset_issue_status = ? AND subscription_id = 0 AND asset_type <> ?",
			biz.PaymentOrderStatusPaid, biz.PaymentAssetIssueStatusIssued, biz.PaymentAssetTypeBalance).
		Order("updated_at ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	orders := make([]*biz.PaymentOrder, len(rows))
	for i := range rows {
		order, err := toBizPaymentOrder(&rows[i])
		if err != nil {
			return nil, err
		}
		orders[i] = order
	}
	return orders, nil
}
