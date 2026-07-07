package data

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"micro-one-api/internal/billing/biz"
)

// operationReportRepo implements biz.OperationReportRepo against the billing
// database. It aggregates payment_orders and user_subscriptions so the report
// is built from authoritative ledger/order/subscription rows, not front-end
// sampling. Subscription rows are attributed to plans through metadata
// payment_trade_no -> payment_orders.plan_id.
type operationReportRepo struct {
	data *Data
}

func NewOperationReportRepo(data *Data) biz.OperationReportRepo {
	return &operationReportRepo{data: data}
}

// AggregatePaymentOrdersByPlan counts new purchases, renewals and refunds and
// sums revenue/refunded quota per plan within the time window. A purchase is
// a "renewal" when the user already had an active subscription in the same
// group at order creation; for the aggregation we approximate this by counting
// paid orders beyond the first per (user, plan) as renewals, which matches the
// AssignOrExtend semantics (first = new, subsequent = extend).
func (r *operationReportRepo) AggregatePaymentOrdersByPlan(ctx context.Context, startTime, endTime time.Time, planID, groupID int64, userID string) ([]biz.PlanOperationRow, error) {
	if r.data == nil || r.data.db == nil {
		return nil, nil
	}
	type row struct {
		PlanID      int64 `gorm:"column:plan_id"`
		GroupID     int64 `gorm:"column:group_id"`
		PaidCount   int64 `gorm:"column:paid_count"`
		RefundCount int64 `gorm:"column:refund_count"`
		Revenue     int64 `gorm:"column:revenue"`
		Refunded    int64 `gorm:"column:refunded"`
	}
	q := r.data.db.WithContext(ctx).Table("payment_orders").
		Select(`
			payment_orders.plan_id AS plan_id,
			payment_orders.group_id AS group_id,
			SUM(CASE WHEN payment_orders.status = 'paid' THEN 1 ELSE 0 END) AS paid_count,
			SUM(CASE WHEN payment_orders.status = 'refunded' THEN 1 ELSE 0 END) AS refund_count,
			SUM(CASE WHEN payment_orders.status = 'paid' THEN payment_orders.money_cents ELSE 0 END) AS revenue,
			SUM(CASE WHEN payment_orders.status = 'refunded' THEN payment_orders.money_cents ELSE 0 END) AS refunded
		`).
		Where("payment_orders.asset_type = ?", "subscription").
		Where("payment_orders.plan_id > 0").
		Where("payment_orders.created_at >= ?", startTime).
		Where("payment_orders.created_at <= ?", endTime).
		Group("payment_orders.plan_id, payment_orders.group_id")
	if planID > 0 {
		q = q.Where("payment_orders.plan_id = ?", planID)
	}
	if groupID > 0 {
		q = q.Where("payment_orders.group_id = ?", groupID)
	}
	if userID != "" {
		q = q.Where("payment_orders.user_id = ?", userID)
	}
	var rows []row
	if err := q.Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]biz.PlanOperationRow, 0, len(rows))
	for _, rr := range rows {
		out = append(out, biz.PlanOperationRow{
			PlanID:           rr.PlanID,
			PlanName:         fmt.Sprintf("plan-%d", rr.PlanID),
			GroupID:          rr.GroupID,
			NewPurchaseCount: 0,
			RenewalCount:     rr.PaidCount,
			RefundCount:      rr.RefundCount,
			RevenueQuota:     rr.Revenue / 100, // cents -> quota
			RefundedQuota:    rr.Refunded / 100,
		})
	}
	for i, rr := range out {
		newCount, renewalCount, err := r.splitPaidOrdersByPlan(ctx, startTime, endTime, rr.PlanID, rr.GroupID, userID)
		if err != nil {
			return nil, err
		}
		out[i].NewPurchaseCount = newCount
		out[i].RenewalCount = renewalCount
	}
	return out, nil
}

func (r *operationReportRepo) splitPaidOrdersByPlan(ctx context.Context, startTime, endTime time.Time, planID, groupID int64, userID string) (int64, int64, error) {
	type paidOrder struct {
		UserID string `gorm:"column:user_id"`
	}
	prior := r.data.db.WithContext(ctx).Table("payment_orders").
		Select("DISTINCT user_id").
		Where("asset_type = ? AND plan_id = ? AND group_id = ? AND status = ?", "subscription", planID, groupID, "paid").
		Where("created_at < ?", startTime)
	if userID != "" {
		prior = prior.Where("user_id = ?", userID)
	}
	var priorRows []paidOrder
	if err := prior.Scan(&priorRows).Error; err != nil {
		return 0, 0, err
	}
	seen := make(map[string]struct{}, len(priorRows))
	for _, row := range priorRows {
		seen[row.UserID] = struct{}{}
	}

	windowQ := r.data.db.WithContext(ctx).Table("payment_orders").
		Select("user_id").
		Where("asset_type = ? AND plan_id = ? AND group_id = ? AND status = ?", "subscription", planID, groupID, "paid").
		Where("created_at >= ? AND created_at <= ?", startTime, endTime).
		Order("created_at ASC, id ASC")
	if userID != "" {
		windowQ = windowQ.Where("user_id = ?", userID)
	}
	var windowRows []paidOrder
	if err := windowQ.Scan(&windowRows).Error; err != nil {
		return 0, 0, err
	}
	var newCount, renewalCount int64
	for _, row := range windowRows {
		if _, ok := seen[row.UserID]; ok {
			renewalCount++
			continue
		}
		seen[row.UserID] = struct{}{}
		newCount++
	}
	return newCount, renewalCount, nil
}

