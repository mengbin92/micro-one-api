package data

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/domain/routing"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
	"micro-one-api/platform/database/testutil"
)

// TestUserPriceOverrideRepo covers the 097 heads+history contract across all
// three dialects: replace semantics (never multiply), monotone versions that
// resume from MAX(history) after a Clear, idempotent clear, and per-(user,
// group) independence.
func TestUserPriceOverrideRepo(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := testutil.RoutingContextDB(t, dialect)
			repo := NewUserPriceOverrideRepo(&Data{db: db})
			ctx := context.Background()

			// No override yet.
			got, err := repo.Get(ctx, 7001, 9)
			require.NoError(t, err)
			require.Nil(t, got)

			// First set is version 1.
			v, err := repo.Set(ctx, 7001, 9, 0.9)
			require.NoError(t, err)
			require.EqualValues(t, 1, v)
			got, err = repo.Get(ctx, 7001, 9)
			require.NoError(t, err)
			require.EqualValues(t, 1, got.Version)
			require.InDelta(t, 0.9, got.PriceRatio, 1e-12)

			// Re-set replaces and bumps the version.
			v, err = repo.Set(ctx, 7001, 9, 1.4)
			require.NoError(t, err)
			require.EqualValues(t, 2, v)
			got, err = repo.Get(ctx, 7001, 9)
			require.NoError(t, err)
			require.InDelta(t, 1.4, got.PriceRatio, 1e-12)

			// Another user / group is independent.
			v, err = repo.Set(ctx, 7002, 9, 2.0)
			require.NoError(t, err)
			require.EqualValues(t, 1, v)
			v, err = repo.Set(ctx, 7001, 10, 3.0)
			require.NoError(t, err)
			require.EqualValues(t, 1, v)

			// Clear drops the live head, keeps history, and is idempotent.
			require.NoError(t, repo.Clear(ctx, 7001, 9))
			require.NoError(t, repo.Clear(ctx, 7001, 9))
			got, err = repo.Get(ctx, 7001, 9)
			require.NoError(t, err)
			require.Nil(t, got)
			var history int64
			require.NoError(t, db.Table("user_routing_price_overrides").Where("user_id = ? AND routing_group_id = ?", 7001, 9).Count(&history).Error)
			require.EqualValues(t, 2, history, "history rows survive a clear")

			// Re-set after clear resumes from MAX(history)+1.
			v, err = repo.Set(ctx, 7001, 9, 0.7)
			require.NoError(t, err)
			require.EqualValues(t, 3, v)
		})
	}
}

// TestRequestSnapshotUserPriceOverride verifies the Phase F resolution order
// (base GroupRatio -> published policy -> user override REPLACES the ratio,
// billing mode stays with the policy), the frozen in-flight snapshot, and the
// quote path across all three dialects.
func TestRequestSnapshotUserPriceOverride(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			t.Setenv("BILLING_REQUEST_SNAPSHOT_V2", "true")
			t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "true")
			db := testutil.RoutingContextDB(t, dialect)
			uc, _, d, _, _ := snapshotBillingFixture(t, db, false, 1, 1)
			uc.SetRoutingPolicyRepo(NewRoutingPolicyRepo(d))
			uc.SetUserPriceOverrideRepo(NewUserPriceOverrideRepo(d))
			ctx := context.Background()

			// Published policy: ratio 0.5, subscription_first (base is also 0.5).
			require.NoError(t, uc.PublishRoutingBillingPolicy(ctx, &routing.BillingPolicy{GroupID: 9, BillingMode: routing.SubscriptionFirst, PriceRatio: 0.5}, 0))

			// No override yet: policy ratio applies.
			r, err := uc.ReserveQuota(ctx, "7001", "base-request", 1000, "m", "1", 0, requestContext())
			require.NoError(t, err)
			require.InDelta(t, 0.5, r.RequestSnapshot.Pricing.GroupRatio, 1e-12)
			require.Equal(t, "routing_policy:9:1", r.RequestSnapshot.BillingPolicyVersion)

			// User override 0.9 REPLACES the policy ratio (not 0.5*0.9).
			version, err := uc.SetUserRoutingPrice(ctx, 7001, 9, 0.9)
			require.NoError(t, err)
			require.EqualValues(t, 1, version)
			r, err = uc.ReserveQuota(ctx, "7001", "override-request", 1000, "m", "1", 0, requestContext())
			require.NoError(t, err)
			require.InDelta(t, 0.9, r.RequestSnapshot.Pricing.GroupRatio, 1e-12, "override replaces the ratio")
			require.Equal(t, routing.SubscriptionFirst, r.RequestSnapshot.BillingMode, "billing mode still comes from the policy")
			require.Equal(t, "user_routing_price:9:7001:1", r.RequestSnapshot.BillingPolicyVersion)

			// The in-flight reservation keeps its frozen snapshot: a replay of
			// the same request id + routing context stays idempotent even after
			// the override changes.
			_, err = uc.SetUserRoutingPrice(ctx, 7001, 9, 1.4)
			require.NoError(t, err)
			replay, err := uc.ReserveQuota(ctx, "7001", "override-request", 1000, "m", "1", 0, requestContext())
			require.NoError(t, err, "replay of the frozen attempt must not conflict")
			require.InDelta(t, 0.9, replay.RequestSnapshot.Pricing.GroupRatio, 1e-12, "in-flight snapshot stays frozen")

			// A new attempt sees the new override version.
			r, err = uc.ReserveQuota(ctx, "7001", "override-v2", 1000, "m", "1", 0, requestContext())
			require.NoError(t, err)
			require.InDelta(t, 1.4, r.RequestSnapshot.Pricing.GroupRatio, 1e-12)
			require.Equal(t, "user_routing_price:9:7001:2", r.RequestSnapshot.BillingPolicyVersion)

			// Clearing the override restores the policy ratio.
			require.NoError(t, uc.ClearUserRoutingPrice(ctx, 7001, 9))
			r, err = uc.ReserveQuota(ctx, "7001", "cleared-request", 1000, "m", "1", 0, requestContext())
			require.NoError(t, err)
			require.InDelta(t, 0.5, r.RequestSnapshot.Pricing.GroupRatio, 1e-12)
			require.Equal(t, "routing_policy:9:1", r.RequestSnapshot.BillingPolicyVersion)

			// Quote path exposes the same override with its audit source.
			_, err = uc.SetUserRoutingPrice(ctx, 7001, 9, 1.1)
			require.NoError(t, err)
			quote, err := uc.RoutingGroupPrice(ctx, 9, 7001)
			require.NoError(t, err)
			require.InDelta(t, 1.1, quote.PriceRatio, 1e-12)
			require.Equal(t, "user_routing_price_override", quote.Source)
			require.Equal(t, "user_routing_price:9:7001:3", quote.Version)
			require.Equal(t, routing.SubscriptionFirst, quote.BillingMode)

			// Validation guards.
			_, err = uc.SetUserRoutingPrice(ctx, 7001, 9, 0)
			require.ErrorIs(t, err, biz.ErrRoutingContextInvalid)
			_, err = uc.SetUserRoutingPrice(ctx, 7001, 9, -1)
			require.ErrorIs(t, err, biz.ErrRoutingContextInvalid)
		})
	}
}

