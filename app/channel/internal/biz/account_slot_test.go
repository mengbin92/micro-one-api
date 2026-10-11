package biz

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestAccountSlotLeasesRemainTelemetry(t *testing.T) {
	s := NewSubscriptionAccountSelector()
	account := &SubscriptionAccount{ID: 1, Concurrency: 1, Weight: 100}
	if _, err := s.Select(context.Background(), "g", []*SubscriptionAccount{account}); err != nil {
		t.Fatal(err)
	}
	// Admission belongs to the actual relay limiter. This selector must accept
	// its load reports even above the configured cap or the channel cap of 100.
	for i := range 101 {
		if !s.RecordSlot(1, fmt.Sprintf("execution-%d", i), true) {
			t.Fatalf("account telemetry imposed an admission cap at %d", i)
		}
	}
	if !s.RecordSlot(1, "execution-0", true) {
		t.Fatal("duplicate renewal rejected")
	}
	if got := s.GetStats()[1]; got.Inflight != 101 || got.ErrorRate != 0 {
		t.Fatalf("renewal changed load or health: %+v", got)
	}
	s.RecordSlot(1, "execution-0", false)
	s.RecordSlot(1, "execution-0", false)
	if got := s.GetStats()[1].Inflight; got != 100 {
		t.Fatalf("duplicate release decremented a different ID: inflight=%d", got)
	}
	if s.RecordSlot(1, "execution-0", true) {
		t.Fatal("late heartbeat resurrected a released lease")
	}
	s.RecordSlot(1, "cleaned-up-before-acquire", false)
	if s.RecordSlot(1, "cleaned-up-before-acquire", true) {
		t.Fatal("late acquire resurrected telemetry after timeout cleanup")
	}
}

func TestAccountSlotLeaseExpiryRestoresWeight(t *testing.T) {
	for _, operation := range []string{"select", "stats", "record"} {
		t.Run(operation, func(t *testing.T) {
			s := NewSubscriptionAccountSelector()
			account := &SubscriptionAccount{ID: 1, Concurrency: 1, Weight: 100}
			if !s.RecordSlot(1, "lost-release", true) {
				t.Fatal("lease rejected")
			}
			if _, err := s.Select(context.Background(), "g", []*SubscriptionAccount{account}); err != nil {
				t.Fatal(err)
			}
			state := s.accounts[1]
			if state.loadFactor() != 1 {
				t.Fatal("executing account was not derated")
			}
			s.mu.Lock()
			state.slotLeases["lost-release"] = time.Now().Add(-time.Second).UnixNano()
			s.mu.Unlock()
			switch operation {
			case "select":
				if _, err := s.Select(context.Background(), "g", []*SubscriptionAccount{account}); err != nil {
					t.Fatal(err)
				}
			case "stats":
				s.GetStats()
			case "record":
				s.RecordSlot(1, "another-completed", false)
			}
			if state.inflight.Load() != 0 || state.loadFactor() != 100 {
				t.Fatalf("expired telemetry still derates account: inflight=%d loadFactor=%d", state.inflight.Load(), state.loadFactor())
			}
		})
	}
}
