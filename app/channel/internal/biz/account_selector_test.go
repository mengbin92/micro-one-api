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
