package biz

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestSelectSubscriptionAccount_FailClosedOnAllCircuitOpen (🟡#6): when the
// accountSelector is configured and every candidate is circuit-opened,
// Select must NOT fall through to uniform random (which would fail-open onto
// the same tripped accounts). It returns ErrSubscriptionAccountNotFound so
// the caller fails closed until the circuit window elapses.
func TestSelectSubscriptionAccount_FailClosedOnAllCircuitOpen(t *testing.T) {
	sel := NewSubscriptionAccountSelector()
	// Trip the circuit on the only candidate by recording >0.5 err/s.
	// SlidingCounter window is 60s; record 35 failures fast to exceed the
	// 0.5 err/s threshold and trip the 30s circuit.
	for i := 0; i < 35; i++ {
		sel.RecordAccountHealth(42, false)
	}
	got, err := sel.Select(context.Background(), "default", []*SubscriptionAccount{
		{ID: 42, Priority: 1, Status: ChannelStatusEnabled},
	})
	if err == nil {
		t.Fatalf("expected fail-closed error when all candidates circuit-opened, got account %v", got)
	}
	if !errors.Is(err, ErrSubscriptionAccountNotFound) {
		t.Fatalf("expected ErrSubscriptionAccountNotFound, got %v", err)
	}
}

// TestSelectSubscriptionAccount_RandomWhenSelectorNil: when accountSelector
// is nil (not configured), the legacy uniform-random path is used. Guards
// against the 🟡#6 fix breaking deployments that never wired the selector.
func TestSelectSubscriptionAccount_RandomWhenSelectorNil(t *testing.T) {
	repo := &mockChannelRepo{
		accounts: map[int64]*SubscriptionAccount{
			1: {ID: 1, Platform: "codex", Status: ChannelStatusEnabled, Group: "default", Models: []string{"gpt-5"}, Priority: 1},
		},
		accAbilities: map[string][]SubscriptionAccountAbility{
			"codex:default:gpt-5": {{Group: "default", Model: "gpt-5", Platform: "codex", AccountID: 1, Enabled: true, Priority: 1}},
		},
	}
	uc := NewChannelUsecase(repo, nil)
	got, err := uc.SelectSubscriptionAccount(context.Background(), "default", "gpt-5", "codex", false)
	if err != nil {
		t.Fatalf("SelectSubscriptionAccount: %v", err)
	}
	if got == nil || got.ID != 1 {
		t.Fatalf("expected account 1, got %v", got)
	}
}

// TestSubscriptionAccountSelector_DefaultWeightOneFailureNotStarved (P1
// review #5): a default-weight (Priority=0 → weight=1) account that takes a
// single failure must NOT be zeroed out of the selector. Pre-fix,
// effectiveWeight = 1 * 80 * 100 / 10000 = 0, so the account never accumulated
// currentWeight again and was never selected — permanent starvation instead
// of the §12.2 "drop to 80% traffic" contract.
func TestSubscriptionAccountSelector_DefaultWeightOneFailureNotStarved(t *testing.T) {
	s := NewSubscriptionAccountSelector()
	acct := &SubscriptionAccount{ID: 1, Priority: 0} // weight defaults to 1
	// Prime the selector state.
	if _, err := s.Select(context.Background(), "g", []*SubscriptionAccount{acct}); err != nil {
		t.Fatalf("prime Select err = %v", err)
	}
	// One failure → healthFactor 80. Without the floor, effectiveWeight = 0.
	s.RecordAccountHealth(1, false)

	// Reset currentWeight so the only signal is effectiveWeight.
	s.mu.Lock()
	for _, st := range s.accounts {
		st.currentWeight = 0
	}
	s.mu.Unlock()

	got, err := s.Select(context.Background(), "g", []*SubscriptionAccount{acct})
	if err != nil {
		t.Fatalf("Select after one failure err = %v", err)
	}
	if got.ID != 1 {
		t.Fatalf("default-weight account starved after one failure: got.ID = %d, want 1", got.ID)
	}
}

func TestSubscriptionAccountSelector_PreservesHealthWeightRatio(t *testing.T) {
	s := NewSubscriptionAccountSelector()
	healthy := &SubscriptionAccount{ID: 1, Priority: 0}
	degraded := &SubscriptionAccount{ID: 2, Priority: 0}
	candidates := []*SubscriptionAccount{healthy, degraded}

	if _, err := s.Select(context.Background(), "g", candidates); err != nil {
		t.Fatalf("prime Select err = %v", err)
	}
	for i := 0; i < 6; i++ {
		s.RecordAccountHealth(degraded.ID, false)
	}
	if got := s.accounts[degraded.ID].healthFactor(); got != 20 {
		t.Fatalf("degraded health factor = %d, want 20", got)
	}

	counts := map[int64]int{}
	for i := 0; i < 600; i++ {
		selected, err := s.Select(context.Background(), "g", candidates)
		if err != nil {
			t.Fatalf("Select err = %v", err)
		}
		counts[selected.ID]++
	}
	// The selector preserves existing smooth-WRR credit when health changes, so
	// the transition window may differ by one selection from the steady-state
	// 5:1 ratio. A rounded integer floor would instead collapse this to 1:1.
	if counts[healthy.ID] < 498 || counts[healthy.ID] > 502 ||
		counts[degraded.ID] < 98 || counts[degraded.ID] > 102 {
		t.Fatalf("selection distribution = %+v, want approximately 500:100", counts)
	}
}

func TestSubscriptionAccountSelector_RefreshesConfiguredWeight(t *testing.T) {
	s := NewSubscriptionAccountSelector()
	account := &SubscriptionAccount{ID: 7, Priority: 1}
	if _, err := s.Select(context.Background(), "g", []*SubscriptionAccount{account}); err != nil {
		t.Fatalf("initial Select err = %v", err)
	}

	updated := &SubscriptionAccount{ID: 7, Priority: 9}
	if _, err := s.Select(context.Background(), "g", []*SubscriptionAccount{updated}); err != nil {
		t.Fatalf("updated Select err = %v", err)
	}
	if got := s.GetStats()[7].Weight; got != 9 {
		t.Fatalf("selector weight after account update = %d, want 9", got)
	}
}

func TestSubscriptionAccountSelector_ExcludesOpenCircuitFromTotalWeight(t *testing.T) {
	s := NewSubscriptionAccountSelector()
	high := &SubscriptionAccount{ID: 1, Priority: 100}
	low := &SubscriptionAccount{ID: 2, Priority: 1}
	candidates := []*SubscriptionAccount{high, low}
	if _, err := s.Select(context.Background(), "g", candidates); err != nil {
		t.Fatalf("prime Select err = %v", err)
	}

	s.mu.Lock()
	for _, st := range s.accounts {
		st.currentWeight = 0
	}
	s.accounts[high.ID].circuitOpenUntil = time.Now().Add(time.Minute).UnixNano()
	s.mu.Unlock()

	for i := 0; i < 5; i++ {
		selected, err := s.Select(context.Background(), "g", candidates)
		if err != nil {
			t.Fatalf("Select err = %v", err)
		}
		if selected.ID != low.ID {
			t.Fatalf("selected circuit-open account %d, want %d", selected.ID, low.ID)
		}
	}
	if got := s.GetStats()[low.ID].CurrentWeight; got != 0 {
		t.Fatalf("eligible account current weight = %d, want 0; open circuits must not contribute to total weight", got)
	}
}

// ── v0.11.0 Phase 3 §3.1: weight field takes precedence over priority ──────

func TestAccountSelectorWeight_PrefersExplicitWeightOverPriority(t *testing.T) {
	// When Weight is set, it wins over Priority. This is the fix for the
	// "weight collapses to 1" bug: an operator setting priority=10 used to get
	// weight=10, but an operator setting priority=10 AND weight=100 now gets
	// weight=100 (not priority=10).
	cases := []struct {
		name string
		acct *SubscriptionAccount
		want int32
	}{
		{"weight wins over priority", &SubscriptionAccount{ID: 1, Priority: 10, Weight: 100}, 100},
		{"priority fallback when weight unset", &SubscriptionAccount{ID: 2, Priority: 7, Weight: 0}, 7},
		{"default 1 when both unset", &SubscriptionAccount{ID: 3, Priority: 0, Weight: 0}, 1},
		{"weight only", &SubscriptionAccount{ID: 4, Priority: 0, Weight: 50}, 50},
		{"nil account", nil, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := accountSelectorWeight(tc.acct); got != tc.want {
				t.Fatalf("accountSelectorWeight(%+v) = %d, want %d", tc.acct, got, tc.want)
			}
		})
	}
}

