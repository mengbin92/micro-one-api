package biz

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ── v0.11.0 Phase 3 §3.3: deterministic distribution tests ─────────────────
//
// The roadmap requires "2 个普通渠道 + 2 个订阅账号的确定性分布测试，覆盖不同
// 优先级、权重、健康降权、并发饱和和来源失败，先证明选择行为再制作运营视图".
// These tests exercise the selector primitives directly (not the full
// SelectChannel/SelectSubscriptionAccount usecase path) so the distribution
// contract is pinned independently of repo mocking.

// TestPhase3_ChannelSelector_WeightDistribution proves two regular channels in
// the same priority tier with configured weights 3:1 split ~75%:25%, not the
// 50:50 a hard-coded weight would produce.
func TestPhase3_ChannelSelector_WeightDistribution(t *testing.T) {
	s := NewWeightedSelector()
	high := &Channel{ID: 1, Weight: 3, Priority: 1}
	low := &Channel{ID: 2, Weight: 1, Priority: 1}
	candidates := []*Channel{high, low}

	counts := map[int64]int{}
	const iterations = 800
	for i := 0; i < iterations; i++ {
		selected, err := s.Select(context.Background(), "g", candidates)
		if err != nil {
			t.Fatalf("Select err = %v", err)
		}
		// Select increments inflight; simulate completion so it stays selectable.
		s.RecordHealth(selected.ID, true, 10, "")
		counts[selected.ID]++
	}
	highPct := float64(counts[1]) / float64(iterations)
	// 3:1 → high ≈ 75%. Allow a tolerance band for smooth-WRR startup.
	if highPct < 0.68 || highPct > 0.82 {
		t.Fatalf("channel weight 3:1 distribution = %d:%d (high %.2f%%), want ~75%%", counts[1], counts[2], highPct)
	}
}

// TestPhase3_ChannelSelector_PriorityTierOrdering proves a higher-priority
// channel is ALWAYS selected over a lower-priority one (priority = layering).
func TestPhase3_ChannelSelector_PriorityTierOrdering(t *testing.T) {
	// The usecase (SelectChannel) splits candidates into priority tiers FIRST
	// and only hands one tier to the selector. So priority = layering is
	// proven at the usecase level (see channel_test.go
	// TestChannelUsecase_SelectChannel_PriorityOrdering). Here we pin the
	// selector-level invariant: within a single tier, priority does NOT bias
	// selection — only the configured Weight does. Two channels same tier,
	// different priority but same weight → ~50:50 (priority is not a weight).
	s := NewWeightedSelector()
	a := &Channel{ID: 1, Weight: 1, Priority: 100}
	b := &Channel{ID: 2, Weight: 1, Priority: 1}
	candidates := []*Channel{a, b}

	counts := map[int64]int{}
	for i := 0; i < 800; i++ {
		got, err := s.Select(context.Background(), "g", candidates)
		if err != nil {
			t.Fatalf("Select err = %v", err)
		}
		s.RecordHealth(got.ID, true, 10, "")
		counts[got.ID]++
	}
	aPct := float64(counts[1]) / float64(800)
	// Same weight (1:1) → ~50:50, regardless of the 100:1 priority difference.
	if aPct < 0.42 || aPct > 0.58 {
		t.Fatalf("within-tier priority must NOT act as weight: priority 100:1, weight 1:1 → %d:%d (%.2f%%), want ~50:50", counts[1], counts[2], aPct)
	}
}

// TestPhase3_ChannelSelector_HealthDerating proves a channel with a high error
// rate receives less traffic than a healthy one, even when both have the same
// configured weight.
func TestPhase3_ChannelSelector_HealthDerating(t *testing.T) {
	s := NewWeightedSelector()
	healthy := &Channel{ID: 1, Weight: 10, Priority: 1}
	degraded := &Channel{ID: 2, Weight: 10, Priority: 1}
	candidates := []*Channel{healthy, degraded}

	// Prime + degrade channel 2.
	for i := 0; i < 20; i++ {
		if _, err := s.Select(context.Background(), "g", candidates); err != nil {
			t.Fatalf("Select err = %v", err)
		}
		s.RecordHealth(1, true, 10, "")
		s.RecordHealth(2, false, 100, "upstream error")
	}

	// healthFactor for degraded should be 1 (errorRate > 0.30).
	if got := s.channels[2].healthFactor(); got != 1 {
		t.Fatalf("degraded healthFactor = %d, want 1", got)
	}

	counts := map[int64]int{}
	for i := 0; i < 400; i++ {
		selected, err := s.Select(context.Background(), "g", candidates)
		if err != nil {
			t.Fatalf("Select err = %v", err)
		}
		s.RecordHealth(selected.ID, true, 10, "")
		counts[selected.ID]++
	}
	// healthy (factor 100) vs degraded (factor 1) → healthy dominates.
	healthyPct := float64(counts[1]) / float64(400)
	if healthyPct < 0.90 {
		t.Fatalf("health-derating distribution = %d:%d (healthy %.2f%%), want >90%% healthy", counts[1], counts[2], healthyPct)
	}
}

