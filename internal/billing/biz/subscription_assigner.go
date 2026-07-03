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
}

type SubscriptionGroupGetter interface {
	GetGroupByID(ctx context.Context, groupID int64) (*subscriptionbiz.SubscriptionGroup, error)
}

type paymentSubscriptionAssigner struct {
	subscriptions SubscriptionAssignmentUsecase
	groups        SubscriptionGroupGetter
	now           func() time.Time
}

func NewPaymentSubscriptionAssigner(subscriptions SubscriptionAssignmentUsecase, groups SubscriptionGroupGetter) SubscriptionAssigner {
	return &paymentSubscriptionAssigner{
		subscriptions: subscriptions,
		groups:        groups,
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
	_, err = a.subscriptions.Assign(ctx, &subscriptionbiz.AssignSubscriptionRequest{
		UserID:           userID,
		GroupID:          order.GroupID,
		SubscriptionName: name,
		StartsAt:         now,
		ExpiresAt:        now + int64(group.DurationDays)*subscriptionSecondsPerDay,
		Metadata:         string(metadata),
	})
	return err
}
