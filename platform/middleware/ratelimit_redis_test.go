package middleware

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
)

func TestRedisRateLimiter_NilClient(t *testing.T) {
	limiter := NewRedisRateLimiter(nil, nil)
	allowed, err := limiter.Allow(context.Background(), "test-key")
	assert.NoError(t, err)
	assert.True(t, allowed)
}

func TestDefaultRedisRateLimitConfig(t *testing.T) {
	cfg := DefaultRedisRateLimitConfig()
	assert.Equal(t, 100, cfg.RequestsPerSecond)
	assert.Equal(t, 200, cfg.Burst)
	assert.Equal(t, "ratelimit:", cfg.KeyPrefix)
}

func TestRedisRateLimiter_LimitBoundary(t *testing.T) {
	// Use miniredis to exercise the actual Lua script so we verify the
	// admit/reject sentinel contract (review High #4): requests up to and
	// including `limit` are admitted; the next request is rejected.
	s, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis not available: %v", err)
	}
	defer s.Close()

	rdb := redis.NewClient(&redis.Options{Addr: s.Addr()})
	defer rdb.Close()

	const limit = 5
	limiter := NewRedisRateLimiter(rdb, &RedisRateLimitConfig{
		RequestsPerSecond: limit,
		Burst:             limit,
		Window:            10 * time.Second,
		KeyPrefix:         "test:",
	})

	ctx := context.Background()
	// The first `limit` requests must be allowed.
	for i := range limit {
		allowed, err := limiter.Allow(ctx, "boundary-key")
		assert.NoError(t, err, "request %d should not error", i+1)
		assert.True(t, allowed, "request %d (within limit %d) should be allowed", i+1, limit)
	}
	// The (limit+1)th request must be rejected — this is the exact boundary
	// that the previous off-by-one (`> rate` vs `>= rate` / sentinel
	// `limit` vs `limit+1`) let through.
	allowed, err := limiter.Allow(ctx, "boundary-key")
	assert.NoError(t, err)
	assert.False(t, allowed, "request beyond limit should be rejected")
}

func TestRedisRateLimiter_UsesBurstCapacity(t *testing.T) {
	s, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: s.Addr()})
	defer rdb.Close()
	limiter := NewRedisRateLimiter(rdb, &RedisRateLimitConfig{
		RequestsPerSecond: 1,
		Burst:             3,
		Window:            time.Second,
		KeyPrefix:         "burst:",
	})
	for i := range 3 {
		allowed, err := limiter.Allow(context.Background(), "client")
		assert.NoError(t, err)
		assert.True(t, allowed, "burst request %d", i+1)
	}
	allowed, err := limiter.Allow(context.Background(), "client")
	assert.NoError(t, err)
	assert.False(t, allowed, "request beyond burst should be rejected")
}