func TestSubscriptionAccountSelector_WeightDrivesDistribution(t *testing.T) {
	// Two accounts, both priority=1 (same tier), but weights 100:20.
	// Over many selections the distribution should approximate 100:20 ≈ 83%:17%,
	// NOT the previous 1:1 collapse. This is the core Phase 3 §3.1 acceptance
	// criterion: "跨来源选择不得继续把订阅账号权重硬编码为 1".
	s := NewSubscriptionAccountSelector()
	high := &SubscriptionAccount{ID: 1, Priority: 1, Weight: 100}
	low := &SubscriptionAccount{ID: 2, Priority: 1, Weight: 20}
	candidates := []*SubscriptionAccount{high, low}

	counts := map[int64]int{}
	const iterations = 600
	for i := 0; i < iterations; i++ {
		selected, err := s.Select(context.Background(), "g", candidates)
		if err != nil {
			t.Fatalf("Select err = %v", err)
		}
		counts[selected.ID]++
	}
	// Expected ratio 100:20 = 5:1 → high ≈ 500, low ≈ 100. Allow a tolerance
	// band for smooth-WRR startup variance.
	highPct := float64(counts[1]) / float64(iterations)
	if highPct < 0.78 || highPct > 0.88 {
		t.Fatalf("weight 100:20 distribution = %d:%d (high %.2f%%), want ~83%% for high", counts[1], counts[2], highPct)
	}
}

