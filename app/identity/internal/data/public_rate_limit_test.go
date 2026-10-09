package data

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPublicRequestLimiterSharedAndFailClosed(t *testing.T) {
	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	defer rdb.Close()
	first, second := NewRepository(&Data{redis: rdb}), NewRepository(&Data{redis: rdb})
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		ok, err := first.AllowPublicRequest(ctx, "mail:alice@example.com", 3, time.Minute)
		require.NoError(t, err)
		require.True(t, ok)
	}
	ok, err := second.AllowPublicRequest(ctx, "mail:alice@example.com", 3, time.Minute)
	require.NoError(t, err)
	require.False(t, ok)
	srv.FastForward(time.Minute)
	ok, err = second.AllowPublicRequest(ctx, "mail:alice@example.com", 3, time.Minute)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = NewRepository(&Data{db: &gorm.DB{}}).AllowPublicRequest(ctx, "ip:1", 3, time.Minute)
	require.Error(t, err)
	require.False(t, ok)
}
