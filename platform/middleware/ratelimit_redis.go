package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	applogger "micro-one-api/platform/logging"
)

// RedisRateLimiter implements a distributed rate limiter using Redis sorted sets (sliding window).
type RedisRateLimiter struct {
	rdb       *redis.Client
	rate      int
	burst     int
	window    time.Duration
	keyPrefix string
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
	}
}

// Allow checks if a request from the given key should be allowed.
// Uses Redis ZRANGEBYSCORE to implement a sliding window counter.
func (rl *RedisRateLimiter) Allow(ctx context.Context, key string) (bool, error) {
	if rl.rdb == nil {
		return true, nil
	}

	redisKey := rl.keyPrefix + key
	now := time.Now()
	windowStart := now.Add(-rl.window)

	pipe := rl.rdb.Pipeline()

	// Remove expired entries
	pipe.ZRemRangeByScore(ctx, redisKey, "0", strconv.FormatInt(windowStart.UnixNano(), 10))

	// Count current window requests
	countCmd := pipe.ZCard(ctx, redisKey)

	// Execute pipeline
	if _, err := pipe.Exec(ctx); err != nil {
		applogger.Log.Warn("Redis rate limit check failed, allowing request",
			zap.String("key", key),
			zap.Error(err),
		)
		return true, nil
	}

	currentCount := countCmd.Val()
	if currentCount >= int64(rl.rate) {
		applogger.Log.Warn("Rate limit exceeded",
			zap.String("key", key),
			zap.Int64("requests", currentCount),
			zap.Int("limit", rl.rate),
		)
		return false, nil
	}

	// Add current request with a unique member to avoid dedup
	member := fmt.Sprintf("%d:%d", now.UnixNano(), now.UnixMicro())
	rl.rdb.ZAdd(ctx, redisKey, redis.Z{
		Score:  float64(now.UnixNano()),
		Member: member,
	})

	// Set TTL on the key to auto-cleanup
	rl.rdb.Expire(ctx, redisKey, rl.window+time.Second)

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
