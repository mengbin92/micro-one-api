package data

import (
	"context"
	"strconv"
	"time"

	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/pkg/safecast"

	"github.com/redis/go-redis/v9"
)

// accountConcurrencyKeyPrefix MUST stay in lockstep with
// internal/biz.accountConcurrencyKeyPrefix: the relay-gateway records each
// in-flight subscription-account slot as a ZSet member under this key, and
// this oracle counts them to drive cross-replica load-aware selection in
// channel-service. The prefix is duplicated here rather than imported,
// because internal/biz is the relay-gateway package (layering: channel data
// never imports the relay gateway).
const accountConcurrencyKeyPrefix = "subscription_account:concurrency:"

// redisLoadOracle implements biz.LoadOracle by counting the live in-flight
// slots the relay-gateway holds in Redis per subscription account. It is the
// Phase D #12 cross-replica load source for SubscriptionAccountSelector.
type redisLoadOracle struct {
	rdb     *redis.Client
	timeout time.Duration
}

// NewRedisLoadOracle builds a biz.LoadOracle over the given Redis client.
// Returns nil when rdb is nil so callers can skip wiring (the selector then
// uses its noop oracle and loadFactor is neutral).
func NewRedisLoadOracle(rdb *redis.Client) biz.LoadOracle {
	if rdb == nil {
		return nil
	}
	return &redisLoadOracle{rdb: rdb, timeout: 200 * time.Millisecond}
}

func (o *redisLoadOracle) Inflight(ctx context.Context, accountID int64) int32 {
	if o == nil || o.rdb == nil || accountID <= 0 {
		return 0
	}
	rCtx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	key := accountConcurrencyKeyPrefix + strconv.FormatInt(accountID, 10)
	// L1 fix: count only members whose lease score is still in the future.
	// The acquire Lua only reaps expired members on the write path, so ZCARD
	// would include dead leases from crashed replicas for up to one leaseTTL.
	// ZCOUNT key now +inf excludes expired-but-unreaped members.
	now := time.Now().UnixMilli()
	n, err := o.rdb.ZCount(rCtx, key, strconv.FormatInt(now, 10), "+inf").Result()
	if err != nil {
		return 0
	}
	return safecast.Int64ToInt32Saturating(n)
}

// InflightBatch pipelines a ZCOUNT per account in a single round-trip
// (MEDIUM-2), so selecting an N-account tier costs one Redis RTT instead of
// N serial ones. Absent accounts default to 0 (no live load).
func (o *redisLoadOracle) InflightBatch(ctx context.Context, accountIDs []int64) map[int64]int32 {
	out := make(map[int64]int32, len(accountIDs))
	if o == nil || o.rdb == nil || len(accountIDs) == 0 {
		return out
	}
	rCtx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	now := strconv.FormatInt(time.Now().UnixMilli(), 10)
	pipe := o.rdb.Pipeline()
	cmds := make([]*redis.IntCmd, len(accountIDs))
	for i, id := range accountIDs {
		if id <= 0 {
			continue
		}
		key := accountConcurrencyKeyPrefix + strconv.FormatInt(id, 10)
		// L1 fix: ZCOUNT key now +inf excludes expired-but-unreaped leases.
		cmds[i] = pipe.ZCount(rCtx, key, now, "+inf")
	}
	if _, err := pipe.Exec(rCtx); err != nil && err != redis.Nil {
		// On pipeline failure, fall back to per-account reads so a transient
		// Redis hiccup does not zero out every account (which would over-load
		// saturated accounts). Individual errors still yield 0.
		for _, id := range accountIDs {
			if id <= 0 {
				continue
			}
			out[id] = o.Inflight(ctx, id)
		}
		return out
	}
	for i, id := range accountIDs {
		if id <= 0 {
			continue
		}
		if cmds[i] != nil {
			out[id] = safecast.Int64ToInt32Saturating(cmds[i].Val())
		}
	}
	return out
}
