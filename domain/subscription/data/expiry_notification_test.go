package data

import (
	"context"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"micro-one-api/domain/subscription/biz"
	"testing"
	"time"
)

func TestExpiryReminderClaimAcrossReplicas(t *testing.T) {
	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	defer rdb.Close()
	ctx := context.Background()
	first, second := NewRepository(nil, rdb), NewRepository(nil, rdb)
	n := biz.ExpiryNotification{SubscriptionID: 1, ExpiresAt: time.Now().Add(time.Hour).Unix()}
	finish, ok, err := first.ClaimExpiryNotification(ctx, n)
	require.NoError(t, err)
	require.True(t, ok)
	_, ok, err = second.ClaimExpiryNotification(ctx, n)
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, finish(ctx, false))
	finish, ok, err = second.ClaimExpiryNotification(ctx, n)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, finish(ctx, true))
	_, ok, err = first.ClaimExpiryNotification(ctx, n)
	require.NoError(t, err)
	require.False(t, ok)
	srv.FastForward(2 * time.Hour)
	require.Empty(t, srv.Keys())
}
