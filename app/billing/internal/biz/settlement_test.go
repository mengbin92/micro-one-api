package biz

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
)

func TestRoutingSettlementQuotaUsesEveryCurrentWindow(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	for _, tc := range []struct {
		name                    string
		dailyStart, weeklyStart int64
		daily, weekly           float64
		want                    bool
	}{
		{"daily exhausted", now.Unix(), now.Unix(), 1, 0, false},
		{"weekly exhausted", now.Unix(), now.Unix(), 0, 1, false},
		{"daily rolled", now.Add(-25 * time.Hour).Unix(), now.Unix(), 1, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			uc := NewBillingUsecaseWithPricing(nil, nil, nil, nil, PricingConfig{})
			uc.options.Now = func() time.Time { return now }
			uc.subscription = &mockSubscriptionPrimitives{
				subscription: &subscriptionbiz.UserSubscription{ID: 1, UserID: 1, GroupID: 2, Status: subscriptionbiz.SubscriptionStatusActive, ExpiresAt: now.Add(time.Hour).Unix(), DailyWindowStart: tc.dailyStart, WeeklyWindowStart: tc.weeklyStart, DailyUsageUSD: tc.daily, WeeklyUsageUSD: tc.weekly},
				group:        &subscriptionbiz.SubscriptionGroup{ID: 2, DailyLimitUSD: fp(1), WeeklyLimitUSD: fp(1), RateMultiplier: 1},
			}
			if tc.name == "daily rolled" {
				uc.subscription.(*mockSubscriptionPrimitives).group.WeeklyLimitUSD = nil
			}
			covers, quota, err := uc.subscriptionCoverage(context.Background(), 1, 10)
			require.NoError(t, err)
			require.True(t, covers)
			require.Equal(t, tc.want, quota)
		})
	}
}
