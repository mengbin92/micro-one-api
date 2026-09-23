package data

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/billing/internal/biz"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
	"micro-one-api/platform/database/testutil"
)

func TestSubscriptionUsageAuthoritativeWindows(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			t.Setenv("BILLING_REQUEST_SNAPSHOT_V2", "true")
			db := testutil.RoutingContextDB(t, dialect)
			uc, _, d, repo, sub := snapshotBillingFixture(t, db, true, 2, 0.5)
			ctx := context.Background()
			p, err := uc.GetSubscriptionUsage(ctx, 7001)
			require.NoError(t, err)
			require.Zero(t, p.DailyUsed.Settled)
			require.Zero(t, *p.DailyUsed.Frozen)
			require.InDelta(t, 0.5, *p.DailyUsed.Available, 1e-9)
			_, err = uc.GetSubscriptionUsage(ctx, 999999)
			require.ErrorIs(t, err, subscriptionbiz.ErrSubscriptionNotFound)
			first, err := uc.ReserveQuota(ctx, "7001", "usage-first", 1000, "m", "1", 0, requestContext())
			require.NoError(t, err)
			group, err := repo.GetGroupByID(ctx, sub.GroupID)
			require.NoError(t, err)
			group.RateMultiplier = 4
			require.NoError(t, repo.UpdateGroup(ctx, group))
			second, err := uc.ReserveQuota(ctx, "7001", "usage-second", 1000, "m", "1", 0, requestContext())
			require.NoError(t, err)
			p, err = uc.GetSubscriptionUsage(ctx, 7001)
			require.NoError(t, err)
			require.Equal(t, "billing", p.UsageSource)
			require.Equal(t, 4.0, p.RateMultiplier)
			require.Zero(t, p.DailyUsed.Settled)
			require.InDelta(t, 0.5, *p.DailyUsed.Frozen, 1e-9)
			require.Zero(t, *p.DailyUsed.Available)
			require.InDelta(t, 0.5, p.DailyUsed.Remaining, 1e-9)
			require.NoError(t, uc.ReleaseQuota(ctx, second.ReservationID, "cancel"))
			p, err = uc.GetSubscriptionUsage(ctx, 7001)
			require.NoError(t, err)
			require.InDelta(t, 0.2, *p.DailyUsed.Frozen, 1e-9)
			require.InDelta(t, 0.3, *p.DailyUsed.Available, 1e-9)
			// A current daily window excludes an in-flight request from yesterday;
			// its weekly/monthly reservation still consumes those original windows.
			now := time.Unix(sub.StartsAt+86400+1, 0)
			reader := biz.NewBillingUsecaseWithOptions(biz.BillingOptions{AccountRepo: d.accountRepo, ReservationRepo: d.reservationRepo, LedgerRepo: d.ledgerRepo, TxRunner: NewTxRunner(d), SubscriptionUsecase: subscriptionbiz.NewSubscriptionUsecase(repo, repo), Now: func() time.Time { return now }})
			p, err = reader.GetSubscriptionUsage(ctx, 7001)
			require.NoError(t, err)
			require.Zero(t, *p.DailyUsed.Frozen)
			require.InDelta(t, 0.2, *p.WeeklyUsed.Frozen, 1e-9)
			_, _, err = reader.CommitQuotaWithUsage(ctx, first.ReservationID, 1000, true, biz.LedgerUsage{PromptTokens: 1000})
			require.NoError(t, err)
			p, err = reader.GetSubscriptionUsage(ctx, 7001)
			require.NoError(t, err)
			require.InDelta(t, 0.2, p.DailyUsed.Settled, 1e-9)
			require.Zero(t, *p.WeeklyUsed.Frozen)
			require.InDelta(t, 0.2, p.WeeklyUsed.Settled, 1e-9)
			group.DailyLimitUSD = nil
			zero := 0.0
			group.WeeklyLimitUSD = &zero
			require.NoError(t, repo.UpdateGroup(ctx, group))
			p, err = reader.GetSubscriptionUsage(ctx, 7001)
			require.NoError(t, err)
			require.True(t, p.DailyUsed.Unlimited)
			require.Nil(t, p.DailyUsed.Available)
			require.False(t, p.WeeklyUsed.Unlimited)
			require.True(t, p.WeeklyUsed.OverLimit)
			require.Zero(t, *p.WeeklyUsed.Available)
			sub.ExpiresAt = time.Now().Add(-time.Hour).Unix()
			require.NoError(t, repo.UpdateSubscription(ctx, sub))
			_, err = uc.GetSubscriptionUsage(ctx, 7001)
			require.ErrorIs(t, err, subscriptionbiz.ErrSubscriptionNotFound)
		})
	}
}
