package middleware

import (
	"testing"
	"time"
)

func TestRateLimiterTokenBucketBurstAndRefill(t *testing.T) {
	limiter := NewRateLimiter(&RateLimitConfig{
		RequestsPerSecond: 10,
		Burst:             3,
		Window:            time.Second,
		MaxClients:        10,
	})
	for i := range 3 {
		allowed, _ := limiter.Allow("client")
		if !allowed {
			t.Fatalf("burst request %d was rejected", i+1)
		}
	}
	if allowed, _ := limiter.Allow("client"); allowed {
		t.Fatal("request beyond burst was allowed without refill")
	}

	limiter.mutex.Lock()
	limiter.clients["client"].lastSeen = time.Now().Add(-110 * time.Millisecond)
	limiter.mutex.Unlock()
	if allowed, _ := limiter.Allow("client"); !allowed {
		t.Fatal("request was not allowed after one token refilled")
	}
	if allowed, _ := limiter.Allow("client"); allowed {
		t.Fatal("limiter refilled more than the sustained rate permits")
	}
}

func TestRateLimiterCapacityUsesSharedOverflowBudget(t *testing.T) {
	l := NewRateLimiter(&RateLimitConfig{RequestsPerSecond: 1, Burst: 1, MaxClients: 1})
	if ok, _ := l.Allow("stored"); !ok {
		t.Fatal("first client denied")
	}
	if ok, _ := l.Allow("new"); !ok {
		t.Fatal("new client permanently denied at capacity")
	}
	if ok, _ := l.Allow("another"); ok {
		t.Fatal("overflow identities bypass shared budget")
	}
	l.mutex.Lock()
	l.overflow.lastSeen = time.Now().Add(-time.Second)
	l.mutex.Unlock()
	if ok, _ := l.Allow("after-refill"); !ok {
		t.Fatal("overflow does not refill")
	}
	if len(l.clients) != 1 {
		t.Fatal("unbounded overflow map")
	}
}
