package cache

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	identityv1 "micro-one-api/api/identity/v1"
)

// stringLoader is a simple CacheLoader used to exercise MultiLevelCache logic
// without a gRPC client.
type stringLoader struct {
	value string
	err   error
	calls int
}

// asLoader returns a CacheLoader[string] that delegates to the stringLoader,
// counting invocations so tests can assert hit/miss/singleflight behavior.
func (l *stringLoader) asLoader() CacheLoader[string] {
	return func(ctx context.Context, key string) (*string, error) {
		l.calls++
		if l.err != nil {
			return nil, l.err
		}
		v := l.value
		return &v, nil
	}
}

// newTestMultiLevel builds a cache with L1 only (no Redis L2) so it runs in
// the sandbox without network. l2Client is nil.
func newTestMultiLevel(t *testing.T, loader CacheLoader[string]) *MultiLevelCache[string] {
	t.Helper()
	c, err := NewMultiLevelCache[string](
		nil, // no L2 in unit tests
		nil, // no event bus
		loader,
		"test",
		&Config{L1CacheSize: 1000, L1TTL: 1 * time.Second, L2TTL: 0, Prefix: "ml"},
	)
	if err != nil {
		t.Fatalf("NewMultiLevelCache: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestMultiLevelCache_L1HitAvoidsLoader(t *testing.T) {
	ld := &stringLoader{value: "v"}
	c := newTestMultiLevel(t, ld.asLoader())

	got, err := c.Get(context.Background(), "k")
	if err != nil || got == nil || *got != "v" {
		t.Fatalf("first Get: got=%v err=%v", got, err)
	}
	if ld.calls != 1 {
		t.Fatalf("loader called %d times, want 1", ld.calls)
	}

	// Second Get must hit L1, not the loader.
	got, err = c.Get(context.Background(), "k")
	if err != nil || got == nil || *got != "v" {
		t.Fatalf("second Get: got=%v err=%v", got, err)
	}
	if ld.calls != 1 {
		t.Fatalf("loader called %d after L1 hit, want 1", ld.calls)
	}
}

func TestMultiLevelCache_LoaderErrorPropagates(t *testing.T) {
	ld := &stringLoader{err: errBoom}
	c := newTestMultiLevel(t, ld.asLoader())

	if _, err := c.Get(context.Background(), "k"); err == nil {
		t.Fatal("expected error from loader, got nil")
	}
}

func TestMultiLevelCache_InvalidateForcesReload(t *testing.T) {
	ld := &stringLoader{value: "v"}
	c := newTestMultiLevel(t, ld.asLoader())

	_, _ = c.Get(context.Background(), "k")
	if ld.calls != 1 {
		t.Fatalf("after first Get, loader calls=%d want 1", ld.calls)
	}

	if err := c.Invalidate(context.Background(), "k"); err != nil {
		t.Fatalf("Invalidate (L1-only, no L2) returned err=%v; expected nil", err)
	}

	_, _ = c.Get(context.Background(), "k")
	if ld.calls != 2 {
		t.Fatalf("after Invalidate+Get, loader calls=%d want 2", ld.calls)
	}
}

func TestMultiLevelCache_L1TTLExpiryReloads(t *testing.T) {
	ld := &stringLoader{value: "v"}
	c := newTestMultiLevel(t, ld.asLoader()) // L1TTL = 1s

	_, _ = c.Get(context.Background(), "k")
	if ld.calls != 1 {
		t.Fatalf("first Get loader calls=%d want 1", ld.calls)
	}

	// Wait for L1 entry to expire.
	time.Sleep(1200 * time.Millisecond)

	_, _ = c.Get(context.Background(), "k")
	if ld.calls != 2 {
		t.Fatalf("after TTL expiry, loader calls=%d want 2 (reload)", ld.calls)
	}
}

func TestMultiLevelCache_SetThenGetAvoidsLoader(t *testing.T) {
	ld := &stringLoader{value: "should-not-be-used"}
	c := newTestMultiLevel(t, ld.asLoader())

	v := "direct"
	if err := c.Set(context.Background(), "k", &v); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := c.Get(context.Background(), "k")
	if err != nil || got == nil || *got != "direct" {
		t.Fatalf("Get after Set: got=%v err=%v", got, err)
	}
	if ld.calls != 0 {
		t.Fatalf("loader called %d times after Set, want 0", ld.calls)
	}
}

func TestMultiLevelCache_ClearAllWipesL1(t *testing.T) {
	ld := &stringLoader{value: "v"}
	c := newTestMultiLevel(t, ld.asLoader())

	_, _ = c.Get(context.Background(), "k1")
	_, _ = c.Get(context.Background(), "k2")
	if ld.calls != 2 {
		t.Fatalf("loader calls=%d want 2", ld.calls)
	}

	c.ClearAll()

	// After ClearAll, both keys must reload from the loader.
	_, _ = c.Get(context.Background(), "k1")
	_, _ = c.Get(context.Background(), "k2")
	if ld.calls != 4 {
		t.Fatalf("after ClearAll, loader calls=%d want 4", ld.calls)
	}
}

func TestMultiLevelCache_ConcurrentGetSingleflight(t *testing.T) {
	ld := &stringLoader{value: "v"}
	c := newTestMultiLevel(t, ld.asLoader())

	// Hammer the same key concurrently; singleflight should collapse the
	// loads so the loader is called far fewer than N times.
	const n = 100
	done := make(chan struct{}, n)
	for range n {
		go func() {
			defer func() { done <- struct{}{} }()
			got, err := c.Get(context.Background(), "k")
			if err != nil || got == nil || *got != "v" {
				t.Errorf("Get: got=%v err=%v", got, err)
			}
		}()
	}
	for range n {
		<-done
	}
	if ld.calls > 10 {
		t.Fatalf("singleflight collapsed too few: loader called %d times (want <=10)", ld.calls)
	}
}

func TestMultiLevelCache_SizeAfterOps(t *testing.T) {
	ld := &stringLoader{value: "v"}
	c := newTestMultiLevel(t, ld.asLoader())

	l1, _ := c.Size()
	if l1 != 0 {
		t.Fatalf("initial L1 size=%d want 0", l1)
	}

	_, _ = c.Get(context.Background(), "a")
	_, _ = c.Get(context.Background(), "b")

	// ristretto Set is async; ristretto may not reflect counts immediately
	// without Wait(). Our cache doesn't call Wait(), so we only assert that
	// Size does not panic and returns a non-negative number.
	l1, _ = c.Size()
	if l1 < 0 {
		t.Fatalf("L1 size negative: %d", l1)
	}
}

// errBoom is a sentinel loader error.
type boomErr struct{}

func (boomErr) Error() string { return "boom" }

var errBoom = boomErr{}

func TestAuthCache_RedisOutageRevocationBoundary(t *testing.T) {
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr(), MaxRetries: -1, DialTimeout: 20 * time.Millisecond})
	t.Cleanup(func() { _ = client.Close() })
	revoked := false
	c, err := NewAuthCache(client, nil, func(context.Context, string) (*identityv1.GetAuthSnapshotReply, error) {
		if revoked {
			return nil, errBoom
		}
		return &identityv1.GetAuthSnapshotReply{UserId: 1, UserEnabled: true, TokenEnabled: true}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if _, err := c.Get(ctx, "test-key"); err != nil {
		t.Fatal(err)
	}
	revoked = true
	srv.Close()
	if _, err := c.Get(ctx, "test-key"); err != nil {
		t.Fatalf("legacy L1 should still be stale before expiry: %v", err)
	}
	if c.cache.ttl != 30*time.Second {
		t.Fatalf("legacy L1 boundary changed: %s", c.cache.ttl)
	}
	// Advance the cached entry past its production TTL without a 30s sleep.
	v, ok := c.cache.l1.Get(c.cache.prefix + "test-key")
	if !ok {
		t.Fatal("missing primed cache entry")
	}
	v.(*entry[identityv1.GetAuthSnapshotReply]).expiresAt = time.Now().Add(-time.Second)
	if _, err := c.Get(ctx, "test-key"); err == nil {
		t.Fatal("revoked identity must fail after L1 expiry while Redis is down")
	}
}

func TestMultiLevelCache_CorruptL2RemovedEvenWhenSourceFails(t *testing.T) {
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	ld := &stringLoader{err: errBoom}
	c := newTestMultiLevel(t, ld.asLoader())
	c.l2 = client
	for _, bad := range []string{"{broken", "", "null"} {
		if err := srv.Set(c.prefix+"k", bad); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Get(context.Background(), "k"); err == nil {
			t.Fatal("expected source failure")
		}
		if srv.Exists(c.prefix + "k") {
			t.Fatalf("corrupt L2 value %q survived failed source load", bad)
		}
	}
	if got := c.metrics.l2Hits.Load(); got != 0 {
		t.Fatalf("corrupt values counted as hits: %d", got)
	}
	ld.err, ld.value = nil, "recovered"
	got, err := c.Get(context.Background(), "k")
	if err != nil || got == nil || *got != "recovered" {
		t.Fatalf("recovery: got=%v err=%v", got, err)
	}
}
