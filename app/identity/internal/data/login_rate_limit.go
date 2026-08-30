package data

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

var errLoginRateLimiterUnavailable = errors.New("login rate limiter unavailable")

func loginFailureRedisKey(key string) string {
	digest := sha256.Sum256([]byte(key))
	return "identity:login-fail:" + hex.EncodeToString(digest[:])
}

// LoginFailureCount returns the failed-login count shared by every identity
// replica. Usernames and addresses are hashed before becoming Redis keys.
func (r *Repository) LoginFailureCount(ctx context.Context, key string) (int64, error) {
	if r.redis == nil {
		return 0, errLoginRateLimiterUnavailable
	}
	count, err := r.redis.Get(ctx, loginFailureRedisKey(key)).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return count, err
}

// RecordLoginFailure atomically increments the shared counter and refreshes
// its lockout window, matching the in-process limiter's sliding expiry.
func (r *Repository) RecordLoginFailure(ctx context.Context, key string, window time.Duration) error {
	if r.redis == nil {
		return errLoginRateLimiterUnavailable
	}
	redisKey := loginFailureRedisKey(key)
	_, err := r.redis.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Incr(ctx, redisKey)
		pipe.Expire(ctx, redisKey, window)
		return nil
	})
	return err
}

// ClearLoginFailures removes successful-login buckets from the shared store.
func (r *Repository) ClearLoginFailures(ctx context.Context, keys ...string) error {
	if r.redis == nil {
		return errLoginRateLimiterUnavailable
	}
	redisKeys := make([]string, 0, len(keys))
	for _, key := range keys {
		if key != "" {
			redisKeys = append(redisKeys, loginFailureRedisKey(key))
		}
	}
	if len(redisKeys) == 0 {
		return nil
	}
	return r.redis.Del(ctx, redisKeys...).Err()
}
