package biz

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestAccountConcurrencyLimiter_UnlimitedWhenLimitNonPositive(t *testing.T) {
	l := NewAccountConcurrencyLimiter()
	for i := 0; i < 100; i++ {
		if _, ok := l.TryAcquire(context.Background(), 7, 0); !ok {
			t.Fatalf("limit=0 must be unlimited, acquire %d failed", i)
		}
	}
	if got := l.Inflight(7); got != 0 {
		t.Fatalf("unlimited acquisitions must not be counted, inflight=%d", got)
	}
}

func TestAccountConcurrencyLimiter_NilReceiverAndBadID(t *testing.T) {
	var l *MemoryAccountConcurrencyLimiter
	if _, ok := l.TryAcquire(context.Background(), 1, 5); !ok {
		t.Fatal("nil limiter must grant")
	}
	l = NewAccountConcurrencyLimiter()
	if _, ok := l.TryAcquire(context.Background(), 0, 5); !ok {
		t.Fatal("non-positive accountID must be treated as unlimited")
	}
}

func TestAccountConcurrencyLimiter_EnforcesLimitAndReleases(t *testing.T) {
	l := NewAccountConcurrencyLimiter()
	const id, limit = int64(42), int32(2)

	r1, ok1 := l.TryAcquire(context.Background(), id, limit)
	r2, ok2 := l.TryAcquire(context.Background(), id, limit)
	if !ok1 || !ok2 {
		t.Fatal("first two acquisitions within limit must succeed")
	}
	if got := l.Inflight(id); got != 2 {
		t.Fatalf("inflight=%d, want 2", got)
	}
	if _, ok := l.TryAcquire(context.Background(), id, limit); ok {
		t.Fatal("third acquisition at limit must fail")
	}

	// A different account is independent.
	if _, ok := l.TryAcquire(context.Background(), 99, limit); !ok {
		t.Fatal("other account must have its own budget")
	}

	// Releasing frees a slot; release is idempotent.
	r1()
	r1() // double release must not underflow
	if got := l.Inflight(id); got != 1 {
		t.Fatalf("after release inflight=%d, want 1", got)
	}
	if _, ok := l.TryAcquire(context.Background(), id, limit); !ok {
		t.Fatal("slot must be reusable after release")
	}
	r2()
}

func TestAccountConcurrencyLimiter_Concurrent(t *testing.T) {
	l := NewAccountConcurrencyLimiter()
	const id, limit = int64(1), int32(4)
	var wg sync.WaitGroup
	var granted int32
	var mu sync.Mutex
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if release, ok := l.TryAcquire(context.Background(), id, limit); ok {
				mu.Lock()
				granted++
				mu.Unlock()
				release()
			}
		}()
	}
	wg.Wait()
	if got := l.Inflight(id); got != 0 {
		t.Fatalf("all slots must be released, inflight=%d", got)
	}
	if granted == 0 {
		t.Fatal("expected some acquisitions to be granted")
	}
}

type fakeAccountConcurrencyRedis struct {
	mu      sync.Mutex
	slots   map[string]map[string]int64
	evalErr error
	zErr    error
}

func newFakeAccountConcurrencyRedis() *fakeAccountConcurrencyRedis {
	return &fakeAccountConcurrencyRedis{slots: make(map[string]map[string]int64)}
}

func (f *fakeAccountConcurrencyRedis) Eval(_ context.Context, _ string, keys []string, args ...any) *redis.Cmd {
	if f.evalErr != nil {
		return redis.NewCmdResult(int64(0), f.evalErr)
	}
	key := keys[0]
	now := toInt64(args[0])
	limit := toInt64(args[1])
	deadline := toInt64(args[2])
	member, _ := args[3].(string)

	f.mu.Lock()
	defer f.mu.Unlock()
	accountSlots := f.slots[key]
	if accountSlots == nil {
		accountSlots = make(map[string]int64)
		f.slots[key] = accountSlots
	}
	for m, d := range accountSlots {
		if d <= now {
			delete(accountSlots, m)
		}
	}
	if int64(len(accountSlots)) >= limit {
		return redis.NewCmdResult(int64(0), nil)
	}
	accountSlots[member] = deadline
	return redis.NewCmdResult(int64(1), nil)
}