// CountSubscriptionsByStatus returns active/expired/revoked subscription counts
// keyed by plan_id. The plan linkage comes from the payment order's plan_id
// joined to the subscription via the traceability metadata
// (subscription.metadata -> payment_trade_no -> payment_orders.plan_id). When
// no link exists the subscription is excluded from the plan breakdown.
func (r *operationReportRepo) CountSubscriptionsByStatus(ctx context.Context, planID, groupID int64) (active, expired, revoked map[int64]int64, err error) {
	active = map[int64]int64{}
	expired = map[int64]int64{}
	revoked = map[int64]int64{}
	if r.data == nil || r.data.db == nil {
		return active, expired, revoked, nil
	}
	type subRow struct {
		Status   string `gorm:"column:status"`
		GroupID  int64  `gorm:"column:group_id"`
		Metadata string `gorm:"column:metadata"`
	}
	q := r.data.db.WithContext(ctx).Table("user_subscriptions").
		Select("status, group_id, metadata")
	if groupID > 0 {
		q = q.Where("group_id = ?", groupID)
	}
	var rows []subRow
	if err := q.Scan(&rows).Error; err != nil {
		return active, expired, revoked, err
	}
	tradeNos := make([]string, 0, len(rows))
	for _, rr := range rows {
		if tradeNo := paymentTradeNoFromMetadata(rr.Metadata); tradeNo != "" {
			tradeNos = append(tradeNos, tradeNo)
		}
	}
	planByTrade, err := r.paymentPlanByTradeNo(ctx, tradeNos)
	if err != nil {
		return active, expired, revoked, err
	}
	for _, rr := range rows {
		pid := planByTrade[paymentTradeNoFromMetadata(rr.Metadata)]
		if pid <= 0 {
			continue
		}
		if planID > 0 && pid != planID {
			continue
		}
		switch rr.Status {
		case "active":
			active[pid]++
		case "expired":
			expired[pid]++
		case "revoked":
			revoked[pid]++
		}
	}
	return active, expired, revoked, nil
}

// AggregateUsageFallbackByPlan sums subscription_cost and balance_cost per plan
// from the billing ledger, joined to payment_orders via the reservation
// reference to recover the plan. subscription_cost = quota absorbed by the
// subscription; balance_cost = quota that fell back to the wallet (余额兜底).
// The ratio balance_cost / (subscription_cost + balance_cost) is the fallback
// ratio.
func (r *operationReportRepo) AggregateUsageFallbackByPlan(ctx context.Context, startTime, endTime time.Time, planID, groupID int64, userID string) (subscriptionUsage, balanceFallback map[int64]int64, err error) {
	subscriptionUsage = map[int64]int64{}
	balanceFallback = map[int64]int64{}
	if r.data == nil || r.data.db == nil {
		return subscriptionUsage, balanceFallback, nil
	}
	type row struct {
		Metadata string `gorm:"column:metadata"`
		SubCost  int64  `gorm:"column:sub_cost"`
		BalCost  int64  `gorm:"column:bal_cost"`
	}
	// Attribute usage through the actual reservation subscription_id instead of
	// user_id. This prevents one consume ledger from being duplicated across
	// every plan the user has ever purchased.
	q := r.data.db.WithContext(ctx).Table("billing_ledgers AS l").
		Select("s.metadata AS metadata, SUM(CASE WHEN l.cost_source = 'subscription' THEN l.subscription_cost ELSE 0 END) AS sub_cost, SUM(CASE WHEN l.cost_source = 'balance' THEN l.balance_cost ELSE 0 END) AS bal_cost").
		Joins("JOIN billing_reservations res ON res.reservation_id = l.reference_id").
		Joins("JOIN user_subscriptions s ON s.id = res.subscription_id").
		Where("l.type = ?", "consume").
		Where("l.created_at >= ?", startTime).
		Where("l.created_at <= ?", endTime).
		Group("s.metadata")
	if groupID > 0 {
		q = q.Where("s.group_id = ?", groupID)
	}
	if userID != "" {
		q = q.Where("l.user_id = ?", userID)
	}
	var rows []row
	if err := q.Scan(&rows).Error; err != nil {
		return subscriptionUsage, balanceFallback, err
	}
	tradeNos := make([]string, 0, len(rows))
	for _, rr := range rows {
		if tradeNo := paymentTradeNoFromMetadata(rr.Metadata); tradeNo != "" {
			tradeNos = append(tradeNos, tradeNo)
		}
	}
	planByTrade, err := r.paymentPlanByTradeNo(ctx, tradeNos)
	if err != nil {
		return subscriptionUsage, balanceFallback, err
	}
	for _, rr := range rows {
		pid := planByTrade[paymentTradeNoFromMetadata(rr.Metadata)]
		if pid == 0 {
			continue
		}
		if planID > 0 && pid != planID {
			continue
		}
		subscriptionUsage[pid] += rr.SubCost
		balanceFallback[pid] += rr.BalCost
	}
	return subscriptionUsage, balanceFallback, nil
}

func paymentTradeNoFromMetadata(raw string) string {
	if raw == "" {
		return ""
	}
	var values map[string]string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return ""
	}
	return values["payment_trade_no"]
}

func (r *operationReportRepo) paymentPlanByTradeNo(ctx context.Context, tradeNos []string) (map[string]int64, error) {
	out := map[string]int64{}
	if len(tradeNos) == 0 {
		return out, nil
	}
	type row struct {
		TradeNo string `gorm:"column:trade_no"`
		PlanID  int64  `gorm:"column:plan_id"`
	}
	var rows []row
	if err := r.data.db.WithContext(ctx).Table("payment_orders").
		Select("trade_no, plan_id").
		Where("trade_no IN ? AND plan_id > 0", uniqueStrings(tradeNos)).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, rr := range rows {
		out[rr.TradeNo] = rr.PlanID
	}
	return out, nil
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
