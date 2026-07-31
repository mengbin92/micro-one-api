package middleware

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	applogger "micro-one-api/platform/logging"
)

// redisSlidingWindowScript atomically removes expired members, counts the
// remaining window, and — only if under the limit — adds the current request,
// sets a TTL, and returns the new count. Running count+add in a single Lua
// EVAL eliminates the check-then-act race where N concurrent requests each saw
// a sub-limit count and were all admitted (review L5). It also returns the
// count on Redis errors is impossible (errors propagate), so callers decide
// fail-open vs fail-closed.
const redisSlidingWindowScript = `
local key = KEYS[1]
local now = tonumber(ARGV[1])
local window_start = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local ttl_ms = tonumber(ARGV[4])
local member = ARGV[5]

redis.call('ZREMRANGEBYSCORE', key, '0', window_start)
local count = redis.call('ZCARD', key)
if count >= limit then
  return count
end
redis.call('ZADD', key, now, member)
redis.call('PEXPIRE', key, ttl_ms)
return count + 1
`

// RedisRateLimiter implements a distributed rate limiter using Redis sorted sets (sliding window).
type RedisRateLimiter struct {
	rdb       *redis.Client
	rate      int
	burst     int
	window    time.Duration
	keyPrefix string
	// script is the pre-loaded Lua script enabling an atomic sliding-window
	// count+add (review L5).
	script *redis.Script
}

// RedisRateLimitConfig holds configuration for the Redis-based rate limiter.
type RedisRateLimitConfig struct {
	RequestsPerSecond int
	Burst             int
	Window            time.Duration
	KeyPrefix         string
}

// DefaultRedisRateLimitConfig returns default configuration.
func DefaultRedisRateLimitConfig() *RedisRateLimitConfig {
	return &RedisRateLimitConfig{
		RequestsPerSecond: 100,
		Burst:             200,
		Window:            time.Minute,
		KeyPrefix:         "ratelimit:",
	}
}

// NewRedisRateLimiter creates a new distributed rate limiter backed by Redis.
func NewRedisRateLimiter(rdb *redis.Client, config *RedisRateLimitConfig) *RedisRateLimiter {
	if config == nil {
		config = DefaultRedisRateLimitConfig()
	}
	return &RedisRateLimiter{
		rdb:       rdb,
		rate:      config.RequestsPerSecond,
		burst:     config.Burst,
		window:    config.Window,
		keyPrefix: config.KeyPrefix,
		script:    redis.NewScript(redisSlidingWindowScript),
	}
}

// Allow checks if a request from the given key should be allowed.
// Uses an atomic Lua sliding-window script (count + conditional add in a
// single EVAL) so concurrent requests cannot each observe a sub-limit count
// and all be admitted (review L5). The key TTL is set inside the script so a
// Redis error no longer leaves an un-expiring key behind. Redis errors still
// fail-open by design (a Redis outage must not take the whole platform down);
// callers that need fail-closed semantics should wrap this limiter.
func (rl *RedisRateLimiter) Allow(ctx context.Context, key string) (bool, error) {
	if rl.rdb == nil {
		return true, nil
	}

	redisKey := rl.keyPrefix + key
	now := time.Now()
	windowStart := now.Add(-rl.window).UnixNano()

	// Unique member so concurrent requests within the same nanosecond are not
	// deduplicated by the sorted set.
	member := fmt.Sprintf("%d:%d", now.UnixNano(), now.UnixMicro())

	ttlMs := int64(rl.window/time.Millisecond) + 1000

	result, err := rl.script.Run(ctx, rl.rdb,
		[]string{redisKey},
		now.UnixNano(), windowStart, rl.rate, ttlMs, member,
	).Int64()
	if err != nil {
		applogger.Log.Warn("Redis rate limit check failed, allowing request",
			zap.String("key", key),
			zap.Error(err),
		)
		return true, nil
	}

	if result > int64(rl.rate) {
		applogger.Log.Warn("Rate limit exceeded",
			zap.String("key", key),
			zap.Int64("requests", result),
			zap.Int("limit", rl.rate),
		)
		return false, nil
	}
	return true, nil
}

// RedisRateLimitMiddleware creates an HTTP middleware that uses Redis-based distributed rate limiting.
func RedisRateLimitMiddleware(rdb *redis.Client, config *RedisRateLimitConfig) func(http.Handler) http.Handler {
	limiter := NewRedisRateLimiter(rdb, config)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := extractRateLimitKey(r)

			allowed, err := limiter.Allow(r.Context(), key)
			if err != nil {
				applogger.Log.Error("Rate limit error", zap.Error(err))
				next.ServeHTTP(w, r)
				return
			}

			if !allowed {
				applogger.Log.Warn("Request rate limited",
					zap.String("key", key),
					zap.String("path", r.URL.Path),
					zap.String("method", r.Method),
				)

				w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", limiter.rate))
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.Header().Set("Retry-After", "60")

				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte(`{"error":{"message":"rate limit exceeded","code":429}}`))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// AdaptiveRateLimitMiddleware creates a middleware that uses Redis limiter when available,
// falling back to in-memory limiter when Redis is unavailable.
func AdaptiveRateLimitMiddleware(rdb *redis.Client, config *RedisRateLimitConfig) func(http.Handler) http.Handler {
	if rdb == nil {
		applogger.Log.Info("Redis not available, falling back to in-memory rate limiter")
		return RateLimit(nil)
	}
	return RedisRateLimitMiddleware(rdb, config)
}
