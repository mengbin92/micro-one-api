package biz

import (
	"context"
	"sync"
	"testing"
	"time"

	commonv1 "micro-one-api/api/common/v1"
)

func TestSlidingWindow_AddAndP95(t *testing.T) {
	w := NewSlidingWindow(100)
	for i := int64(0); i < 100; i++ {
		w.Add(i)
	}
	p95 := w.P95()
	// 95th percentile of [0..99] should be ~94.
	if p95 != 94 {
		t.Fatalf("P95 = %v, want 94", p95)
	}
}

func TestSlidingWindow_RingBufferNoGrowth(t *testing.T) {
	w := NewSlidingWindow(10)
	for i := int64(0); i < 1000; i++ {
		w.Add(i)
	}
	w.mu.Lock()
	n := len(w.values)
	w.mu.Unlock()
	if n != 10 {
		t.Fatalf("ring buffer grew unbounded: len=%d, want 10", n)
	}
	// Last 10 values added were [990..999]; P95 should be in that range.
	p95 := w.P95()
	if p95 < 990 || p95 > 999 {
		t.Fatalf("P95 = %v, want within [990,999]", p95)
	}
}

func TestSlidingWindow_Empty(t *testing.T) {
	w := NewSlidingWindow(10)
	if got := w.P95(); got != 0 {
		t.Fatalf("P95 on empty = %v, want 0", got)
	}
}

func TestSlidingWindow_DefaultSize(t *testing.T) {
	w := NewSlidingWindow(0)
	for i := int64(0); i < 200; i++ {
		w.Add(i)
	}
	w.mu.Lock()
	n := len(w.values)
	w.mu.Unlock()
	if n != 100 {
		t.Fatalf("default cap = %d, want 100", n)
	}
}

func TestSlidingCounter_RateAndIncrement(t *testing.T) {
	c := NewSlidingCounter(60 * time.Second)
	for i := 0; i < 10; i++ {
		c.Increment()
	}
	// 10 errors in the same second bucket over a 60s window.
	rate := c.Rate()
	if rate < 0.16 || rate > 0.17 {
		t.Fatalf("Rate = %v, want ~0.167", rate)
	}
}

func TestSlidingCounter_Empty(t *testing.T) {
	c := NewSlidingCounter(60 * time.Second)
	if got := c.Rate(); got != 0 {
		t.Fatalf("Rate on empty = %v, want 0", got)
	}
}

func TestSlidingCounter_Cleanup(t *testing.T) {
	c := NewSlidingCounter(2 * time.Second)
	// Manually inject old buckets.
	c.mu.Lock()
	c.counts[time.Now().Unix()-100] = 5
	c.counts[time.Now().Unix()-200] = 5
	c.mu.Unlock()
	if got := c.Rate(); got != 0 {
		t.Fatalf("Rate after cleanup of old buckets = %v, want 0", got)
	}
	c.mu.Lock()
	remaining := len(c.counts)
	c.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("cleanup left %d stale buckets", remaining)
	}
}

func TestWeightedSelector_Empty(t *testing.T) {
	s := NewWeightedSelector()
	_, err := s.Select(context.Background(), "g", nil)
	if err != ErrChannelNotFound {
		t.Fatalf("err = %v, want ErrChannelNotFound", err)
	}
}

func TestWeightedSelector_SingleCandidate(t *testing.T) {
	s := NewWeightedSelector()
	ch := &commonv1.ChannelInfo{Id: 1, Priority: 10}
	got, err := s.Select(context.Background(), "g", []*commonv1.ChannelInfo{ch})
	if err != nil {
		t.Fatalf("Select err = %v", err)
	}
	if got.Id != 1 {
		t.Fatalf("got.Id = %d, want 1", got.Id)
	}
}

func TestWeightedSelector_DistributionFavorsHigherWeight(t *testing.T) {
	s := NewWeightedSelector()
	high := &commonv1.ChannelInfo{Id: 1, Priority: 100}
	low := &commonv1.ChannelInfo{Id: 2, Priority: 1}
	candidates := []*commonv1.ChannelInfo{high, low}

	counts := map[int64]int{}
	const iterations = 1000
	for i := 0; i < iterations; i++ {
		// Reset currentWeight each iteration to isolate the dynamic-weight
		// comparison (we are not testing full smooth-WRR rotation here, only
		// that a higher-weight channel wins more often from a clean state).
		got, err := s.Select(context.Background(), "g", candidates)
		if err != nil {
			t.Fatalf("Select err = %v", err)
		}
		counts[got.Id]++
		// Reset selector state to start fresh each iteration.
		s.mu.Lock()
		for _, st := range s.channels {
			st.currentWeight = 0
			st.inflight.Store(0)
		}
		s.mu.Unlock()
	}

	if counts[1] <= counts[2] {
		t.Fatalf("higher-weight channel not favored: high=%d low=%d", counts[1], counts[2])
	}
}

func TestWeightedSelector_RecordHealthUpdatesInflight(t *testing.T) {
	s := NewWeightedSelector()
	ch := &commonv1.ChannelInfo{Id: 1, Priority: 10}
	_, _ = s.Select(context.Background(), "g", []*commonv1.ChannelInfo{ch})

	st, ok := s.GetState(1)
	if !ok {
		t.Fatal("expected state for channel 1")
	}
	if got := st.inflight.Load(); got != 1 {
		t.Fatalf("inflight after select = %d, want 1", got)
	}

	s.RecordHealth(1, true, int64(50*time.Millisecond), "")
	st, _ = s.GetState(1)
	if got := st.inflight.Load(); got != 0 {
		t.Fatalf("inflight after RecordHealth = %d, want 0", got)
	}
}

func TestWeightedSelector_ConcurrentSelect(t *testing.T) {
	s := NewWeightedSelector()
	ch := &commonv1.ChannelInfo{Id: 1, Priority: 10}
	candidates := []*commonv1.ChannelInfo{ch}

	var wg sync.WaitGroup
	errs := make(chan error, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Select(context.Background(), "g", candidates)
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent Select err = %v", err)
	}
}
