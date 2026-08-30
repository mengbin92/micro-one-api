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
