package data

import (
	"context"
	"testing"

	"micro-one-api/domain/subscription/biz"

	"github.com/stretchr/testify/require"
)

func TestAddUsageRejectsExpiredSubscription(t *testing.T) {
	repo := setupSubscriptionTestDB(t)
	ctx := context.Background()
	sub := &biz.UserSubscription{UserID: 1, Status: biz.SubscriptionStatusActive, ExpiresAt: 100}
	require.NoError(t, repo.CreateSubscription(ctx, sub))
	require.ErrorIs(t, repo.AddUsage(ctx, 1, 2.5, 100), biz.ErrSubscriptionNotFound)
	got, err := repo.GetSubscriptionByID(ctx, sub.ID)
	require.NoError(t, err)
	require.Zero(t, got.DailyUsageUSD)
}

func TestMemoryUsageRejectsExpiredSubscription(t *testing.T) {
	r := NewMemoryRepositoryForTest()
	s := &biz.UserSubscription{UserID: 1, Status: biz.SubscriptionStatusActive, ExpiresAt: 100}
	ctx := context.Background()
	require.NoError(t, r.CreateSubscription(ctx, s))
	require.ErrorIs(t, r.AddUsage(ctx, 1, 2, 100), biz.ErrSubscriptionNotFound)
	got, err := r.GetSubscriptionByID(ctx, s.ID)
	require.NoError(t, err)
	require.Zero(t, got.DailyUsageUSD)
}
