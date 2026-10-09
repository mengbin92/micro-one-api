package data

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"micro-one-api/domain/subscription/biz"
	"time"
)

// Redis claim expiry permits recovery after a crash; success is kept until expiry.
func (r *Repository) ClaimExpiryNotification(ctx context.Context, n biz.ExpiryNotification) (func(context.Context, bool) error, bool, error) {
	noop := func(context.Context, bool) error { return nil }
	if r.redis == nil {
		if r.db != nil {
			return noop, false, fmt.Errorf("expiry reminder deduplication requires Redis")
		}
		return noop, true, nil
	}
	key := fmt.Sprintf("subscription:expiry-notified:%d:%d", n.SubscriptionID, n.ExpiresAt)
	token := uuid.NewString()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	claimed, err := r.redis.SetNX(ctx, key, token, 5*time.Minute).Result()
	if err != nil || !claimed {
		return noop, claimed, err
	}
	complete := func(ctx context.Context, success bool) error {
		ttl := time.Until(time.Unix(n.ExpiresAt, 0)).Milliseconds()
		if ttl < 1 {
			ttl = 1
		}
		return r.redis.Eval(ctx, `if redis.call('GET',KEYS[1])~=ARGV[1] then return redis.error_reply('expiry claim lost') end
if ARGV[2]=='1' then return redis.call('SET',KEYS[1],'notified','PX',ARGV[3]) end
return redis.call('DEL',KEYS[1])`, []string{key}, token, map[bool]int{true: 1, false: 0}[success], ttl).Err()
	}
	return complete, true, nil
}