func (f *fakeAccountConcurrencyRedis) ZAdd(_ context.Context, key string, members ...redis.Z) *redis.IntCmd {
	if f.zErr != nil {
		return redis.NewIntResult(0, f.zErr)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	accountSlots := f.slots[key]
	if accountSlots == nil {
		accountSlots = make(map[string]int64)
		f.slots[key] = accountSlots
	}
	for _, z := range members {
		member, _ := z.Member.(string)
		accountSlots[member] = int64(z.Score)
	}
	return redis.NewIntResult(int64(len(members)), nil)
}

func (f *fakeAccountConcurrencyRedis) Expire(context.Context, string, time.Duration) *redis.BoolCmd {
	if f.zErr != nil {
		return redis.NewBoolResult(false, f.zErr)
	}
	return redis.NewBoolResult(true, nil)
}

func (f *fakeAccountConcurrencyRedis) ZRem(_ context.Context, key string, members ...any) *redis.IntCmd {
	if f.zErr != nil {
		return redis.NewIntResult(0, f.zErr)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, raw := range members {
		member, _ := raw.(string)
		if _, ok := f.slots[key][member]; ok {
			delete(f.slots[key], member)
			n++
		}
	}
	return redis.NewIntResult(n, nil)
}

func (f *fakeAccountConcurrencyRedis) ZCard(_ context.Context, key string) *redis.IntCmd {
	if f.zErr != nil {
		return redis.NewIntResult(0, f.zErr)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return redis.NewIntResult(int64(len(f.slots[key])), nil)
}

func toInt64(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int32:
		return int64(x)
	case int:
		return int64(x)
	default:
		return 0
	}
}

func TestRedisAccountConcurrencyLimiter_SharedAcrossInstances(t *testing.T) {
	store := newFakeAccountConcurrencyRedis()
	replicaA := newRedisAccountConcurrencyLimiter(store, "a")
	replicaB := newRedisAccountConcurrencyLimiter(store, "b")

	release, ok := replicaA.TryAcquire(context.Background(), 42, 1)
	if !ok {
		t.Fatal("first replica should acquire the only slot")
	}
	defer release()
	if _, ok := replicaB.TryAcquire(context.Background(), 42, 1); ok {
		t.Fatal("second replica must observe the shared Redis concurrency limit")
	}
	if got := replicaA.Inflight(context.Background(), 42); got != 1 {
		t.Fatalf("inflight=%d, want 1", got)
	}
	release()
	releaseB, ok := replicaB.TryAcquire(context.Background(), 42, 1)
	if !ok {
		t.Fatal("slot must be reusable after release")
	}
	releaseB()
}

func TestRedisAccountConcurrencyLimiter_FallsBackToMemoryOnAcquireError(t *testing.T) {
	store := newFakeAccountConcurrencyRedis()
	store.evalErr = errors.New("redis unavailable")
	limiter := newRedisAccountConcurrencyLimiter(store, "a")

	r1, ok1 := limiter.TryAcquire(context.Background(), 7, 1)
	if !ok1 {
		t.Fatal("first fallback memory acquire should succeed")
	}
	defer r1()
	if _, ok := limiter.TryAcquire(context.Background(), 7, 1); ok {
		t.Fatal("fallback memory limiter should still enforce the account limit")
	}
	if got := limiter.fallback.Inflight(7); got != 1 {
		t.Fatalf("fallback inflight=%d, want 1", got)
	}
}

func TestNewRedisAccountConcurrencyLimiter_NilClient(t *testing.T) {
	if l := NewRedisAccountConcurrencyLimiter(nil); l != nil {
		t.Fatalf("NewRedisAccountConcurrencyLimiter(nil) = %v, want nil", l)
	}
}