func TestSubscriptionAccountSelector_WeightSamePriorityNotEqualDistribution(t *testing.T) {
	// Regression guard: two accounts with the SAME priority but DIFFERENT
	// weights must NOT split 50:50 (the old hard-coded-1 behaviour).
	s := NewSubscriptionAccountSelector()
	a := &SubscriptionAccount{ID: 1, Priority: 5, Weight: 1}
	b := &SubscriptionAccount{ID: 2, Priority: 5, Weight: 9}
	candidates := []*SubscriptionAccount{a, b}

	counts := map[int64]int{}
	const iterations = 1000
	for i := 0; i < iterations; i++ {
		selected, err := s.Select(context.Background(), "g", candidates)
		if err != nil {
			t.Fatalf("Select err = %v", err)
		}
		counts[selected.ID]++
	}
	// weight 1:9 → b should get ~90%. If they were equal (old bug), it'd be 50%.
	bPct := float64(counts[2]) / float64(iterations)
	if bPct < 0.85 || bPct > 0.95 {
		t.Fatalf("weight 1:9 distribution = %d:%d (b %.2f%%), want ~90%% for b", counts[1], counts[2], bPct)
	}
}

// ── v0.11.0 Phase 3 §3.2: load-aware inertness contract ────────────────────

// TestSubscriptionAccountSelector_LoadFactorInertInProduction proves the
// degradation contract: because Acquire/Release are never called in
// production, loadFactor is always neutral (100) and inflight stays 0. The
// selector still distributes correctly via health + configured weight, so the
// inert load-aware seam does NOT cause incorrect routing — it only means
// in-flight saturation is not caught (a saturated account is caught by its
// circuit breaker instead). This test pins that contract so a future seam
// that wires Acquire/Release must explicitly update it.
func TestSubscriptionAccountSelector_LoadFactorInertInProduction(t *testing.T) {
	s := NewSubscriptionAccountSelector()
	a := &SubscriptionAccount{ID: 1, Priority: 1, Weight: 100}
	b := &SubscriptionAccount{ID: 2, Priority: 1, Weight: 100}
	candidates := []*SubscriptionAccount{a, b}

	// Prime the selector.
	for i := 0; i < 10; i++ {
		if _, err := s.Select(context.Background(), "g", candidates); err != nil {
			t.Fatalf("Select err = %v", err)
		}
	}

	// Inert contract: no Acquire was called, so inflight == 0 and loadFactor
	// == 100 (neutral) for both accounts.
	for _, id := range []int64{1, 2} {
		st := s.accounts[id]
		if got := st.inflight.Load(); got != 0 {
			t.Fatalf("account %d inflight = %d, want 0 (Acquire never called in production)", id, got)
		}
		if got := st.loadFactor(); got != 100 {
			t.Fatalf("account %d loadFactor = %d, want 100 (neutral when inert)", id, got)
		}
	}

	// Distribution still works via configured weight (100:100 → ~50:50).
	counts := map[int64]int{}
	for i := 0; i < 1000; i++ {
		selected, err := s.Select(context.Background(), "g", candidates)
		if err != nil {
			t.Fatalf("Select err = %v", err)
		}
		counts[selected.ID]++
	}
	// Equal weights → roughly 50:50. The point is that inert loadFactor does
	// not break distribution.
	if counts[1] < 400 || counts[1] > 600 {
		t.Fatalf("inert loadFactor distribution = %d:%d, want ~500:500", counts[1], counts[2])
	}
}

// TestSubscriptionAccountSelector_LoadFactorDegradesWhenAcquireCalled proves
// that IF a future seam wires Acquire/Release, the loadFactor bands engage
// correctly. This is the "fault test that proves safe degradation" the roadmap
// requires for the day the seam is wired; today it documents the intended
// behaviour so the seam implementer has a target.
func TestSubscriptionAccountSelector_LoadFactorDegradesWhenAcquireCalled(t *testing.T) {
	s := NewSubscriptionAccountSelector()
	a := &SubscriptionAccount{ID: 1, Priority: 1, Weight: 100}
	if _, err := s.Select(context.Background(), "g", []*SubscriptionAccount{a}); err != nil {
		t.Fatalf("Select err = %v", err)
	}

	// Simulate a future seam that reports in-flight load.
	for i := 0; i < 25; i++ {
		s.Acquire(1)
	}
	st := s.accounts[1]
	if got := st.inflight.Load(); got != 25 {
		t.Fatalf("inflight after 25 Acquire = %d, want 25", got)
	}
	// 25 in-flight falls in the [20,50) band → loadFactor 20.
	if got := st.loadFactor(); got != 20 {
		t.Fatalf("loadFactor at inflight=25 = %d, want 20", got)
	}

	// Release back to neutral.
	for i := 0; i < 25; i++ {
		s.Release(1)
	}
	if got := st.loadFactor(); got != 100 {
		t.Fatalf("loadFactor after full Release = %d, want 100", got)
	}
}
