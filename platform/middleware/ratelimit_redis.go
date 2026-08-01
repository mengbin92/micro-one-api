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

// redisTokenBucketScript atomically refills and consumes a token. Redis
// executes the Lua script as one operation, so concurrent instances cannot
// each observe the same token.
const redisTokenBucketScript = `
local key = KEYS[1]
local now = tonumber(ARGV[1])
local rate = tonumber(ARGV[2])
local burst = tonumber(ARGV[3])
local ttl_ms = tonumber(ARGV[4])
local tokens = tonumber(redis.call('HGET', key, 'tokens'))
local last = tonumber(redis.call('HGET', key, 'last_ms'))
if tokens == nil then tokens = burst; last = now end
local elapsed = math.max(0, now - last)
tokens = math.min(burst, tokens + elapsed * rate / 1000.0)
if tokens < 1 then
  redis.call('HSET', key, 'tokens', tokens, 'last_ms', now)
  redis.call('PEXPIRE', key, ttl_ms)
  return 0
end
tokens = tokens - 1
redis.call('HSET', key, 'tokens', tokens, 'last_ms', now)
redis.call('PEXPIRE', key, ttl_ms)
return 1
`

// RedisRateLimiter implements a distributed token-bucket limiter using Redis.
type RedisRateLimiter struct {
	rdb       *redis.Client
	rate      int
	burst     int
	keyPrefix string
	// script is the pre-loaded atomic token-bucket update.
	script *redis.Script
}

// RedisRateLimitConfig holds configuration for the Redis-based rate limiter.
type RedisRateLimitConfig struct {
	RequestsPerSecond int
	Burst             int
	// Window is retained for configuration compatibility. Token-bucket
	// limiting always interprets RequestsPerSecond as a per-second refill.
	Window    time.Duration
	KeyPrefix string
}

// DefaultRedisRateLimitConfig returns default configuration.
func DefaultRedisRateLimitConfig() *RedisRateLimitConfig {
	return &RedisRateLimitConfig{
		RequestsPerSecond: 100,
		Burst:             200,
		Window:            time.Second,
		KeyPrefix:         "ratelimit:",
	}
}

// NewRedisRateLimiter creates a new distributed rate limiter backed by Redis.
func NewRedisRateLimiter(rdb *redis.Client, config *RedisRateLimitConfig) *RedisRateLimiter {
	if config == nil {
		config = DefaultRedisRateLimitConfig()
	}
	rate := config.RequestsPerSecond
	if rate <= 0 {
		rate = 1
	}
	burst := config.Burst
	if burst <= 0 {
		burst = rate
	}
	return &RedisRateLimiter{
		rdb:       rdb,
		rate:      rate,
		burst:     burst,
		keyPrefix: config.KeyPrefix,
		script:    redis.NewScript(redisTokenBucketScript),
	}
}

// Allow checks if a request from the given key should be allowed.
// Uses an atomic Lua token-bucket update so concurrent requests cannot each
// observe the same token and all be admitted. The key TTL is set inside the
// script so a Redis error no longer leaves an un-expiring key behind. Redis errors still
// fail-open by design (a Redis outage must not take the whole platform down);
// callers that need fail-closed semantics should wrap this limiter.
func (rl *RedisRateLimiter) Allow(ctx context.Context, key string) (bool, error) {
	if rl.rdb == nil {
		return true, nil
	}

	redisKey := rl.keyPrefix + key
	now := time.Now()
	ttlMs := int64(float64(rl.burst)/float64(rl.rate)*1000) + 1000

	result, err := rl.script.Run(ctx, rl.rdb,
		[]string{redisKey},
		now.UnixMilli(), rl.rate, rl.burst, ttlMs,
	).Int64()
	if err != nil {
		applogger.Log.Warn("Redis rate limit check failed, allowing request",
			zap.String("key", key),
			zap.Error(err),
		)
		return true, nil
	}

	if result == 0 {
		applogger.Log.Warn("Rate limit exceeded",
			zap.String("key", key),
			zap.Bool("token_available", false),
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
				w.Header().Set("Retry-After", "1")

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
