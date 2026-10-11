package data

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/billing/internal/biz"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
	"micro-one-api/platform/database/testutil"
)

func TestSubscriptionCommerceCNYWalletPrice(t *testing.T) {
	t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "true")
	t.Setenv("RECHARGE_AMOUNT_MULTIPLIER", "10")
	db := testutil.RoutingContextDB(t, "sqlite")
	billing, _, d, repo, _ := snapshotBillingFixture(t, db, false, 1, 1)
	ctx := context.Background()
	require.NoError(t, db.Table("users").Where("id = ?", 7001).Update("balance", 40_000_000).Error)
	group := &subscriptionbiz.SubscriptionGroup{Name: "CNY price", Status: 1, RateMultiplier: 1}
	require.NoError(t, repo.CreateGroup(ctx, group))
	contract, err := subscriptionbiz.NewContract(group, []subscriptionbiz.RoutingCoverage{{GroupID: 9, GroupKey: "default", GrantsAccess: true}})
	require.NoError(t, err)
	plan := &subscriptionbiz.SubscriptionPlan{GroupID: group.ID, Name: "CNY 100", PriceQuota: 100, ValidityDays: 30, ForSale: true, Contract: contract}
	require.NoError(t, repo.CreatePlan(ctx, plan))
	commerce := biz.NewSubscriptionCommerce(billing, subscriptionbiz.NewSubscriptionUsecase(repo, repo), repo, NewSubscriptionCommerceRepo(d))
	req := biz.SubscriptionCommerceRequest{UserID: 7001, PlanID: plan.ID, RequestID: "CNY-purchase"}
	first, err := commerce.Execute(ctx, req)
	require.NoError(t, err)
	require.EqualValues(t, 30_000_000, first.Balance, "CNY 100 costs USD 1000 at the recharge multiplier 10")
	require.EqualValues(t, 100, first.Subscription.PricePaid, "the upgrade baseline remains nominal CNY")
	t.Setenv("RECHARGE_AMOUNT_MULTIPLIER", "20")
	replay, err := commerce.Execute(ctx, req)
	require.NoError(t, err)
	require.Equal(t, first, replay, "a completed purchase cannot be repriced on retry")
	t.Setenv("RECHARGE_AMOUNT_MULTIPLIER", "10")
	req.RequestID = "CNY-renew"
	renewed, err := commerce.Execute(ctx, req)
	require.NoError(t, err)
	require.EqualValues(t, 20_000_000, renewed.Balance)
	require.Equal(t, first.Subscription.ExpiresAt+30*86400, renewed.Subscription.ExpiresAt)
	plan.PriceQuota = 150
	require.NoError(t, repo.UpdatePlan(ctx, plan))
	req.RequestID = "CNY-upgrade"
	req.FromSubscriptionID = first.Subscription.ID
	req.ChangePolicy = "immediate"
	upgraded, err := commerce.Execute(ctx, req)
	require.NoError(t, err)
	require.EqualValues(t, 15_000_000, upgraded.Balance, "only the CNY 50 difference is converted")
	require.EqualValues(t, 5_000_000, upgraded.Change.ChargedQuota)
	require.EqualValues(t, 150, upgraded.Subscription.PricePaid)
	require.Equal(t, renewed.Subscription.ExpiresAt, upgraded.Subscription.ExpiresAt)
	var amounts []int64
	require.NoError(t, db.Table("billing_ledgers").Order("id").Pluck("amount", &amounts).Error)
	require.Equal(t, []int64{-10_000_000, -10_000_000, -5_000_000}, amounts)
}

