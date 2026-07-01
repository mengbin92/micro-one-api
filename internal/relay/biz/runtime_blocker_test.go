package biz

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// fakeRuntimeRedis is a minimal in-memory stand-in for *redis.Client. It is a
// dumb key/value store: it captures the TTL passed to Set (so tests can assert
// a positive expiry is handed to Redis) but does not expire entries itself. The
// authoritative deadline check lives in RedisRuntimeBlocker.IsBlocked
// (until.After(now)), which the tests drive via the now argument. getErr forces
// read failures to exercise the fail-open path.
type fakeRuntimeRedis struct {
	mu      sync.Mutex
	data    map[string]string
	lastTTL time.Duration
	getErr  error
}

func newFakeRuntimeRedis() *fakeRuntimeRedis {
	return &fakeRuntimeRedis{data: make(map[string]string)}
}

func (f *fakeRuntimeRedis) Set(_ context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, _ := value.(string)
	f.data[key] = s
	f.lastTTL = expiration
	return redis.NewStatusResult("OK", nil)
}

func (f *fakeRuntimeRedis) Get(_ context.Context, key string) *redis.StringCmd {
	if f.getErr != nil {
		return redis.NewStringResult("", f.getErr)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.data[key]
	if !ok {
		return redis.NewStringResult("", redis.Nil)
	}
	return redis.NewStringResult(v, nil)
}

func (f *fakeRuntimeRedis) Del(_ context.Context, keys ...string) *redis.IntCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, k := range keys {
		if _, ok := f.data[k]; ok {
			delete(f.data, k)
			n++
		}
	}
	return redis.NewIntResult(n, nil)
}

