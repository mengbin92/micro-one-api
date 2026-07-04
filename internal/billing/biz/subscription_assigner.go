package biz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	subscriptionbiz "micro-one-api/internal/subscription/biz"
)

const subscriptionSecondsPerDay = 24 * 60 * 60

type SubscriptionAssignmentUsecase interface {
	Assign(ctx context.Context, req *subscriptionbiz.AssignSubscriptionRequest) (*subscriptionbiz.UserSubscription, error)
	AssignOrExtend(ctx context.Context, req *subscriptionbiz.AssignSubscriptionRequest) (*subscriptionbiz.UserSubscription, bool, error)
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

func (a *paymentSubscriptionAssigner) AssignSubscriptionAfterPayment(ctx context.Context, order *PaymentOrder) error {
	if a == nil || a.subscriptions == nil || a.groups == nil {
		return errors.New("subscription assigner is not configured")
	}
	if order == nil {
		return errors.New("payment order is required")
	}
	userID, err := strconv.ParseInt(order.UserID, 10, 64)
	if err != nil || userID <= 0 {
		return fmt.Errorf("invalid payment order user_id %q", order.UserID)
	}
	if order.GroupID <= 0 {
		return errors.New("subscription group_id is required")
	}
	if order.PlanID > 0 {
		return a.assignPlan(ctx, order)
	}
	return a.assignGroup(ctx, order)
}

func (a *paymentSubscriptionAssigner) assignGroup(ctx context.Context, order *PaymentOrder) error {
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
	metadata, _ := json.Marshal(map[string]string{
		"payment_trade_no":  order.TradeNo,
		"provider_trade_no": order.ProviderTradeNo,
	})
	_, _, err = a.subscriptions.AssignOrExtend(ctx, &subscriptionbiz.AssignSubscriptionRequest{
		UserID:           userID,
		GroupID:          order.GroupID,
		SubscriptionName: name,
		StartsAt:         now,
		ExpiresAt:        now + int64(group.DurationDays)*subscriptionSecondsPerDay,
		Metadata:         string(metadata),
	})
	return err
}

func (a *paymentSubscriptionAssigner) assignPlan(ctx context.Context, order *PaymentOrder) error {
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
	metadata, _ := json.Marshal(map[string]string{
		"payment_trade_no":  order.TradeNo,
		"provider_trade_no": order.ProviderTradeNo,
		"plan_id":           strconv.FormatInt(plan.ID, 10),
		"plan_name":         plan.Name,
	})
	_, _, err = a.subscriptions.AssignOrExtend(ctx, &subscriptionbiz.AssignSubscriptionRequest{
		UserID:           userID,
		GroupID:          plan.GroupID,
		SubscriptionName: name,
		StartsAt:         now,
		ExpiresAt:        expiresAt,
		Metadata:         string(metadata),
	})
	return err
}
