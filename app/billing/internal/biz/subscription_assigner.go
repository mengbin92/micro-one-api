package biz

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"micro-one-api/pkg/jsonx"

	subscriptionbiz "micro-one-api/domain/subscription/biz"
)

const subscriptionSecondsPerDay = 24 * 60 * 60

type SubscriptionAssignmentUsecase interface {
	Assign(ctx context.Context, req *subscriptionbiz.AssignSubscriptionRequest) (*subscriptionbiz.UserSubscription, error)
	AssignOrExtend(ctx context.Context, req *subscriptionbiz.AssignSubscriptionRequest) (*subscriptionbiz.UserSubscription, bool, error)
	// AssignOrExtendInTx is the in-transaction variant used by the
	// payment assigner's in-tx path so the grant/extension commits or
	// rolls back with the payment order status transition (code-review
	// 2026-07-30 billing-H2).
	AssignOrExtendInTx(ctx context.Context, tx subscriptionbiz.Tx, req *subscriptionbiz.AssignSubscriptionRequest) (*subscriptionbiz.UserSubscription, bool, error)
}

type SubscriptionGroupGetter interface {
	GetGroupByID(ctx context.Context, groupID int64) (*subscriptionbiz.SubscriptionGroup, error)
}

type SubscriptionPlanGetter interface {
	GetPlanByID(ctx context.Context, planID int64) (*subscriptionbiz.SubscriptionPlan, error)
}

type paymentSubscriptionAssigner struct {
	subscriptions SubscriptionAssignmentUsecase
	groups        SubscriptionGroupGetter
	plans         SubscriptionPlanGetter
	now           func() time.Time
}

func NewPaymentSubscriptionAssigner(subscriptions SubscriptionAssignmentUsecase, groups SubscriptionGroupGetter, plans ...SubscriptionPlanGetter) SubscriptionAssigner {
	var planGetter SubscriptionPlanGetter
	if len(plans) > 0 {
		planGetter = plans[0]
	}
	return &paymentSubscriptionAssigner{
		subscriptions: subscriptions,
		groups:        groups,
		plans:         planGetter,
		now:           time.Now,
	}
}

// AssignSubscriptionAfterPayment is the legacy entry point that fulfils the
// subscription grant outside the caller's transaction. It is retained for
// test paths that exercise the assigner with an in-memory fake. Production
// callers should use AssignSubscriptionAfterPaymentInTx so the grant and the
// payment order status transition commit atomically (code-review
// 2026-07-30 billing-H2).
func (a *paymentSubscriptionAssigner) AssignSubscriptionAfterPayment(ctx context.Context, order *PaymentOrder) error {
	return a.assignSubscriptionAfterPayment(ctx, nil, order)
}

// AssignSubscriptionAfterPaymentInTx fulfils the subscription grant inside
// the caller's transaction. The subscription assign/extend and the payment
// order status transition therefore commit or roll back atomically, so a
// failure after the grant (e.g. the payment_orders Updates) cannot leave the
// wallet/subscription credited while the order stays pending (which a
// replayed payment callback would re-credit). When tx is nil the function
// falls back to the legacy non-tx path (used by in-memory tests).
func (a *paymentSubscriptionAssigner) AssignSubscriptionAfterPaymentInTx(ctx context.Context, tx subscriptionbiz.Tx, order *PaymentOrder) error {
	return a.assignSubscriptionAfterPayment(ctx, tx, order)
}

func (a *paymentSubscriptionAssigner) assignSubscriptionAfterPayment(ctx context.Context, tx subscriptionbiz.Tx, order *PaymentOrder) error {
	if a == nil || a.subscriptions == nil {
		return errors.New("subscription assigner is not configured")
	}
	if order == nil {
		return errors.New("payment order is required")
	}
	if _, err := strconv.ParseInt(order.UserID, 10, 64); err != nil || order.UserID == "" {
		return fmt.Errorf("invalid payment order user_id %q", order.UserID)
	}
	// Plan-backed orders (including snapshot-fulfilled ones) carry their own
	// group_id in the snapshot/plan, so the order-level GroupID may be 0. Only
	// the group-only path requires a populated order.GroupID and a configured
	// group getter.
	if order.PlanID > 0 {
		return a.assignPlan(ctx, tx, order)
	}
	if a.groups == nil {
		return errors.New("subscription assigner is not configured")
	}
	if order.GroupID <= 0 {
		return errors.New("subscription group_id is required")
	}
	return a.assignGroup(ctx, tx, order)
}

func (a *paymentSubscriptionAssigner) assignGroup(ctx context.Context, tx subscriptionbiz.Tx, order *PaymentOrder) error {
	userID, err := strconv.ParseInt(order.UserID, 10, 64)
	if err != nil || userID <= 0 {
		return fmt.Errorf("invalid payment order user_id %q", order.UserID)
	}
	group, err := a.groups.GetGroupByID(ctx, order.GroupID)
	if err != nil {
		return err
	}
	if group.DurationDays <= 0 {
		return fmt.Errorf("subscription group %d duration_days must be positive", order.GroupID)
	}
	name := group.DisplayName
	if name == "" {
		name = group.Name
	}
	now := a.now().Unix()
	metadata, _ := jsonx.Marshal(map[string]string{
		"payment_trade_no":  order.TradeNo,
		"provider_trade_no": order.ProviderTradeNo,
	})
	req := &subscriptionbiz.AssignSubscriptionRequest{
		UserID:           userID,
		GroupID:          order.GroupID,
		SubscriptionName: name,
		StartsAt:         now,
		ExpiresAt:        now + int64(group.DurationDays)*subscriptionSecondsPerDay,
		Metadata:         string(metadata),
	}
	var sub *subscriptionbiz.UserSubscription
	var err2 error
	if tx != nil {
		sub, _, err2 = a.subscriptions.AssignOrExtendInTx(ctx, tx, req)
	} else {
		sub, _, err2 = a.subscriptions.AssignOrExtend(ctx, req)
	}
	if err2 == nil && sub != nil {
		// Stamp the granted subscription id onto the order so refunds can
		// resolve the exact subscription deterministically (phase 2.3
		// traceability) instead of falling back to the user's current
		// active subscription.
		order.SubscriptionID = sub.ID
	}
	return err2
}