// TestCheckRoutingSettlement covers the ordered-routing settlement
// qualification probe across all three dialects and the three billing modes.
// All billing-mode publishes happen before any subscription contract exists:
// mode changes are contract-protected once a contract references the group.
func TestCheckRoutingSettlement(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			t.Setenv("BILLING_REQUEST_SNAPSHOT_V2", "true")
			t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "true")
			db := testutil.RoutingContextDB(t, dialect)
			uc, _, d, repo, _ := snapshotBillingFixture(t, db, false, 1, 1)
			uc.SetRoutingPolicyRepo(NewRoutingPolicyRepo(d))
			ctx := context.Background()

			// Publish the three settlement modes up front (no contracts yet).
			require.NoError(t, uc.PublishRoutingBillingPolicy(ctx, &routing.BillingPolicy{GroupID: 9, BillingMode: routing.SubscriptionFirst, PriceRatio: 0.5}, 0))
			require.NoError(t, uc.PublishRoutingBillingPolicy(ctx, &routing.BillingPolicy{GroupID: 10, BillingMode: routing.WalletOnly, PriceRatio: 0.5}, 0))
			require.NoError(t, uc.PublishRoutingBillingPolicy(ctx, &routing.BillingPolicy{GroupID: 11, BillingMode: routing.SubscriptionOnly, PriceRatio: 0.5}, 0))

			// subscription_first: a positive balance suffices without a subscription.
			settlement, err := uc.CheckRoutingSettlement(ctx, 7001, 9)
			require.NoError(t, err)
			require.True(t, settlement.Allowed)

			// wallet_only: balance decides.
			settlement, err = uc.CheckRoutingSettlement(ctx, 7001, 10)
			require.NoError(t, err)
			require.True(t, settlement.Allowed)
			require.NoError(t, db.Table("users").Where("id = ?", 7001).Update("balance", 0).Error)
			settlement, err = uc.CheckRoutingSettlement(ctx, 7001, 10)
			require.NoError(t, err)
			require.False(t, settlement.Allowed)
			require.NotEmpty(t, settlement.Reason)
			require.NoError(t, db.Table("users").Where("id = ?", 7001).Update("balance", 1000000).Error)

			// subscription_only without a subscription terminates.
			settlement, err = uc.CheckRoutingSettlement(ctx, 7001, 11)
			require.NoError(t, err)
			require.False(t, settlement.Allowed)

			// A covering subscription with remaining quota allows; exhausted
			// window quota denies; subscription_first then falls back to the
			// wallet.
			limit := 0.5
			group := &subscriptionbiz.SubscriptionGroup{Name: "settle", Status: 1, RateMultiplier: 2, DailyLimitUSD: &limit, WeeklyLimitUSD: &limit, MonthlyLimitUSD: &limit}
			require.NoError(t, repo.CreateGroup(ctx, group))
			now := time.Now().Unix()
			sub := &subscriptionbiz.UserSubscription{UserID: 7001, GroupID: group.ID, Status: subscriptionbiz.SubscriptionStatusActive, StartsAt: now, ExpiresAt: now + 86400, DailyWindowStart: now, WeeklyWindowStart: now, MonthlyWindowStart: now, CreatedAt: now}
			require.NoError(t, repo.CreateSubscription(ctx, sub))
			subs := subscriptionbiz.NewSubscriptionUsecase(repo, repo)
			uc.SetSubscriptionPrimatives(subs)

			settlement, err = uc.CheckRoutingSettlement(ctx, 7001, 11)
			require.NoError(t, err)
			require.True(t, settlement.Allowed)
			require.NoError(t, subs.RecordUsage(ctx, 7001, 0.5))
			settlement, err = uc.CheckRoutingSettlement(ctx, 7001, 11)
			require.NoError(t, err)
			require.False(t, settlement.Allowed, "exhausted quota terminates subscription-only settlement")
			settlement, err = uc.CheckRoutingSettlement(ctx, 7001, 9)
			require.NoError(t, err)
			require.True(t, settlement.Allowed, "subscription_first falls back to the wallet")

			// Bad ids fail closed.
			_, err = uc.CheckRoutingSettlement(ctx, 0, 9)
			require.ErrorIs(t, err, biz.ErrRoutingContextInvalid)
		})
	}
}
