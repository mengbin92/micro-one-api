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

// AccountConcurrencyLimiter caps the number of in-flight relay requests per
// subscription account. It is the enforcement side of
// SubscriptionAccount.Concurrency: channel-service owns the configured limit,
// the gateway holds a slot for the lifetime of each upstream call (including the
// full duration of a streamed response) so a single account is never saturated
// into upstream 429/529s.
type AccountConcurrencyLimiter interface {
	TryAcquire(ctx context.Context, accountID int64, limit int32) (func(), bool)
}

// MemoryAccountConcurrencyLimiter enforces account concurrency inside a single
// relay-gateway process.
type MemoryAccountConcurrencyLimiter struct {
	mu       sync.Mutex
	inflight map[int64]int32
}

// NewAccountConcurrencyLimiter builds an empty limiter.
func NewAccountConcurrencyLimiter() *MemoryAccountConcurrencyLimiter {
	return &MemoryAccountConcurrencyLimiter{inflight: make(map[int64]int32)}
}

// TryAcquire reserves a concurrency slot for accountID. It returns a release
// function and true when a slot was granted, or (nil, false) when the account is
// already at its limit. The release function is idempotent and safe to call from
// any goroutine.
//
// A non-positive limit (or a nil limiter / non-positive accountID) means
// "unlimited": TryAcquire always succeeds and returns a no-op release so callers
// need not special-case the unlimited path.
func (l *MemoryAccountConcurrencyLimiter) TryAcquire(_ context.Context, accountID int64, limit int32) (func(), bool) {
	if l == nil || limit <= 0 || accountID <= 0 {
		return func() {}, true
	}
	l.mu.Lock()
	if l.inflight == nil {
		l.inflight = make(map[int64]int32)
	}
	if l.inflight[accountID] >= limit {
		l.mu.Unlock()
		return nil, false
	}
	l.inflight[accountID]++
	l.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			if l.inflight[accountID] <= 1 {
				delete(l.inflight, accountID)
			} else {
				l.inflight[accountID]--
			}
			l.mu.Unlock()
		})
	}, true
}

// Inflight returns the current in-flight count for accountID. Intended for
// tests and observability, not the hot path.
func (l *MemoryAccountConcurrencyLimiter) Inflight(accountID int64) int32 {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.inflight[accountID]
}

// accountConcurrencyKeyPrefix namespaces cross-replica account-concurrency
// slots in Redis.
const accountConcurrencyKeyPrefix = "subscription_account:concurrency:"

const (
	accountConcurrencyRedisTimeout = 3 * time.Second
	defaultAccountConcurrencyTTL   = 2 * time.Minute
)

const redisAcquireConcurrencyScript = `
redis.call("ZREMRANGEBYSCORE", KEYS[1], "-inf", ARGV[1])
local current = redis.call("ZCARD", KEYS[1])
if current >= tonumber(ARGV[2]) then
	return 0
end
redis.call("ZADD", KEYS[1], ARGV[3], ARGV[4])
redis.call("PEXPIRE", KEYS[1], ARGV[5])
return 1
`

type accountConcurrencyRedis interface {
	Eval(ctx context.Context, script string, keys []string, args ...any) *redis.Cmd
	ZAdd(ctx context.Context, key string, members ...redis.Z) *redis.IntCmd
	Expire(ctx context.Context, key string, expiration time.Duration) *redis.BoolCmd
	ZRem(ctx context.Context, key string, members ...any) *redis.IntCmd
	ZCard(ctx context.Context, key string) *redis.IntCmd
}

// RedisAccountConcurrencyLimiter shares subscription-account concurrency across
// relay replicas. Slots are short Redis leases and are refreshed while the
// request is in flight; if a process dies, the lease expires and frees the slot.
// Redis command failures fail open to the memory limiter so a Redis outage
// degrades to the pre-Redis behaviour instead of blocking all requests.
type RedisAccountConcurrencyLimiter struct {
	rdb       accountConcurrencyRedis
	fallback  *MemoryAccountConcurrencyLimiter
	keyPrefix string
	timeout   time.Duration
	leaseTTL  time.Duration
	instance  string
	nextID    atomic.Uint64
}

