package biz

import (
	"context"
	"errors"
	"testing"
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
