package biz

import (
	"sync"
	"testing"
)

func TestAccountConcurrencyLimiter_UnlimitedWhenLimitNonPositive(t *testing.T) {
	l := NewAccountConcurrencyLimiter()
	for i := 0; i < 100; i++ {
		if _, ok := l.TryAcquire(7, 0); !ok {
			t.Fatalf("limit=0 must be unlimited, acquire %d failed", i)
		}
	}
	if got := l.Inflight(7); got != 0 {
		t.Fatalf("unlimited acquisitions must not be counted, inflight=%d", got)
	}
}

func TestAccountConcurrencyLimiter_NilReceiverAndBadID(t *testing.T) {
	var l *AccountConcurrencyLimiter
	if _, ok := l.TryAcquire(1, 5); !ok {
		t.Fatal("nil limiter must grant")
	}
	l = NewAccountConcurrencyLimiter()
	if _, ok := l.TryAcquire(0, 5); !ok {
		t.Fatal("non-positive accountID must be treated as unlimited")
	}
}

func TestAccountConcurrencyLimiter_EnforcesLimitAndReleases(t *testing.T) {
	l := NewAccountConcurrencyLimiter()
	const id, limit = int64(42), int32(2)

	r1, ok1 := l.TryAcquire(id, limit)
	r2, ok2 := l.TryAcquire(id, limit)
	if !ok1 || !ok2 {
		t.Fatal("first two acquisitions within limit must succeed")
	}
	if got := l.Inflight(id); got != 2 {
		t.Fatalf("inflight=%d, want 2", got)
	}
	if _, ok := l.TryAcquire(id, limit); ok {
		t.Fatal("third acquisition at limit must fail")
	}

	// A different account is independent.
	if _, ok := l.TryAcquire(99, limit); !ok {
		t.Fatal("other account must have its own budget")
	}

	// Releasing frees a slot; release is idempotent.
	r1()
	r1() // double release must not underflow
	if got := l.Inflight(id); got != 1 {
		t.Fatalf("after release inflight=%d, want 1", got)
	}
	if _, ok := l.TryAcquire(id, limit); !ok {
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
			if release, ok := l.TryAcquire(id, limit); ok {
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
