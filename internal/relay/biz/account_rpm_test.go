package biz

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestAccountRPMLimiter_UnlimitedWhenLimitNonPositive(t *testing.T) {
	l := NewAccountRPMLimiter()
	for i := 0; i < 100; i++ {
		if !l.TryAcquire(context.Background(), 7, 0) {
			t.Fatalf("limit=0 must be unlimited, acquire %d failed", i)
		}
	}
}

func TestAccountRPMLimiter_EnforcesRollingMinute(t *testing.T) {
	l := NewAccountRPMLimiter()
	now := time.Unix(1710000000, 0)
	l.now = func() time.Time { return now }

	if !l.TryAcquire(context.Background(), 42, 2) {
		t.Fatal("first request should pass")
	}
	if !l.TryAcquire(context.Background(), 42, 2) {
		t.Fatal("second request should pass")
	}
	if l.TryAcquire(context.Background(), 42, 2) {
		t.Fatal("third request in the same minute should be blocked")
	}
	now = now.Add(61 * time.Second)
	if !l.TryAcquire(context.Background(), 42, 2) {
		t.Fatal("window expiry should restore capacity")
	}
}

func TestAccountRPMLimiter_Concurrent(t *testing.T) {
	l := NewAccountRPMLimiter()
	const id, limit = int64(1), int32(4)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var granted int32
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if l.TryAcquire(context.Background(), id, limit) {
				mu.Lock()
				granted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if granted != limit {
		t.Fatalf("granted=%d, want %d", granted, limit)
	}
}

type fakeAccountRPMRedis struct {
	mu      sync.Mutex
	events  map[string]map[string]int64
	evalErr error
}

func newFakeAccountRPMRedis() *fakeAccountRPMRedis {
	return &fakeAccountRPMRedis{events: make(map[string]map[string]int64)}
}

func (f *fakeAccountRPMRedis) Eval(_ context.Context, _ string, keys []string, args ...any) *redis.Cmd {
	if f.evalErr != nil {
		return redis.NewCmdResult(int64(0), f.evalErr)
	}
	key := keys[0]
	cutoff := toInt64(args[0])
	limit := toInt64(args[1])
	now := toInt64(args[2])
	member, _ := args[3].(string)

	f.mu.Lock()
	defer f.mu.Unlock()
	accountEvents := f.events[key]
	if accountEvents == nil {
		accountEvents = make(map[string]int64)
		f.events[key] = accountEvents
	}
	for m, ts := range accountEvents {
		if ts <= cutoff {
			delete(accountEvents, m)
		}
	}
	if int64(len(accountEvents)) >= limit {
		return redis.NewCmdResult(int64(0), nil)
	}
	accountEvents[member] = now
	return redis.NewCmdResult(int64(1), nil)
}

func TestRedisAccountRPMLimiter_SharedAcrossInstances(t *testing.T) {
	store := newFakeAccountRPMRedis()
	replicaA := newRedisAccountRPMLimiter(store, "a")
	replicaB := newRedisAccountRPMLimiter(store, "b")

	if !replicaA.TryAcquire(context.Background(), 42, 1) {
		t.Fatal("first replica should acquire the only rpm slot")
	}
	if replicaB.TryAcquire(context.Background(), 42, 1) {
		t.Fatal("second replica must observe the shared Redis rpm limit")
	}
}

func TestRedisUserRPMLimiter_SeparateKeyspaceFromAccountRPM(t *testing.T) {
	store := newFakeAccountRPMRedis()
	accountLimiter := newRedisRPMLimiter(store, accountRPMKeyPrefix, "account")
	userLimiter := newRedisRPMLimiter(store, userRPMKeyPrefix, "user")

	if !accountLimiter.TryAcquire(context.Background(), 42, 1) {
		t.Fatal("account rpm acquire should succeed")
	}
	if !userLimiter.TryAcquire(context.Background(), 42, 1) {
		t.Fatal("user rpm with the same numeric id must use a separate keyspace")
	}
}

func TestRedisAccountRPMLimiter_FallsBackToMemoryOnAcquireError(t *testing.T) {
	store := newFakeAccountRPMRedis()
	store.evalErr = errors.New("redis unavailable")
	limiter := newRedisAccountRPMLimiter(store, "a")

	if !limiter.TryAcquire(context.Background(), 7, 1) {
		t.Fatal("first fallback memory acquire should succeed")
	}
	if limiter.TryAcquire(context.Background(), 7, 1) {
		t.Fatal("fallback memory limiter should still enforce the account rpm limit")
	}
}

func TestNewRedisAccountRPMLimiter_NilClient(t *testing.T) {
	if l := NewRedisAccountRPMLimiter(nil); l != nil {
		t.Fatalf("NewRedisAccountRPMLimiter(nil) = %v, want nil", l)
	}
}