// NewRedisAccountConcurrencyLimiter builds a Redis-backed account-concurrency
// limiter. Returns nil when rdb is nil so callers can keep the memory limiter.
func NewRedisAccountConcurrencyLimiter(rdb *redis.Client) *RedisAccountConcurrencyLimiter {
	if rdb == nil {
		return nil
	}
	return newRedisAccountConcurrencyLimiter(rdb, "")
}

func newRedisAccountConcurrencyLimiter(rdb accountConcurrencyRedis, instance string) *RedisAccountConcurrencyLimiter {
	if instance == "" {
		instance = strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return &RedisAccountConcurrencyLimiter{
		rdb:       rdb,
		fallback:  NewAccountConcurrencyLimiter(),
		keyPrefix: accountConcurrencyKeyPrefix,
		timeout:   accountConcurrencyRedisTimeout,
		leaseTTL:  defaultAccountConcurrencyTTL,
		instance:  instance,
	}
}

func (l *RedisAccountConcurrencyLimiter) TryAcquire(ctx context.Context, accountID int64, limit int32) (func(), bool) {
	if l == nil || l.rdb == nil {
		return func() {}, true
	}
	if limit <= 0 || accountID <= 0 {
		return func() {}, true
	}
	key := l.key(accountID)
	member := l.slotMember(accountID)
	now := time.Now()
	deadline := now.Add(l.leaseTTL)
	rCtx, cancel := context.WithTimeout(ctx, l.timeout)
	defer cancel()
	granted, err := l.rdb.Eval(
		rCtx,
		redisAcquireConcurrencyScript,
		[]string{key},
		now.UnixMilli(),
		limit,
		deadline.UnixMilli(),
		member,
		int64(l.leaseTTL/time.Millisecond),
	).Int64()
	if err != nil {
		metrics.RelayAccountConcurrencyFallbackTotal.WithLabelValues("acquire_error").Inc()
		return l.fallback.TryAcquire(ctx, accountID, limit)
	}
	if granted == 0 {
		return nil, false
	}

	done := make(chan struct{})
	var once sync.Once
	go l.refreshLease(ctx, key, member, done)
	return func() {
		once.Do(func() {
			close(done)
			rCtx, cancel := context.WithTimeout(context.Background(), l.timeout)
			defer cancel()
			if err := l.rdb.ZRem(rCtx, key, member).Err(); err != nil {
				metrics.RelayAccountConcurrencyFallbackTotal.WithLabelValues("release_error").Inc()
			}
		})
	}, true
}

func (l *RedisAccountConcurrencyLimiter) Inflight(ctx context.Context, accountID int64) int32 {
	if l == nil || l.rdb == nil || accountID <= 0 {
		return 0
	}
	rCtx, cancel := context.WithTimeout(ctx, l.timeout)
	defer cancel()
	n, err := l.rdb.ZCard(rCtx, l.key(accountID)).Result()
	if err != nil {
		metrics.RelayAccountConcurrencyFallbackTotal.WithLabelValues("count_error").Inc()
		return 0
	}
	return int32(n)
}

func (l *RedisAccountConcurrencyLimiter) refreshLease(ctx context.Context, key, member string, done <-chan struct{}) {
	interval := l.leaseTTL / 2
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			deadline := time.Now().Add(l.leaseTTL).UnixMilli()
			rCtx, cancel := context.WithTimeout(ctx, l.timeout)
			if err := l.rdb.ZAdd(rCtx, key, redis.Z{Score: float64(deadline), Member: member}).Err(); err != nil {
				metrics.RelayAccountConcurrencyFallbackTotal.WithLabelValues("refresh_error").Inc()
				cancel()
				continue
			}
			if err := l.rdb.Expire(rCtx, key, l.leaseTTL).Err(); err != nil {
				metrics.RelayAccountConcurrencyFallbackTotal.WithLabelValues("refresh_error").Inc()
			}
			cancel()
		}
	}
}

func (l *RedisAccountConcurrencyLimiter) key(accountID int64) string {
	return l.keyPrefix + strconv.FormatInt(accountID, 10)
}

func (l *RedisAccountConcurrencyLimiter) slotMember(accountID int64) string {
	id := l.nextID.Add(1)
	return l.instance + ":" + strconv.FormatInt(accountID, 10) + ":" + strconv.FormatUint(id, 10)
}

var _ AccountConcurrencyLimiter = (*MemoryAccountConcurrencyLimiter)(nil)
var _ AccountConcurrencyLimiter = (*RedisAccountConcurrencyLimiter)(nil)
