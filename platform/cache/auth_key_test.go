package cache

import (
	"context"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	identityv1 "micro-one-api/api/identity/v1"
)

func TestAuthCacheDoesNotExposeTokenInRedis(t *testing.T) {
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	const token = "sk-private-api-token"
	c, err := NewAuthCache(client, nil, func(_ context.Context, key string) (*identityv1.GetAuthSnapshotReply, error) {
		require.Equal(t, token, key)
		return &identityv1.GetAuthSnapshotReply{UserId: 1}, nil
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	_, err = c.Get(context.Background(), token)
	require.NoError(t, err)
	require.Len(t, srv.Keys(), 1)
	require.False(t, strings.Contains(srv.Keys()[0], token))
	require.NoError(t, c.Invalidate(context.Background(), token))
	require.Empty(t, srv.Keys())
}