func (a *paymentSubscriptionAssigner) assignPlan(ctx context.Context, tx subscriptionbiz.Tx, order *PaymentOrder) error {
	if order == nil {
		return errors.New("payment order is required")
	}
	// Phase 2: fulfil from the immutable plan snapshot captured at order
	// creation. This decouples payment completion from later on/off-shelf or
	// price/validity edits to the live plan row. When a snapshot exists the
	// assigner never re-reads the plan repo for fulfilment attributes.
	if snap, snapErr := DecodePlanSnapshot(order.PlanSnapshot); snapErr != nil {
		return fmt.Errorf("decode plan snapshot: %w", snapErr)
	} else if snap.PlanID > 0 {
		return a.assignFromSnapshot(ctx, tx, order, snap)
	}

	if a.plans == nil {
		return errors.New("subscription plan assigner is not configured")
	}
	userID, err := strconv.ParseInt(order.UserID, 10, 64)
	if err != nil || userID <= 0 {
		return fmt.Errorf("invalid payment order user_id %q", order.UserID)
	}
	plan, err := a.plans.GetPlanByID(ctx, order.PlanID)
	if err != nil {
		return err
	}
	if plan == nil || plan.GroupID <= 0 {
		return errors.New("subscription plan is invalid")
	}
	durationDays := order.AssetAmount
	if durationDays <= 0 {
		durationDays = int64(plan.ValidityDays)
	}
	if durationDays <= 0 {
		return errors.New("subscription plan duration must be positive")
	}
	name := plan.Name
	if name == "" {
		name = plan.ProductName
	}
	if name == "" && plan.Group != nil {
		name = plan.Group.DisplayName
	}
	now := a.now().Unix()
	expiresAt := now + durationDays*subscriptionSecondsPerDay
	metadata, _ := jsonx.Marshal(map[string]string{
		"payment_trade_no":  order.TradeNo,
		"provider_trade_no": order.ProviderTradeNo,
		"plan_id":           strconv.FormatInt(plan.ID, 10),
		"plan_name":         plan.Name,
	})
	req := &subscriptionbiz.AssignSubscriptionRequest{
		UserID:           userID,
		GroupID:          plan.GroupID,
		SubscriptionName: name,
		StartsAt:         now,
		ExpiresAt:        expiresAt,
		Metadata:         string(metadata),
	}
	var sub *subscriptionbiz.UserSubscription
	var assignErr error
	if tx != nil {
		sub, _, assignErr = a.subscriptions.AssignOrExtendInTx(ctx, tx, req)
	} else {
		sub, _, assignErr = a.subscriptions.AssignOrExtend(ctx, req)
	}
	if assignErr == nil && sub != nil {
		// Stamp the granted subscription id onto the order (phase 2.3).
		order.SubscriptionID = sub.ID
	}
	return assignErr
}

// assignFromSnapshot issues the subscription using only the frozen plan view
// stored on the payment order. The live plan row is not consulted, so taking
// the plan off-shelf after order creation cannot strand an already-paid order.
func (a *paymentSubscriptionAssigner) assignFromSnapshot(ctx context.Context, tx subscriptionbiz.Tx, order *PaymentOrder, snap PlanSnapshot) error {
	userID, err := strconv.ParseInt(order.UserID, 10, 64)
	if err != nil || userID <= 0 {
		return fmt.Errorf("invalid payment order user_id %q", order.UserID)
	}
	if snap.GroupID <= 0 {
		return errors.New("plan snapshot group_id is required")
	}
	durationDays := order.AssetAmount
	if durationDays <= 0 {
		durationDays = int64(snap.ValidityDays)
	}
	if durationDays <= 0 {
		return errors.New("subscription plan duration must be positive")
	}
	name := snap.Name
	if name == "" {
		name = snap.ProductName
	}
	if name == "" {
		name = fmt.Sprintf("plan-%d", snap.PlanID)
	}
	now := a.now().Unix()
	expiresAt := now + durationDays*subscriptionSecondsPerDay
	metadata, _ := jsonx.Marshal(map[string]string{
		"payment_trade_no":  order.TradeNo,
		"provider_trade_no": order.ProviderTradeNo,
		"plan_id":           strconv.FormatInt(snap.PlanID, 10),
		"plan_name":         snap.Name,
		"plan_snapshot":     "true",
	})
	req := &subscriptionbiz.AssignSubscriptionRequest{
		UserID:           userID,
		GroupID:          snap.GroupID,
		SubscriptionName: name,
		StartsAt:         now,
		ExpiresAt:        expiresAt,
		Metadata:         string(metadata),
	}
	var sub *subscriptionbiz.UserSubscription
	var assignErr error
	if tx != nil {
		sub, _, assignErr = a.subscriptions.AssignOrExtendInTx(ctx, tx, req)
	} else {
		sub, _, assignErr = a.subscriptions.AssignOrExtend(ctx, req)
	}
	if assignErr == nil && sub != nil {
		// Stamp the granted subscription id onto the order (phase 2.3).
		order.SubscriptionID = sub.ID
	}
	return assignErr
}