// Scan returns all keys matching the prefix in a single batch (cursor 0). Good
// enough for tests; the production client paginates via the real cursor.
func (f *fakeRuntimeRedis) Scan(_ context.Context, _ uint64, match string, _ int64) *redis.ScanCmd {
	if f.getErr != nil {
		return redis.NewScanCmdResult(nil, 0, f.getErr)
	}
	prefix := strings.TrimSuffix(match, "*")
	f.mu.Lock()
	defer f.mu.Unlock()
	var keys []string
	for k := range f.data {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	return redis.NewScanCmdResult(keys, 0, nil)
}

func TestRedisRuntimeBlocker_BlockAndIsBlocked(t *testing.T) {
	base := time.Now()
	fake := newFakeRuntimeRedis()
	b := newRedisRuntimeBlocker(fake)

	until := base.Add(5 * time.Second)
	if err := b.Block(context.Background(), 12, until, "status=429"); err != nil {
		t.Fatalf("Block: %v", err)
	}
	if fake.lastTTL <= 0 || fake.lastTTL > 5*time.Second {
		t.Fatalf("Redis TTL = %v, want (0, 5s]", fake.lastTTL)
	}

	block, ok := b.IsBlocked(context.Background(), 12, base.Add(time.Second))
	if !ok {
		t.Fatalf("account should be blocked")
	}
	if block.AccountID != 12 || block.Reason != "status=429" {
		t.Fatalf("unexpected block: %+v", block)
	}
	// UnixMilli round-trips at millisecond precision, so allow a small delta.
	if delta := block.Until.Sub(until); delta > time.Millisecond || delta < -time.Millisecond {
		t.Fatalf("until = %v, want ~%v", block.Until, until)
	}
	if m := b.Metrics(); m.Blocks != 1 || m.Hits != 1 {
		t.Fatalf("metrics = %+v, want Blocks=1 Hits=1", m)
	}
}

func TestRedisRuntimeBlocker_ExpiresAtDeadline(t *testing.T) {
	base := time.Now()
	fake := newFakeRuntimeRedis()
	b := newRedisRuntimeBlocker(fake)

	until := base.Add(5 * time.Second)
	if err := b.Block(context.Background(), 7, until, "status=500"); err != nil {
		t.Fatalf("Block: %v", err)
	}
	// Once the caller's clock is past the block deadline, IsBlocked reports
	// not-blocked even though the (dumb) store still holds the key — the Redis
	// TTL would have removed it in production.
	if _, ok := b.IsBlocked(context.Background(), 7, base.Add(6*time.Second)); ok {
		t.Fatalf("block should be expired at deadline")
	}
}

func TestRedisRuntimeBlocker_BlockPastDeadlineIsNoop(t *testing.T) {
	base := time.Now()
	fake := newFakeRuntimeRedis()
	b := newRedisRuntimeBlocker(fake)

	// until already in the past -> non-positive TTL -> nothing stored.
	if err := b.Block(context.Background(), 5, base.Add(-time.Second), "stale"); err != nil {
		t.Fatalf("Block: %v", err)
	}
	if _, ok := b.IsBlocked(context.Background(), 5, base); ok {
		t.Fatalf("no block should exist for past deadline")
	}
	if m := b.Metrics(); m.Blocks != 0 {
		t.Fatalf("metrics = %+v, want Blocks=0", m)
	}
}

func TestRedisRuntimeBlocker_Clear(t *testing.T) {
	base := time.Now()
	fake := newFakeRuntimeRedis()
	b := newRedisRuntimeBlocker(fake)

	_ = b.Block(context.Background(), 3, base.Add(2*time.Minute), "status=401")
	if err := b.Clear(context.Background(), 3); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, ok := b.IsBlocked(context.Background(), 3, base.Add(time.Second)); ok {
		t.Fatalf("block should be cleared")
	}
	if m := b.Metrics(); m.Clears != 1 {
		t.Fatalf("metrics = %+v, want Clears=1", m)
	}
}

func TestRedisRuntimeBlocker_FailsOpenOnReadError(t *testing.T) {
	base := time.Now()
	fake := newFakeRuntimeRedis()
	b := newRedisRuntimeBlocker(fake)
	_ = b.Block(context.Background(), 9, base.Add(time.Minute), "status=429")

	// A Redis read error must report not-blocked so an outage does not make
	// every account unschedulable.
	fake.getErr = errors.New("redis unavailable")
	if _, ok := b.IsBlocked(context.Background(), 9, base); ok {
		t.Fatalf("IsBlocked must fail open on read error")
	}
	if m := b.Metrics(); m.Hits != 0 {
		t.Fatalf("metrics = %+v, want Hits=0", m)
	}
}

func TestRedisRuntimeBlocker_BlockShareableAcrossInstances(t *testing.T) {
	// Two blockers over one store model two relay replicas: a block written by
	// one replica is visible to the other.
	base := time.Now()
	fake := newFakeRuntimeRedis()
	replicaA := newRedisRuntimeBlocker(fake)
	replicaB := newRedisRuntimeBlocker(fake)

	if err := replicaA.Block(context.Background(), 42, base.Add(5*time.Second), "status=429"); err != nil {
		t.Fatalf("Block: %v", err)
	}
	if _, ok := replicaB.IsBlocked(context.Background(), 42, base.Add(time.Second)); !ok {
		t.Fatalf("replica B should observe replica A's block")
	}
}

func TestNewRedisRuntimeBlocker_NilClient(t *testing.T) {
	if b := NewRedisRuntimeBlocker(nil); b != nil {
		t.Fatalf("NewRedisRuntimeBlocker(nil) = %v, want nil", b)
	}
}

func TestRedisRuntimeBlocker_ActiveCount(t *testing.T) {
	base := time.Now()
	fake := newFakeRuntimeRedis()
	b := newRedisRuntimeBlocker(fake)

	if n, err := b.ActiveCount(context.Background()); err != nil || n != 0 {
		t.Fatalf("ActiveCount empty = (%d,%v), want (0,nil)", n, err)
	}

	_ = b.Block(context.Background(), 1, base.Add(time.Minute), "a")
	_ = b.Block(context.Background(), 2, base.Add(time.Minute), "b")
	_ = b.Block(context.Background(), 3, base.Add(time.Minute), "c")
	_ = b.Clear(context.Background(), 2)

	n, err := b.ActiveCount(context.Background())
	if err != nil {
		t.Fatalf("ActiveCount: %v", err)
	}
	if n != 2 {
		t.Fatalf("ActiveCount = %d, want 2", n)
	}
}

func TestRedisRuntimeBlocker_ActiveCountScanError(t *testing.T) {
	fake := newFakeRuntimeRedis()
	fake.getErr = errors.New("redis down")
	b := newRedisRuntimeBlocker(fake)
	if _, err := b.ActiveCount(context.Background()); err == nil {
		t.Fatalf("ActiveCount should surface scan error")
	}
}

func TestRedisRuntimeBlocker_StartActiveGaugeReporter(t *testing.T) {
	base := time.Now()
	fake := newFakeRuntimeRedis()
	b := newRedisRuntimeBlocker(fake)
	_ = b.Block(context.Background(), 1, base.Add(time.Minute), "a")
	_ = b.Block(context.Background(), 2, base.Add(time.Minute), "b")

	var (
		mu   sync.Mutex
		last float64
		hits int
	)
	stop := b.StartActiveGaugeReporter(5*time.Millisecond, func(v float64) {
		mu.Lock()
		last = v
		hits++
		mu.Unlock()
	})
	defer stop()

	// The reporter publishes once promptly on startup, then on each tick.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got, seen := last, hits
		mu.Unlock()
		if seen > 0 && got == 2 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("gauge reporter did not publish active count = 2 (last=%v)", last)
}

func TestStartActiveGaugeReporter_NilSafe(t *testing.T) {
	var b *RedisRuntimeBlocker
	stop := b.StartActiveGaugeReporter(time.Second, func(float64) {})
	stop() // must not panic
}

func TestDecodeRuntimeBlockValue(t *testing.T) {
	until := time.Unix(1_700_000_000, 0)
	got, reason := decodeRuntimeBlockValue(encodeRuntimeBlockValue(until, "status=429|extra"))
	if !got.Equal(until) {
		t.Fatalf("until = %v, want %v", got, until)
	}
	// Reason may itself contain '|'; only the first separator splits.
	if reason != "status=429|extra" {
		t.Fatalf("reason = %q", reason)
	}
	if _, r := decodeRuntimeBlockValue("no-separator"); !strings.Contains(r, "no-separator") {
		t.Fatalf("malformed value should return raw reason, got %q", r)
	}
}
