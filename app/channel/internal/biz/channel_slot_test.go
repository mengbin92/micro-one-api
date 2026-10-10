package biz

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestChannelSlotLeaseIdentityAndRecovery(t *testing.T) {
	s := NewWeightedSelector()
	if !s.RecordSlot(1, "active", true) || !s.RecordSlot(1, "other", true) {
		t.Fatal("initial execution leases rejected")
	}
	s.mu.Lock()
	s.channels[1].slotLeases["active"] = time.Now().Add(time.Minute).UnixNano()
	s.mu.Unlock()
	if !s.RecordSlot(1, "active", true) {
		t.Fatal("active lease renewal rejected")
	}
	if got := s.GetStats()[1].Inflight; got != 2 {
		t.Fatalf("duplicate acquire counted twice: inflight=%d", got)
	}
	s.mu.Lock()
	deadline := s.channels[1].slotLeases["active"]
	s.mu.Unlock()
	if deadline < time.Now().Add(90*time.Second).UnixNano() {
		t.Fatal("duplicate acquire did not renew the lease")
	}
	for range 2 {
		if !s.RecordSlot(1, "active", false) {
			t.Fatal("idempotent release rejected")
		}
	}
	if got := s.GetStats()[1].Inflight; got != 1 {
		t.Fatalf("duplicate release affected a different lease: inflight=%d", got)
	}
	if s.RecordSlot(1, "active", true) {
		t.Fatal("late heartbeat resurrected a released lease")
	}
	if !s.RecordSlot(1, "cancelled-before-acquire", false) || s.RecordSlot(1, "cancelled-before-acquire", true) {
		t.Fatal("late acquire resurrected an acquisition already cleaned up")
	}
	if got := s.GetStats()[1].Inflight; got != 1 {
		t.Fatalf("cancelled acquisition changed another lease: inflight=%d", got)
	}
}

func TestChannelSlotLeaseExpiryAffectsSelectionAndStats(t *testing.T) {
	for _, operation := range []string{"select", "stats", "acquire"} {
		t.Run(operation, func(t *testing.T) {
			s := NewWeightedSelector()
			ch := &Channel{ID: 1, Weight: 1}
			s.UpdateChannel(ch)
			for i := range 100 {
				if !s.RecordSlot(1, fmt.Sprintf("execution-%d", i), true) {
					t.Fatal("lease rejected before capacity")
				}
			}
			if _, err := s.Select(context.Background(), "g", []*Channel{ch}); err == nil {
				t.Fatal("saturated channel remains selectable")
			}
			s.mu.Lock()
			for slotID := range s.channels[1].slotLeases {
				s.channels[1].slotLeases[slotID] = time.Now().Add(-time.Second).UnixNano()
			}
			s.mu.Unlock()
			want := int32(0)
			switch operation {
			case "select":
				if _, err := s.Select(context.Background(), "g", []*Channel{ch}); err != nil {
					t.Fatalf("expired leases blocked selection: %v", err)
				}
			case "stats":
				if got := s.GetStats()[1].Inflight; got != 0 {
					t.Fatalf("stats counted expired leases: inflight=%d", got)
				}
			case "acquire":
				if !s.RecordSlot(1, "new-execution", true) {
					t.Fatal("expired leases blocked new execution")
				}
				want = 1
			}
			if got := s.GetStats()[1].Inflight; got != want {
				t.Fatalf("expired leases retained inflight=%d, want%d", got, want)
			}
		})
	}
}

func TestChannelSlotConcurrentAdmissionCap(t *testing.T) {
	s := NewWeightedSelector()
	var wg sync.WaitGroup
	granted := make(chan string, 200)
	for i := range 200 {
		wg.Go(func() {
			slotID := fmt.Sprintf("execution-%d", i)
			if s.RecordSlot(1, slotID, true) {
				granted <- slotID
			}
		})
	}
	wg.Wait()
	close(granted)
	if len(granted) != 100 || s.GetStats()[1].Inflight != 100 {
		t.Fatalf("atomic admission granted=%d, stats=%+v; cap=100", len(granted), s.GetStats()[1])
	}
	for slotID := range granted {
		if !s.RecordSlot(1, slotID, true) {
			t.Fatal("renewal must work even at capacity")
		}
		if !s.RecordSlot(1, slotID, false) {
			t.Fatal("release rejected")
		}
	}
	if got := s.GetStats()[1].Inflight; got != 0 {
		t.Fatalf("completed leases retained inflight=%d", got)
	}
}

func TestChannelSlotTombstonesHaveBoundedRetention(t *testing.T) {
	s := NewWeightedSelector()
	s.RecordSlot(1, "released", false)
	s.mu.Lock()
	state := s.channels[1]
	state.releasedSlots["released"] = time.Now().Add(-time.Second).UnixNano()
	// The previous cleanup was less than a minute ago. Hot-path calls leave
	// the tombstone sweep alone until its deadline to avoid a scan per call.
	nextCleanup := state.nextSlotCleanup
	s.mu.Unlock()
	s.GetStats()
	s.mu.Lock()
	if _, ok := state.releasedSlots["released"]; !ok || state.nextSlotCleanup != nextCleanup {
		s.mu.Unlock()
		t.Fatal("tombstone cleanup was repeated before its minute deadline")
	}
	state.nextSlotCleanup = 0
	s.mu.Unlock()
	s.GetStats()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(state.releasedSlots) != 0 {
		t.Fatal("expired tombstone retained after cleanup deadline")
	}
}
