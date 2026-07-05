package biz

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	"micro-one-api/internal/pkg/metrics"
)

// AccountRPMLimiter caps relay dispatch attempts per rolling minute for a
// subscription account. A non-positive limit means unlimited.
type AccountRPMLimiter interface {
	TryAcquire(ctx context.Context, accountID int64, limit int32) bool
}

type MemoryAccountRPMLimiter struct {
	mu     sync.Mutex
	events map[int64][]int64
	now    func() time.Time
}

func NewAccountRPMLimiter() *MemoryAccountRPMLimiter {
	return &MemoryAccountRPMLimiter{
		events: make(map[int64][]int64),
		now:    time.Now,
	}
}

func (l *MemoryAccountRPMLimiter) TryAcquire(_ context.Context, accountID int64, limit int32) bool {
	if l == nil || limit <= 0 || accountID <= 0 {
		return true
	}
	now := time.Now()
	if l.now != nil {
		now = l.now()
	}
	nowMs := now.UnixMilli()
	cutoff := now.Add(-time.Minute).UnixMilli()

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.events == nil {
		l.events = make(map[int64][]int64)
	}
	kept := pruneRPMEvents(l.events[accountID], cutoff)
	if len(kept) >= int(limit) {
		l.events[accountID] = kept
		return false
	}
	kept = append(kept, nowMs)
	l.events[accountID] = kept
	return true
}

func pruneRPMEvents(events []int64, cutoff int64) []int64 {
	idx := 0
	for idx < len(events) && events[idx] <= cutoff {
		idx++
	}
	if idx > 0 {
		copy(events, events[idx:])
		events = events[:len(events)-idx]
	}
	return events
}

const accountRPMKeyPrefix = "subscription_account:rpm:"
const userRPMKeyPrefix = "user:rpm:"

const (
	accountRPMRedisTimeout = 3 * time.Second
	accountRPMWindow       = time.Minute
)

const redisAcquireRPMScript = `
redis.call("ZREMRANGEBYSCORE", KEYS[1], "-inf", ARGV[1])
local current = redis.call("ZCARD", KEYS[1])
if current >= tonumber(ARGV[2]) then
	return 0
end
redis.call("ZADD", KEYS[1], ARGV[3], ARGV[4])
redis.call("PEXPIRE", KEYS[1], ARGV[5])
return 1
`

type accountRPMRedis interface {
	Eval(ctx context.Context, script string, keys []string, args ...any) *redis.Cmd
}

type RedisAccountRPMLimiter struct {
	rdb       accountRPMRedis
	fallback  *MemoryAccountRPMLimiter
	keyPrefix string
	timeout   time.Duration
	window    time.Duration
	instance  string
	nextID    atomic.Uint64
}

func NewRedisAccountRPMLimiter(rdb *redis.Client) *RedisAccountRPMLimiter {
	if rdb == nil {
		return nil
	}
	return newRedisRPMLimiter(rdb, accountRPMKeyPrefix, "")
}

func newRedisAccountRPMLimiter(rdb accountRPMRedis, instance string) *RedisAccountRPMLimiter {
	return newRedisRPMLimiter(rdb, accountRPMKeyPrefix, instance)
}

func NewRedisUserRPMLimiter(rdb *redis.Client) *RedisAccountRPMLimiter {
	if rdb == nil {
		return nil
	}
	return newRedisRPMLimiter(rdb, userRPMKeyPrefix, "")
}

func newRedisRPMLimiter(rdb accountRPMRedis, keyPrefix, instance string) *RedisAccountRPMLimiter {
	if instance == "" {
		instance = strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	if keyPrefix == "" {
		keyPrefix = accountRPMKeyPrefix
	}
	return &RedisAccountRPMLimiter{
		rdb:       rdb,
		fallback:  NewAccountRPMLimiter(),
		keyPrefix: keyPrefix,
		timeout:   accountRPMRedisTimeout,
		window:    accountRPMWindow,
		instance:  instance,
	}
}

func (l *RedisAccountRPMLimiter) TryAcquire(ctx context.Context, accountID int64, limit int32) bool {
	if l == nil || l.rdb == nil {
		return true
	}
	if limit <= 0 || accountID <= 0 {
		return true
	}
	now := time.Now()
	cutoff := now.Add(-l.window).UnixMilli()
	member := l.instance + ":" + strconv.FormatInt(accountID, 10) + ":" + strconv.FormatUint(l.nextID.Add(1), 10)
	rCtx, cancel := context.WithTimeout(ctx, l.timeout)
	defer cancel()
	granted, err := l.rdb.Eval(
		rCtx,
		redisAcquireRPMScript,
		[]string{l.key(accountID)},
		cutoff,
		limit,
		now.UnixMilli(),
		member,
		int64(l.window/time.Millisecond),
	).Int64()
	if err != nil {
		metrics.RelayAccountRPMFallbackTotal.WithLabelValues("acquire_error").Inc()
		return l.fallback.TryAcquire(ctx, accountID, limit)
	}
	return granted != 0
}

func (l *RedisAccountRPMLimiter) key(accountID int64) string {
	return l.keyPrefix + strconv.FormatInt(accountID, 10)
}

var _ AccountRPMLimiter = (*MemoryAccountRPMLimiter)(nil)
var _ AccountRPMLimiter = (*RedisAccountRPMLimiter)(nil)
