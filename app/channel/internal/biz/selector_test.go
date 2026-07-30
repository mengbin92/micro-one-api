package biz

import (
	"context"
	"sync"
	"testing"
	"time"
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
	ch := &Channel{ID: 1, Priority: 10}
	got, err := s.Select(context.Background(), "g", []*Channel{ch})
	if err != nil {
		t.Fatalf("Select err = %v", err)
	}
	if got.ID != 1 {
		t.Fatalf("got.ID = %d, want 1", got.ID)
	}
}

func TestWeightedSelector_DistributionFavorsHigherWeight(t *testing.T) {
	s := NewWeightedSelector()
	high := &Channel{ID: 1, Priority: 100}
	low := &Channel{ID: 2, Priority: 1}
	candidates := []*Channel{high, low}

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
		counts[got.ID]++
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
	ch := &Channel{ID: 1, Priority: 10}
	_, _ = s.Select(context.Background(), "g", []*Channel{ch})

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
	ch := &Channel{ID: 1, Priority: 10}
	candidates := []*Channel{ch}

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

// TestWeightedSelector_DefaultWeightOneFailureNotStarved (P1 review #5):
// a default-weight (Priority=0 → weight=1) channel that takes a single
// failure must NOT be zeroed out of the selector. Pre-fix,
// effectiveWeight = 1 * 80 * 100 / 10000 = 0, so the channel never
// accumulated currentWeight again and was never selected — permanent
// starvation instead of the §12.2 "drop to 80% traffic" contract.
func TestWeightedSelector_DefaultWeightOneFailureNotStarved(t *testing.T) {
	s := NewWeightedSelector()
	ch := &Channel{ID: 1, Priority: 0} // weight defaults to 1
	// Prime the selector state.
	if _, err := s.Select(context.Background(), "g", []*Channel{ch}); err != nil {
		t.Fatalf("prime Select err = %v", err)
	}
	// Record one failure — healthFactor drops to 80 (<5% band). Without the
	// floor, effectiveWeight becomes 0 and the channel is starved forever.
	s.RecordHealth(1, false, int64(50*time.Millisecond), "502")

	// Reset currentWeight so the only signal is effectiveWeight.
	s.mu.Lock()
	for _, st := range s.channels {
		st.currentWeight = 0
	}
	s.mu.Unlock()

	// The channel must still be selectable (effectiveWeight floored to 1).
	got, err := s.Select(context.Background(), "g", []*Channel{ch})
	if err != nil {
		t.Fatalf("Select after one failure err = %v", err)
	}
	if got.ID != 1 {
		t.Fatalf("default-weight channel starved after one failure: got.ID = %d, want 1", got.ID)
	}
}

func TestWeightedSelector_PreservesHealthWeightRatio(t *testing.T) {
	s := NewWeightedSelector()
	healthy := &Channel{ID: 1, Priority: 0}
	degraded := &Channel{ID: 2, Priority: 0}
	candidates := []*Channel{healthy, degraded}

	selected, err := s.Select(context.Background(), "g", candidates)
	if err != nil {
		t.Fatalf("prime Select err = %v", err)
	}
	s.RecordHealth(selected.ID, true, int64(50*time.Millisecond), "")
	for i := 0; i < 6; i++ {
		s.RecordHealth(degraded.ID, false, int64(50*time.Millisecond), "502")
	}
	if got := s.channels[degraded.ID].healthFactor(); got != 20 {
		t.Fatalf("degraded health factor = %d, want 20", got)
	}

	counts := map[int64]int{}
	for i := 0; i < 600; i++ {
		selected, err = s.Select(context.Background(), "g", candidates)
		if err != nil {
			t.Fatalf("Select err = %v", err)
		}
		counts[selected.ID]++
		s.RecordHealth(selected.ID, true, int64(50*time.Millisecond), "")
	}
	// The selector preserves existing smooth-WRR credit when health changes, so
	// the transition window may differ by one selection from the steady-state
	// 5:1 ratio. A rounded integer floor would instead collapse this to 1:1.
	if counts[healthy.ID] < 498 || counts[healthy.ID] > 502 ||
		counts[degraded.ID] < 98 || counts[degraded.ID] > 102 {
		t.Fatalf("selection distribution = %+v, want approximately 500:100", counts)
	}
}

func TestWeightedSelector_RefreshesConfiguredChannel(t *testing.T) {
	s := NewWeightedSelector()
	original := &Channel{ID: 7, Name: "old", Priority: 1}
	selected, err := s.Select(context.Background(), "g", []*Channel{original})
	if err != nil {
		t.Fatalf("initial Select err = %v", err)
	}
	s.RecordHealth(selected.ID, true, int64(10*time.Millisecond), "")

	updated := &Channel{ID: 7, Name: "new", Priority: 9}
	selected, err = s.Select(context.Background(), "g", []*Channel{updated})
	if err != nil {
		t.Fatalf("updated Select err = %v", err)
	}
	if selected != updated || selected.Name != "new" {
		t.Fatalf("selector returned stale channel snapshot: got %+v, want %+v", selected, updated)
	}
	if got := s.GetStats()[7].Weight; got != 9 {
		t.Fatalf("selector weight after channel update = %d, want 9", got)
	}
}

func TestWeightedSelector_ExcludesOpenCircuitFromTotalWeight(t *testing.T) {
	s := NewWeightedSelector()
	high := &Channel{ID: 1, Priority: 100}
	low := &Channel{ID: 2, Priority: 1}
	candidates := []*Channel{high, low}
	selected, err := s.Select(context.Background(), "g", candidates)
	if err != nil {
		t.Fatalf("prime Select err = %v", err)
	}
	s.RecordHealth(selected.ID, true, int64(10*time.Millisecond), "")

	s.mu.Lock()
	for _, st := range s.channels {
		st.currentWeight = 0
		st.inflight.Store(0)
	}
	s.channels[high.ID].circuitOpenUntil = time.Now().Add(time.Minute).UnixNano()
	s.mu.Unlock()

	for i := 0; i < 5; i++ {
		selected, err = s.Select(context.Background(), "g", candidates)
		if err != nil {
			t.Fatalf("Select err = %v", err)
		}
		if selected.ID != low.ID {
			t.Fatalf("selected circuit-open channel %d, want %d", selected.ID, low.ID)
		}
		s.RecordHealth(selected.ID, true, int64(10*time.Millisecond), "")
	}
	if got := s.GetStats()[low.ID].CurrentWeight; got != 0 {
		t.Fatalf("eligible channel current weight = %d, want 0; open circuits must not contribute to total weight", got)
	}
}

// TestWeightedSelector_LoadFactorDeratesHighInflight proves the Phase D #12
// channel-side load-aware path: a channel with high in-flight load receives a
// lower effective weight than an idle sibling, while the hard cap still skips
// it only at maxConcurrent. Because the channel selector owns the full
// in-flight lifecycle in-process (Select increments, RecordHealth decrements),
// no cross-replica oracle is needed here.
func TestWeightedSelector_LoadFactorDeratesHighInflight(t *testing.T) {
	s := NewWeightedSelector()
	// Two equal-weight channels; pump channel 1 to 75% of its 100 cap by
	// selecting it without recording health (which would reset inflight).
	busy := &Channel{ID: 1, Priority: 10}
	idle := &Channel{ID: 2, Priority: 10}
	candidates := []*Channel{busy, idle}

	// Prime both states.
	_, _ = s.Select(context.Background(), "g", candidates)
	// Reset WRR state so the comparison is about effective weight only.
	s.mu.Lock()
	for _, st := range s.channels {
		st.currentWeight = 0
	}
	s.mu.Unlock()

	// Simulate 75 concurrent in-flight on channel 1.
	st1, _ := s.GetState(1)
	for i := 0; i < 75; i++ {
		st1.inflight.Add(1)
	}
	// 75/100 = 75% → [0.75,0.9) band → loadFactor 20.
	if got := st1.loadFactor(); got != 20 {
		t.Fatalf("busy loadFactor = %d, want 20", got)
	}
	st2, _ := s.GetState(2)
	if got := st2.loadFactor(); got != 100 {
		t.Fatalf("idle loadFactor = %d, want 100", got)
	}

	counts := map[int64]int{}
	for i := 0; i < 1000; i++ {
		got, err := s.Select(context.Background(), "g", candidates)
		if err != nil {
			t.Fatalf("Select err = %v", err)
		}
		counts[got.ID]++
		// Reset WRR state each iteration to isolate effective-weight comparison.
		s.mu.Lock()
		for _, st := range s.channels {
			st.currentWeight = 0
		}
		// keep the busy channel loaded; Select incremented its inflight, undo that.
		s.channels[1].inflight.Store(75)
		s.channels[2].inflight.Store(0)
		s.mu.Unlock()
	}
	// Idle channel (loadFactor 100) should win far more than busy (20).
	if counts[2] <= counts[1] {
		t.Fatalf("busy channel not derated: idle=%d busy=%d", counts[2], counts[1])
	}
}

// TestWeightedSelector_LoadFactorBands pins the channel-side relative bands.
func TestWeightedSelector_LoadFactorBands(t *testing.T) {
	s := NewWeightedSelector()
	ch := &Channel{ID: 1, Priority: 1}
	_, _ = s.Select(context.Background(), "g", []*Channel{ch})
	st, _ := s.GetState(1)

	for _, tc := range []struct {
		inflight int32
		want     int32
	}{
		{0, 100},  // idle
		{39, 100}, // <40%
		{40, 80},  // [40,60)%
		{59, 80},  // [40,60)%
		{60, 50},  // [60,75)%
		{74, 50},  // [60,75)%
		{75, 20},  // [75,90)%
		{89, 20},  // [75,90)%
		{90, 1},   // >=90%
		{100, 1},  // at cap
	} {
		st.inflight.Store(tc.inflight)
		if got := st.loadFactor(); got != tc.want {
			t.Fatalf("inflight=%d loadFactor=%d, want %d", tc.inflight, got, tc.want)
		}
	}
}