func TestSubscriptionCommerceHistoricalNominalCredit(t *testing.T) {
	t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "true")
	t.Setenv("RECHARGE_AMOUNT_MULTIPLIER", "10")
	db := testutil.RoutingContextDB(t, "sqlite")
	billing, _, d, repo, _ := snapshotBillingFixture(t, db, false, 1, 1)
	ctx := context.Background()
	require.NoError(t, db.Table("users").Where("id = ?", 7001).Update("balance", 20_000_000).Error)
	group := &subscriptionbiz.SubscriptionGroup{Name: "historical CNY", Status: 1, RateMultiplier: 1}
	require.NoError(t, repo.CreateGroup(ctx, group))
	contract, err := subscriptionbiz.NewContract(group, []subscriptionbiz.RoutingCoverage{{GroupID: 9, GroupKey: "default", GrantsAccess: true}})
	require.NoError(t, err)
	now := time.Now().Unix()
	old := &subscriptionbiz.UserSubscription{UserID: 7001, GroupID: group.ID, SubscriptionName: "paid before correction", Contract: contract, PricePaid: 100, Status: subscriptionbiz.SubscriptionStatusActive, StartsAt: now, ExpiresAt: now + 30*86400, DailyWindowStart: now, WeeklyWindowStart: now, MonthlyWindowStart: now}
	require.NoError(t, repo.CreateSubscription(ctx, old))
	plan := &subscriptionbiz.SubscriptionPlan{GroupID: group.ID, Name: "CNY 150", PriceQuota: 150, ValidityDays: 30, ForSale: true, Contract: contract}
	require.NoError(t, repo.CreatePlan(ctx, plan))
	commerce := biz.NewSubscriptionCommerce(billing, subscriptionbiz.NewSubscriptionUsecase(repo, repo), repo, NewSubscriptionCommerceRepo(d))
	result, err := commerce.Execute(ctx, biz.SubscriptionCommerceRequest{UserID: 7001, PlanID: plan.ID, FromSubscriptionID: old.ID, ChangePolicy: "immediate", RequestID: "historical-upgrade"})
	require.NoError(t, err)
	require.EqualValues(t, 15_000_000, result.Balance)
	require.EqualValues(t, 5_000_000, result.Change.ChargedQuota, "historical nominal CNY 100 receives its original upgrade credit")
	require.Equal(t, old.ExpiresAt, result.Subscription.ExpiresAt)
}

func TestSubscriptionPaymentFrozenCNYPrice(t *testing.T) {
	t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "true")
	t.Setenv("BILLING_REQUEST_SNAPSHOT_V2", "true")
	t.Setenv("RECHARGE_AMOUNT_MULTIPLIER", "10")
	db := testutil.RoutingContextDB(t, "sqlite")
	billing, _, d, repo, _ := snapshotBillingFixture(t, db, false, 1, 1)
	billing.SetRoutingPolicyRepo(NewRoutingPolicyRepo(d))
	ctx := context.Background()
	group := &subscriptionbiz.SubscriptionGroup{Name: "frozen CNY", Status: 1, RateMultiplier: 1}
	require.NoError(t, repo.CreateGroup(ctx, group))
	contract, err := subscriptionbiz.NewContract(group, []subscriptionbiz.RoutingCoverage{{GroupID: 9, GroupKey: "default", GrantsAccess: true}})
	require.NoError(t, err)
	plan := &subscriptionbiz.SubscriptionPlan{GroupID: group.ID, Name: "CNY 100", PriceQuota: 100, ValidityDays: 30, ForSale: true, Contract: contract}
	require.NoError(t, repo.CreatePlan(ctx, plan))
	subs := subscriptionbiz.NewSubscriptionUsecase(repo, repo)
	payments := biz.NewPaymentUsecaseWithAssignerAndSnapshotter(NewPaymentRepo(d), biz.NewMockPaymentProvider(), nil, biz.NewPaymentSubscriptionAssigner(subs, repo, repo), biz.NewPaymentPlanSnapshotter(repo, billing))
	payments.SetSubscriptionPurchaseValidator(subs)
	req := biz.CreatePaymentOrderRequest{UserID: "7001", RequestID: "frozen-CNY-payment", PlanID: plan.ID, Channel: "mock", AssetType: "subscription", AssetAmount: 1, MoneyCents: 1, Currency: "CNY"}
	order, err := payments.CreateOrder(ctx, req)
	require.NoError(t, err)
	require.EqualValues(t, 10000, order.MoneyCents)
	snapshot, err := subscriptionbiz.DecodePlanSnapshot(order.PlanSnapshot)
	require.NoError(t, err)
	require.Equal(t, &subscriptionbiz.PriceConversion{Version: 1, RechargeMultiplier: 10, WalletAmount: 10_000_000}, snapshot.PriceConversion)
	t.Setenv("RECHARGE_AMOUNT_MULTIPLIER", "20")
	plan.PriceQuota = 150
	require.NoError(t, repo.UpdatePlan(ctx, plan))
	replay, err := payments.CreateOrder(ctx, req)
	require.NoError(t, err)
	require.Equal(t, order.PlanSnapshot, replay.PlanSnapshot)
	require.Equal(t, order.MoneyCents, replay.MoneyCents)
	wrongCurrency := req
	wrongCurrency.Currency = "USD"
	_, err = payments.CreateOrder(ctx, wrongCurrency)
	require.ErrorIs(t, err, biz.ErrRoutingContextInvalid)
	paid, err := payments.MarkOrderPaid(ctx, order.TradeNo, "frozen-provider")
	require.NoError(t, err)
	sub, err := repo.GetSubscriptionByID(ctx, paid.SubscriptionID)
	require.NoError(t, err)
	require.EqualValues(t, 100, sub.PricePaid, "cash fulfillment preserves the purchase-time nominal CNY upgrade credit")
}

