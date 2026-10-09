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

type publicRequestCount struct {
	count   int
	expires time.Time
}

func (r *Repository) AllowPublicRequest(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	key = "public:" + key
	if r.redis != nil {
		count, err := r.redis.Eval(ctx, `local n = redis.call('INCR', KEYS[1]); if n == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[1]) end; return n`, []string{loginFailureRedisKey(key)}, window.Milliseconds()).Int64()
		return count <= int64(limit), err
	}
	if r.db != nil {
		return false, errLoginRateLimiterUnavailable
	}
	r.identityLock.Lock()
	defer r.identityLock.Unlock()
	now := time.Now()
	for key, value := range r.publicRequests {
		if !value.expires.After(now) {
			delete(r.publicRequests, key)
		}
	}
	if r.publicRequests == nil {
		r.publicRequests = make(map[string]publicRequestCount)
	}
	value := r.publicRequests[key]
	if value.count == 0 {
		if len(r.publicRequests) >= 10000 {
			return false, nil
		}
		value.expires = now.Add(window)
	}
	if value.count >= limit {
		return false, nil
	}
	value.count++
	r.publicRequests[key] = value
	return true, nil
}

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
