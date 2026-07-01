package biz

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// RuntimeBlock records short-lived account blocks observed while relaying
// upstream traffic. These blocks are operational, not account lifecycle state.
type RuntimeBlock struct {
	AccountID int64
	Until     time.Time
	Reason    string
}

type RuntimeBlockerMetrics struct {
	Blocks     int64
	Hits       int64
	Expired    int64
	Clears     int64
	ActiveSize int64
}

// RuntimeBlocker filters subscription accounts that recently failed at
// runtime. Implementations may be in-memory, Redis-backed, or both.
type RuntimeBlocker interface {
	Block(ctx context.Context, accountID int64, until time.Time, reason string) error
	Clear(ctx context.Context, accountID int64) error
	IsBlocked(ctx context.Context, accountID int64, now time.Time) (RuntimeBlock, bool)
	Metrics() RuntimeBlockerMetrics
}

type NoopRuntimeBlocker struct{}

func (NoopRuntimeBlocker) Block(context.Context, int64, time.Time, string) error { return nil }
func (NoopRuntimeBlocker) Clear(context.Context, int64) error                    { return nil }
func (NoopRuntimeBlocker) IsBlocked(context.Context, int64, time.Time) (RuntimeBlock, bool) {
	return RuntimeBlock{}, false
}
func (NoopRuntimeBlocker) Metrics() RuntimeBlockerMetrics { return RuntimeBlockerMetrics{} }

// MemoryRuntimeBlocker is the default relay-gateway runtime blocker. It keeps
// short TTL blocks local to the process and prunes expired entries on access.
type MemoryRuntimeBlocker struct {
	mu     sync.Mutex
	blocks map[int64]RuntimeBlock

	blockCount  atomic.Int64
	hitCount    atomic.Int64
	expireCount atomic.Int64
	clearCount  atomic.Int64
}

func NewMemoryRuntimeBlocker() *MemoryRuntimeBlocker {
	return &MemoryRuntimeBlocker{blocks: make(map[int64]RuntimeBlock)}
}

func (b *MemoryRuntimeBlocker) Block(_ context.Context, accountID int64, until time.Time, reason string) error {
	if b == nil || accountID <= 0 || until.IsZero() {
		return nil
	}
	b.mu.Lock()
	if b.blocks == nil {
		b.blocks = make(map[int64]RuntimeBlock)
	}
	b.blocks[accountID] = RuntimeBlock{AccountID: accountID, Until: until, Reason: reason}
	b.mu.Unlock()
	b.blockCount.Add(1)
	return nil
}

func (b *MemoryRuntimeBlocker) Clear(_ context.Context, accountID int64) error {
	if b == nil || accountID <= 0 {
		return nil
	}
	b.mu.Lock()
	delete(b.blocks, accountID)
	b.mu.Unlock()
	b.clearCount.Add(1)
	return nil
}

func (b *MemoryRuntimeBlocker) IsBlocked(_ context.Context, accountID int64, now time.Time) (RuntimeBlock, bool) {
	if b == nil || accountID <= 0 {
		return RuntimeBlock{}, false
	}
	if now.IsZero() {
		now = time.Now()
	}
	b.mu.Lock()
	block, ok := b.blocks[accountID]
	if ok && !block.Until.After(now) {
		delete(b.blocks, accountID)
		ok = false
		b.expireCount.Add(1)
	}
	b.mu.Unlock()
	if ok {
		b.hitCount.Add(1)
		return block, true
	}
	return RuntimeBlock{}, false
}

func (b *MemoryRuntimeBlocker) Metrics() RuntimeBlockerMetrics {
	if b == nil {
		return RuntimeBlockerMetrics{}
	}
	b.mu.Lock()
	active := int64(len(b.blocks))
	b.mu.Unlock()
	return RuntimeBlockerMetrics{
		Blocks:     b.blockCount.Load(),
		Hits:       b.hitCount.Load(),
		Expired:    b.expireCount.Load(),
		Clears:     b.clearCount.Load(),
		ActiveSize: active,
	}
}