func TestSubscriptionCommerceLegacyUpgradeCreditAdvances(t *testing.T) {
	t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "true")
	t.Setenv("RECHARGE_AMOUNT_MULTIPLIER", "10")
	db := testutil.RoutingContextDB(t, "sqlite")
	billing, _, d, repo, _ := snapshotBillingFixture(t, db, false, 1, 1)
	ctx := context.Background()
	require.NoError(t, db.Table("users").Where("id = ?", 7001).Update("balance", 40_000_000).Error)
	group := &subscriptionbiz.SubscriptionGroup{Name: "legacy price baseline", Status: 1, RateMultiplier: 1}
	require.NoError(t, repo.CreateGroup(ctx, group))
	plan := &subscriptionbiz.SubscriptionPlan{GroupID: group.ID, Name: "legacy CNY 100", PriceQuota: 100, ValidityDays: 30, ForSale: true}
	require.NoError(t, repo.CreatePlan(ctx, plan))
	commerce := biz.NewSubscriptionCommerce(billing, subscriptionbiz.NewSubscriptionUsecase(repo, repo), repo, NewSubscriptionCommerceRepo(d))
	req := biz.SubscriptionCommerceRequest{UserID: 7001, PlanID: plan.ID, RequestID: "legacy-CNY-purchase"}
	first, err := commerce.Execute(ctx, req)
	require.NoError(t, err)
	req.FromSubscriptionID = first.Subscription.ID
	req.ChangePolicy = "immediate"
	for i, price := range []int64{150, 200} {
		plan.PriceQuota = price
		require.NoError(t, repo.UpdatePlan(ctx, plan))
		req.RequestID = "legacy-CNY-upgrade-" + string(rune('a'+i))
		upgraded, err := commerce.Execute(ctx, req)
		require.NoError(t, err)
		require.EqualValues(t, price, upgraded.Subscription.PricePaid)
		require.EqualValues(t, 5_000_000, upgraded.Change.ChargedQuota, "each CNY 50 upgrade charges only its difference")
	}
}

