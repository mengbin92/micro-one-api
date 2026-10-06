package biz

import (
	"context"
	"fmt"
	"micro-one-api/domain/authorization"
	"time"
)

// PlanOperationRow is one row of the subscription operational report,
// aggregated by plan. It mirrors the proto PlanOperationRow.
type PlanOperationRow struct {
	PlanID               int64
	PlanName             string
	GroupID              int64
	NewPurchaseCount     int64
	RenewalCount         int64
	RefundCount          int64
	RevenueQuota         int64
	RefundedQuota        int64
	ActiveSubscriptions  int64
	ExpiredSubscriptions int64
	RevokedSubscriptions int64
	// SubscriptionUsageQuota is the total quota absorbed by subscriptions
	// purchased under this plan (cost_source=subscription). BalanceFallbackQuota
	// is the quota that fell back to the wallet (cost_source=balance) because
	// the subscription's window was exhausted. The ratio
	// BalanceFallbackQuota / (SubscriptionUsageQuota + BalanceFallbackQuota)
	// is the "余额兜底比例" the roadmap asks for.
	SubscriptionUsageQuota int64
	BalanceFallbackQuota   int64
}

// SubscriptionOperationReport is the aggregated result.
type SubscriptionOperationReport struct {
	Rows               []PlanOperationRow
	TotalRevenueQuota  int64
	TotalRefundedQuota int64
}

// OperationReportRepo is the narrow aggregation interface the report usecase
// needs. It is kept separate from the per-entity repos so the aggregation
// queries can be tuned independently of the OLTP writes.
type OperationReportRepo interface {
	// AggregatePaymentOrdersByPlan counts new purchases, renewals and refunds
	// and sums revenue/refunded quota per plan within the time window. The
	// plan_id/group_id/userID filters are optional (zero/empty = no filter).
	AggregatePaymentOrdersByPlan(ctx context.Context, startTime, endTime time.Time, planID, groupID int64, userID string) ([]PlanOperationRow, error)
	// CountSubscriptionsByStatus returns active/expired/revoked subscription
	// counts grouped by plan. The plan snapshot on payment_orders links a
	// subscription to the plan it was purchased under; when no link exists
	// (admin-assigned subscriptions) the row is excluded.
	CountSubscriptionsByStatus(ctx context.Context, planID, groupID int64) (active, expired, revoked map[int64]int64, err error)
	// AggregateUsageFallbackByPlan sums subscription_cost and balance_cost
	// per plan so the report can show the 余额兜底比例 (how much of a plan's
	// usage fell back to the wallet after the subscription window was
	// exhausted).
	AggregateUsageFallbackByPlan(ctx context.Context, startTime, endTime time.Time, planID, groupID int64, userID string) (subscriptionUsage, balanceFallback map[int64]int64, err error)
}

// SubscriptionReportUsecase builds the operational report from ledger/order/
// subscription aggregation so the dashboard never depends on front-end
// sampling.
type SubscriptionReportUsecase struct {
	authorization authorization.Resolver
	repo          OperationReportRepo
	now           func() time.Time
}

func NewSubscriptionReportUsecase(repo OperationReportRepo) *SubscriptionReportUsecase {
	return &SubscriptionReportUsecase{repo: repo, now: time.Now}
}

// BuildReport aggregates the plan-dimension report. When startTime/endTime
// are zero the window defaults to the last 30 days.
func (uc *SubscriptionReportUsecase) BuildReport(ctx context.Context, startTime, endTime time.Time, planID, groupID int64, userID string) (*SubscriptionOperationReport, error) {
	var authErr error
	ctx, authErr = prepareBilling(ctx, uc.authorization, "subscription.user_subscriptions", "subscription.user_subscription.report.read")
	if authErr != nil {
		return nil, authErr
	}
	ctx, authErr = prepareBilling(ctx, uc.authorization, "billing.payments.list", "billing.payment.list")
	if authErr != nil {
		return nil, authErr
	}
	ctx, authErr = prepareBilling(ctx, uc.authorization, "billing.ledger.read", "billing.account.ledger.read")
	if authErr != nil {
		return nil, authErr
	}
	ctx = context.WithValue(ctx, subscriptionReportFilterKey{}, userID)
	if uc == nil || uc.repo == nil {
		return nil, fmt.Errorf("subscription report usecase is not configured")
	}
	now := uc.now()
	if startTime.IsZero() {
		startTime = now.AddDate(0, 0, -30)
	}
	if endTime.IsZero() {
		endTime = now
	}
	rows, err := uc.repo.AggregatePaymentOrdersByPlan(ctx, startTime, endTime, planID, groupID, userID)
	if err != nil {
		return nil, fmt.Errorf("aggregate payment orders: %w", err)
	}
	active, expired, revoked, err := uc.repo.CountSubscriptionsByStatus(ctx, planID, groupID)
	if err != nil {
		return nil, fmt.Errorf("count subscriptions by status: %w", err)
	}
	// Merge the subscription status counts into the per-plan rows. The
	// payment aggregation keys rows by plan_id; the status counts key by
	// plan_id too (best-effort from snapshot linkage).
	for i := range rows {
		pid := rows[i].PlanID
		rows[i].ActiveSubscriptions = active[pid]
		rows[i].ExpiredSubscriptions = expired[pid]
		rows[i].RevokedSubscriptions = revoked[pid]
	}
	// Subscription usage vs balance fallback ratio (余额兜底比例).
	subUsage, balFallback, err := uc.repo.AggregateUsageFallbackByPlan(ctx, startTime, endTime, planID, groupID, userID)
	if err != nil {
		return nil, fmt.Errorf("aggregate usage fallback: %w", err)
	}
	for i := range rows {
		pid := rows[i].PlanID
		rows[i].SubscriptionUsageQuota = subUsage[pid]
		rows[i].BalanceFallbackQuota = balFallback[pid]
	}
	report := &SubscriptionOperationReport{Rows: rows}
	for _, r := range rows {
		report.TotalRevenueQuota += r.RevenueQuota
		report.TotalRefundedQuota += r.RefundedQuota
	}
	return report, nil
}

type subscriptionReportFilterKey struct{}

func SubscriptionReportUserFilter(ctx context.Context) string {
	v, _ := ctx.Value(subscriptionReportFilterKey{}).(string)
	return v
}