var _ RuntimeBlocker = (*MemoryRuntimeBlocker)(nil)
var _ RuntimeBlocker = NoopRuntimeBlocker{}

// runtimeBlockKeyPrefix namespaces runtime-block keys in Redis.
const runtimeBlockKeyPrefix = "relay_rt_block:"

// runtimeRedisTimeout bounds each Redis call so a slow store never stalls the
// relay hot path (account selection). On timeout reads fail open.
const runtimeRedisTimeout = 3 * time.Second

// runtimeRedis is the subset of *redis.Client used by RedisRuntimeBlocker.
// It exists so tests can supply a fake without a live Redis.
type runtimeRedis interface {
	Set(ctx context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd
	Get(ctx context.Context, key string) *redis.StringCmd
	Del(ctx context.Context, keys ...string) *redis.IntCmd
	Scan(ctx context.Context, cursor uint64, match string, count int64) *redis.ScanCmd
}

// RedisRuntimeBlocker stores short-lived account blocks in Redis so every relay
// replica observes the same runtime state. Unlike MemoryRuntimeBlocker (whose
// blocks are per-process), a 429/5xx seen by one replica cools the account down
// for all of them. Expiry is delegated to Redis TTL.
//
// Reads fail open: if Redis is unreachable, IsBlocked reports "not blocked"
// rather than making every account unschedulable — a Redis blip degrades to the
// pre-blocker behaviour instead of taking the gateway down.
type RedisRuntimeBlocker struct {
	rdb       runtimeRedis
	keyPrefix string
	timeout   time.Duration

	blockCount atomic.Int64
	hitCount   atomic.Int64
	clearCount atomic.Int64
	errorCount atomic.Int64
}

// NewRedisRuntimeBlocker builds a Redis-backed blocker. Returns nil when rdb is
// nil so callers can fall back to the in-memory blocker for single-replica or
// Redis-less deployments.
func NewRedisRuntimeBlocker(rdb *redis.Client) *RedisRuntimeBlocker {
	if rdb == nil {
		return nil
	}
	return newRedisRuntimeBlocker(rdb)
}

func newRedisRuntimeBlocker(rdb runtimeRedis) *RedisRuntimeBlocker {
	return &RedisRuntimeBlocker{
		rdb:       rdb,
		keyPrefix: runtimeBlockKeyPrefix,
		timeout:   runtimeRedisTimeout,
	}
}

func (b *RedisRuntimeBlocker) key(accountID int64) string {
	return b.keyPrefix + strconv.FormatInt(accountID, 10)
}

func (b *RedisRuntimeBlocker) Block(ctx context.Context, accountID int64, until time.Time, reason string) error {
	if b == nil || b.rdb == nil || accountID <= 0 || until.IsZero() {
		return nil
	}
	ttl := time.Until(until)
	if ttl <= 0 {
		return nil
	}
	rCtx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	if err := b.rdb.Set(rCtx, b.key(accountID), encodeRuntimeBlockValue(until, reason), ttl).Err(); err != nil {
		b.errorCount.Add(1)
		return err
	}
	b.blockCount.Add(1)
	return nil
}

func (b *RedisRuntimeBlocker) Clear(ctx context.Context, accountID int64) error {
	if b == nil || b.rdb == nil || accountID <= 0 {
		return nil
	}
	rCtx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	if err := b.rdb.Del(rCtx, b.key(accountID)).Err(); err != nil {
		b.errorCount.Add(1)
		return err
	}
	b.clearCount.Add(1)
	return nil
}

func (b *RedisRuntimeBlocker) IsBlocked(ctx context.Context, accountID int64, now time.Time) (RuntimeBlock, bool) {
	if b == nil || b.rdb == nil || accountID <= 0 {
		return RuntimeBlock{}, false
	}
	if now.IsZero() {
		now = time.Now()
	}
	rCtx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	val, err := b.rdb.Get(rCtx, b.key(accountID)).Result()
	if err != nil {
		if err != redis.Nil {
			// Fail open: a Redis error must not make the account unschedulable.
			b.errorCount.Add(1)
		}
		return RuntimeBlock{}, false
	}
	until, reason := decodeRuntimeBlockValue(val)
	if until.IsZero() || !until.After(now) {
		return RuntimeBlock{}, false
	}
	b.hitCount.Add(1)
	return RuntimeBlock{AccountID: accountID, Until: until, Reason: reason}, true
}

func (b *RedisRuntimeBlocker) Metrics() RuntimeBlockerMetrics {
	if b == nil {
		return RuntimeBlockerMetrics{}
	}
	// Expired and ActiveSize are intentionally left zero: expiry is delegated to
	// Redis TTL, and an accurate cross-replica active count would require a SCAN
	// on the hot path. The counters below remain per-replica observability.
	return RuntimeBlockerMetrics{
		Blocks: b.blockCount.Load(),
		Hits:   b.hitCount.Load(),
		Clears: b.clearCount.Load(),
	}
}

// runtimeBlockScanCount bounds each SCAN batch; large enough to keep round
// trips low, small enough to avoid blocking Redis on a big keyspace.
const runtimeBlockScanCount = 512

// ActiveCount scans Redis for currently-live block keys. It is intended for
// low-frequency metric reporting, NOT the request hot path: SCAN is O(keyspace)
// and must never gate account selection. Returns the count observed so far even
// on error, so a partial scan still yields a usable lower bound.
func (b *RedisRuntimeBlocker) ActiveCount(ctx context.Context) (int64, error) {
	if b == nil || b.rdb == nil {
		return 0, nil
	}
	match := b.keyPrefix + "*"
	var (
		cursor uint64
		count  int64
	)
	for {
		keys, next, err := b.rdb.Scan(ctx, cursor, match, runtimeBlockScanCount).Result()
		if err != nil {
			b.errorCount.Add(1)
			return count, err
		}
		count += int64(len(keys))
		cursor = next
		if cursor == 0 {
			return count, nil
		}
	}
}

// StartActiveGaugeReporter launches a background goroutine that periodically
// scans Redis for live blocks and publishes the count via set (e.g. a Prometheus
// gauge). Passing the sink as a callback keeps this package decoupled from the
// metrics registry. Returns a stop function that halts the goroutine and waits
// for it to exit; safe to call on a nil blocker (no-op).
func (b *RedisRuntimeBlocker) StartActiveGaugeReporter(interval time.Duration, set func(float64)) func() {
	if b == nil || b.rdb == nil || set == nil {
		return func() {}
	}
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		b.reportActive(ctx, set) // publish promptly on startup
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				b.reportActive(ctx, set)
			}
		}
	}()
	return func() {
		cancel()
		wg.Wait()
	}
}

func (b *RedisRuntimeBlocker) reportActive(ctx context.Context, set func(float64)) {
	rCtx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	n, err := b.ActiveCount(rCtx)
	if err != nil {
		return // skip this tick; a transient Redis error must not zero the gauge
	}
	set(float64(n))
}

// encodeRuntimeBlockValue packs the block deadline and reason into one value so
// IsBlocked reconstructs the RuntimeBlock in a single round trip. Format:
// "<untilUnixMilli>|<reason>".
func encodeRuntimeBlockValue(until time.Time, reason string) string {
	return strconv.FormatInt(until.UnixMilli(), 10) + "|" + reason
}

func decodeRuntimeBlockValue(val string) (time.Time, string) {
	head, reason, found := strings.Cut(val, "|")
	if !found {
		return time.Time{}, val
	}
	ms, err := strconv.ParseInt(head, 10, 64)
	if err != nil {
		return time.Time{}, reason
	}
	return time.UnixMilli(ms), reason
}

var _ RuntimeBlocker = (*RedisRuntimeBlocker)(nil)