func TestSubscriptionCommerceRejectsNegativePriceBaseline(t *testing.T) {
	t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "true")
	t.Setenv("RECHARGE_AMOUNT_MULTIPLIER", "10")
	db := testutil.RoutingContextDB(t, "sqlite")
	billing, _, d, repo, _ := snapshotBillingFixture(t, db, false, 1, 1)
	ctx := context.Background()
	group := &subscriptionbiz.SubscriptionGroup{Name: "invalid historical price", Status: 1, RateMultiplier: 1}
	require.NoError(t, repo.CreateGroup(ctx, group))
	contract, err := subscriptionbiz.NewContract(group, []subscriptionbiz.RoutingCoverage{{GroupID: 9, GroupKey: "default", GrantsAccess: true}})
	require.NoError(t, err)
	now := time.Now().Unix()
	old := &subscriptionbiz.UserSubscription{UserID: 7001, GroupID: group.ID, Contract: contract, PricePaid: -1, Status: subscriptionbiz.SubscriptionStatusActive, StartsAt: now, ExpiresAt: now + 30*86400, DailyWindowStart: now, WeeklyWindowStart: now, MonthlyWindowStart: now}
	require.NoError(t, repo.CreateSubscription(ctx, old))
	plan := &subscriptionbiz.SubscriptionPlan{GroupID: group.ID, Name: "overflowing price", PriceQuota: math.MaxInt64, ValidityDays: 30, ForSale: true, Contract: contract}
	require.NoError(t, repo.CreatePlan(ctx, plan))
	commerce := biz.NewSubscriptionCommerce(billing, subscriptionbiz.NewSubscriptionUsecase(repo, repo), repo, NewSubscriptionCommerceRepo(d))
	_, err = commerce.Execute(ctx, biz.SubscriptionCommerceRequest{UserID: 7001, PlanID: plan.ID, FromSubscriptionID: old.ID, ChangePolicy: "immediate", RequestID: "invalid-baseline-upgrade"})
	require.Error(t, err, "overflowing nominal price differences must not become free upgrades")
	current, err := repo.GetSubscriptionByID(ctx, old.ID)
	require.NoError(t, err)
	require.EqualValues(t, -1, current.PricePaid)
	var count int64
	require.NoError(t, db.Table("subscription_commerce_receipts").Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Table("billing_ledgers").Count(&count).Error)
	require.Zero(t, count)
}

func TestSubscriptionGroupPaymentFreezesCNYPrice(t *testing.T) {
	t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "false")
	t.Setenv("RECHARGE_AMOUNT_MULTIPLIER", "10")
	db := testutil.RoutingContextDB(t, "sqlite")
	_, _, d, repo, _ := snapshotBillingFixture(t, db, false, 1, 1)
	ctx := context.Background()
	group := &subscriptionbiz.SubscriptionGroup{Name: "group CNY 100", Status: 1, PriceQuota: 100, DurationDays: 30, RateMultiplier: 1}
	require.NoError(t, repo.CreateGroup(ctx, group))
	subs := subscriptionbiz.NewSubscriptionUsecase(repo, repo)
	orders := NewPaymentRepo(d)
	payments := biz.NewPaymentUsecaseWithAssigner(orders, biz.NewMockPaymentProvider(), nil, biz.NewPaymentSubscriptionAssigner(subs, repo, repo))
	req := biz.CreatePaymentOrderRequest{UserID: "7001", Channel: "mock", AssetType: "subscription", GroupID: group.ID, AssetAmount: 30, MoneyCents: 10000, Currency: "CNY"}
	order, err := payments.CreateOrder(ctx, req)
	require.NoError(t, err)
	snapshot, err := subscriptionbiz.DecodePlanSnapshot(order.PlanSnapshot)
	require.NoError(t, err)
	require.Zero(t, snapshot.PlanID)
	require.Equal(t, &subscriptionbiz.PriceConversion{Version: 1, RechargeMultiplier: 10, WalletAmount: 10_000_000}, snapshot.PriceConversion)
	t.Setenv("RECHARGE_AMOUNT_MULTIPLIER", "20")
	group.PriceQuota = 0
	group.DurationDays = 0
	group.Name = "edited group"
	require.NoError(t, repo.UpdateGroup(ctx, group))
	paid, err := payments.MarkOrderPaid(ctx, order.TradeNo, "group-frozen-provider")
	require.NoError(t, err)
	sub, err := repo.GetSubscriptionByID(ctx, paid.SubscriptionID)
	require.NoError(t, err)
	require.InDelta(t, time.Now().Unix()+30*86400, sub.ExpiresAt, 2, "fulfillment and refund must share the frozen purchase duration")
	require.Equal(t, "group CNY 100", sub.SubscriptionName)
	require.EqualValues(t, 100, sub.PricePaid)
	refunds := biz.NewRefundUsecase(orders.(biz.RefundRepo), d.accountRepo, d.ledgerRepo, subs)
	refunded, err := refunds.RefundSubscriptionOrder(ctx, biz.RefundRequest{TradeNo: order.TradeNo, Policy: biz.RefundPolicyRevoke})
	require.NoError(t, err)
	require.EqualValues(t, 10_000_000, refunded.RefundedQuota)
	account, err := d.accountRepo.GetAccountSnapshot(ctx, "7001")
	require.NoError(t, err)
	require.EqualValues(t, 11_000_000, account.Balance)
}
