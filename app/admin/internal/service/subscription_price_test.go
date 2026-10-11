package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	billingv1 "micro-one-api/api/billing/v1"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
)

type priceBillingClient struct {
	billingv1.BillingServiceClient
	charged, refunded int64
	changeMultiplier  func()
}

func TestLegacySubscriptionChangeRejectsInvalidPriceBeforeWallet(t *testing.T) {
	t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "false")
	for _, prices := range [][2]int64{{-1, 0}, {math.MaxInt64, -1}, {math.MaxInt64, 0}} {
		group := &subscriptionbiz.SubscriptionGroup{ID: 7, Name: "price-policy", Status: 1}
		groups := &fakeGroupRepo{group: group}
		repo := &fakeSubscriptionRepo{active: &subscriptionbiz.UserSubscription{ID: 3, UserID: 42, GroupID: group.ID, Status: "active"}}
		billing := &priceBillingClient{}
		svc := NewAdminService(billing, nil, nil, nil)
		svc.SetSubscriptionUsecases(subscriptionbiz.NewSubscriptionUsecase(repo, groups), subscriptionbiz.NewGroupUsecase(groups))
		_, err := svc.ChangeSubscription(context.Background(), subscriptionbiz.ChangeRequest{UserID: 42, FromSubscriptionID: 3, ToGroupID: group.ID, NewPriceQuota: prices[0], OldPriceQuota: prices[1], Policy: subscriptionbiz.SubscriptionChangePolicyImmediate}, "invalid-price")
		require.Error(t, err, "negative prices and conversion overflow must not become a free immediate change")
		require.Zero(t, billing.charged)
		require.Zero(t, repo.updatedCount)
	}
}

func (c *priceBillingClient) PurchaseSubscription(_ context.Context, req *billingv1.PurchaseSubscriptionRequest, _ ...grpc.CallOption) (*billingv1.PurchaseSubscriptionResponse, error) {
	c.charged = req.PriceAmount
	if c.changeMultiplier != nil {
		c.changeMultiplier()
	}
	return &billingv1.PurchaseSubscriptionResponse{Success: true}, nil
}

func (c *priceBillingClient) TopUpQuota(_ context.Context, req *billingv1.TopUpQuotaRequest, _ ...grpc.CallOption) (*billingv1.TopUpQuotaResponse, error) {
	c.refunded = req.Amount
	return &billingv1.TopUpQuotaResponse{Success: true}, nil
}

type priceFailingSubscriptionRepo struct{ fakeSubscriptionRepo }

func (*priceFailingSubscriptionRepo) CreateSubscription(context.Context, *subscriptionbiz.UserSubscription) error {
	return errors.New("entitlement insert failed")
}

func (*priceFailingSubscriptionRepo) UpdateSubscriptionFields(context.Context, *subscriptionbiz.UserSubscription, []subscriptionbiz.SubscriptionField) error {
	return errors.New("entitlement update failed")
}

type pricePlanRepo struct {
	subscriptionbiz.PlanRepository
	plan *subscriptionbiz.SubscriptionPlan
}

func (r pricePlanRepo) GetPlanByID(context.Context, int64) (*subscriptionbiz.SubscriptionPlan, error) {
	return r.plan, nil
}

func TestLegacySubscriptionPriceRollbackUsesActualDebit(t *testing.T) {
	t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "false")
	for _, kind := range []string{"group", "plan", "upgrade"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv("RECHARGE_AMOUNT_MULTIPLIER", "10")
			group := &subscriptionbiz.SubscriptionGroup{ID: 7, Name: "price-policy", Status: 1, PriceQuota: 100, DurationDays: 30}
			groups := &fakeGroupRepo{group: group}
			repo := &priceFailingSubscriptionRepo{}
			billing := &priceBillingClient{changeMultiplier: func() { t.Setenv("RECHARGE_AMOUNT_MULTIPLIER", "2") }}
			svc := NewAdminService(billing, nil, nil, nil)
			plan := &subscriptionbiz.SubscriptionPlan{ID: 8, GroupID: group.ID, Group: group, Name: "price-plan", PriceQuota: 100, ValidityDays: 30, ForSale: true}
			svc.SetSubscriptionUsecases(subscriptionbiz.NewSubscriptionUsecase(repo, groups), subscriptionbiz.NewGroupUsecase(groups), subscriptionbiz.NewPlanUsecase(pricePlanRepo{plan: plan}, groups))
			var err error
			switch kind {
			case "group":
				_, err = svc.PurchaseSubscription(context.Background(), 42, group.ID, "price-rollback-group")
			case "plan":
				_, err = svc.PurchaseSubscriptionPlan(context.Background(), 42, plan.ID, "price-rollback-plan")
			case "upgrade":
				repo.active = &subscriptionbiz.UserSubscription{ID: 3, UserID: 42, GroupID: group.ID, Status: "active", StartsAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Hour).Unix()}
				_, err = svc.ChangeSubscription(context.Background(), subscriptionbiz.ChangeRequest{UserID: 42, FromSubscriptionID: 3, ToGroupID: group.ID, NewPriceQuota: 200, OldPriceQuota: 100}, "price-rollback-upgrade")
			}
			require.Error(t, err)
			require.EqualValues(t, 10_000_000, billing.charged)
			require.Equal(t, billing.charged, billing.refunded, "rollback must restore the debit, even when the configured rate changes")
		})
	}
}

func TestNewFrozenPaymentCompletionUsesNominalPriceAndValidity(t *testing.T) {
	t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "false")
	t.Setenv("RECHARGE_AMOUNT_MULTIPLIER", "10")
	for _, planID := range []int64{0, 8} {
		t.Run(fmt.Sprint(planID), func(t *testing.T) {
			group := &subscriptionbiz.SubscriptionGroup{ID: 7, Name: "edited group", Status: 1, PriceQuota: 200, DurationDays: 1}
			groups := &fakeGroupRepo{group: group}
			repo := &fakeSubscriptionRepo{}
			svc := NewAdminService(nil, nil, nil, nil)
			svc.SetSubscriptionUsecases(subscriptionbiz.NewSubscriptionUsecase(repo, groups), subscriptionbiz.NewGroupUsecase(groups))
			conversion, err := subscriptionbiz.CapturePriceConversion(100)
			require.NoError(t, err)
			snap := subscriptionbiz.PlanSnapshot{PlanID: planID, GroupID: group.ID, Name: "frozen purchase", PriceQuota: 100, ValidityDays: 30, PriceConversion: conversion}
			order := &billingv1.PaymentOrder{PlanId: planID, GroupId: group.ID, AssetAmount: 1, PlanSnapshot: subscriptionbiz.EncodePlanSnapshot(snap)}
			sub, err := svc.fulfilPaidOrder(context.Background(), 42, order)
			require.NoError(t, err)
			require.EqualValues(t, 100, sub.PricePaid)
			require.EqualValues(t, 30*secondsPerDay, sub.ExpiresAt-sub.StartsAt)
			require.Equal(t, "frozen purchase", sub.SubscriptionName)
			order.GroupId++
			_, err = svc.fulfilPaidOrder(context.Background(), 42, order)
			require.Error(t, err, "a frozen purchase cannot grant another group's entitlement")
			require.Equal(t, 0, repo.updatedCount)
		})
	}
}