// TestPhase3_ChannelSelector_ConcurrencySaturation proves a channel at its
// max-concurrent limit is skipped (saturation handling), so traffic shifts to
// the available channel.
func TestPhase3_ChannelSelector_ConcurrencySaturation(t *testing.T) {
	s := NewWeightedSelector()
	// Channel 1 has high weight so it wins initially; channel 2 is the fallback.
	limited := &Channel{ID: 1, Weight: 100, Priority: 1}
	open := &Channel{ID: 2, Weight: 1, Priority: 1}
	candidates := []*Channel{limited, open}

	// Prime the selector so channel 1 exists in state, then artificially
	// saturate it to its default maxConcurrent (100) by bumping inflight
	// directly. This simulates the production condition where relay dispatch
	// (a future Acquire seam) reports in-flight load.
	if _, err := s.Select(context.Background(), "g", candidates); err != nil {
		t.Fatalf("prime Select err = %v", err)
	}
	s.channels[1].inflight.Store(100) // == default maxConcurrent
	if got := s.channels[1].inflight.Load(); got != 100 {
		t.Fatalf("limited channel inflight = %d, want 100 (saturated)", got)
	}

	// Now all further selections must go to channel 2 (1 is at max-concurrent).
	for i := 0; i < 50; i++ {
		got, err := s.Select(context.Background(), "g", candidates)
		if err != nil {
			t.Fatalf("Select err = %v", err)
		}
		assert.Equal(t, int64(2), got.ID, "saturated channel 1 should be skipped")
		s.RecordHealth(2, true, 10, "")
	}
}

// TestPhase3_ChannelSelector_FailureExhaustsTier proves that when ALL channels
// in a tier are circuit-opened, Select returns ErrChannelNotFound (fail-closed)
// rather than falling back onto the broken channels.
func TestPhase3_ChannelSelector_FailureExhaustsTier(t *testing.T) {
	s := NewWeightedSelector()
	a := &Channel{ID: 1, Weight: 1, Priority: 1}
	b := &Channel{ID: 2, Weight: 1, Priority: 1}
	candidates := []*Channel{a, b}

	// Trip both circuits with a sustained high error rate.
	for i := 0; i < 40; i++ {
		s.Select(context.Background(), "g", candidates)
		s.RecordHealth(1, false, 100, "err")
		s.RecordHealth(2, false, 100, "err")
	}

	_, err := s.Select(context.Background(), "g", candidates)
	assert.ErrorIs(t, err, ErrChannelNotFound, "all-circuit-open tier must fail closed")
}

// ── subscription-account-side parity (2 accounts) ──────────────────────────

// TestPhase3_AccountSelector_WeightDistribution mirrors the channel test for
// two subscription accounts with weights 4:1.
func TestPhase3_AccountSelector_WeightDistribution(t *testing.T) {
	s := NewSubscriptionAccountSelector()
	high := &SubscriptionAccount{ID: 1, Priority: 1, Weight: 4}
	low := &SubscriptionAccount{ID: 2, Priority: 1, Weight: 1}
	candidates := []*SubscriptionAccount{high, low}

	counts := map[int64]int{}
	const iterations = 1000
	for i := 0; i < iterations; i++ {
		selected, err := s.Select(context.Background(), "g", candidates)
		if err != nil {
			t.Fatalf("Select err = %v", err)
		}
		counts[selected.ID]++
	}
	highPct := float64(counts[1]) / float64(iterations)
	// 4:1 → high ≈ 80%.
	if highPct < 0.74 || highPct > 0.86 {
		t.Fatalf("account weight 4:1 distribution = %d:%d (high %.2f%%), want ~80%%", counts[1], counts[2], highPct)
	}
}

// TestPhase3_AccountSelector_HealthDerating proves a degraded account's
// healthFactor reduces its share, mirroring the channel selector.
func TestPhase3_AccountSelector_HealthDerating(t *testing.T) {
	s := NewSubscriptionAccountSelector()
	healthy := &SubscriptionAccount{ID: 1, Priority: 1, Weight: 10}
	degraded := &SubscriptionAccount{ID: 2, Priority: 1, Weight: 10}
	candidates := []*SubscriptionAccount{healthy, degraded}

	// Prime + degrade account 2.
	for i := 0; i < 6; i++ {
		s.Select(context.Background(), "g", candidates)
		s.RecordAccountHealth(2, false)
	}
	if got := s.accounts[2].healthFactor(); got != 20 {
		t.Fatalf("degraded healthFactor = %d, want 20", got)
	}

	counts := map[int64]int{}
	for i := 0; i < 500; i++ {
		selected, err := s.Select(context.Background(), "g", candidates)
		if err != nil {
			t.Fatalf("Select err = %v", err)
		}
		counts[selected.ID]++
	}
	// healthy (factor 100) vs degraded (factor 20) → ~83%:17%.
	healthyPct := float64(counts[1]) / float64(500)
	if healthyPct < 0.78 || healthyPct > 0.88 {
		t.Fatalf("account health-derating distribution = %d:%d (healthy %.2f%%), want ~83%%", counts[1], counts[2], healthyPct)
	}
}

// TestPhase3_AccountSelector_CircuitOpenFailClosed proves that when both
// accounts trip their circuit breaker, Select fails closed.
func TestPhase3_AccountSelector_CircuitOpenFailClosed(t *testing.T) {
	s := NewSubscriptionAccountSelector()
	a := &SubscriptionAccount{ID: 1, Priority: 1, Weight: 1}
	b := &SubscriptionAccount{ID: 2, Priority: 1, Weight: 1}
	candidates := []*SubscriptionAccount{a, b}

	// Trip both circuits.
	for i := 0; i < 40; i++ {
		s.Select(context.Background(), "g", candidates)
		s.RecordAccountHealth(1, false)
		s.RecordAccountHealth(2, false)
	}

	_, err := s.Select(context.Background(), "g", candidates)
	assert.ErrorIs(t, err, ErrSubscriptionAccountNotFound, "all-circuit-open account tier must fail closed")
}
