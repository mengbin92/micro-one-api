package biz

import (
	"sync/atomic"
	"time"

	"micro-one-api/pkg/safecast"
)

// Match the relay account-concurrency lease duration. Live streams renew each
// minute; a lost release or crashed relay cannot occupy the lease forever.
const executionSlotLeaseDuration = 2 * time.Minute

// Both selectors share token identity and expiry. The containing selector lock
// protects these maps; inflight is also read by load weighting helpers.
type executionSlotLeases struct {
	inflight        atomic.Int32
	slotLeases      map[string]int64 // slot ID → UnixNano deadline
	releasedSlots   map[string]int64 // cancelled IDs reject delayed acquire/renew
	nextSlotCleanup int64            // tombstone cleanup deadline
}

func (s *executionSlotLeases) recordSlot(slotID string, acquired bool, limit int32) bool {
	if slotID == "" {
		return false
	}
	if s.slotLeases == nil {
		s.slotLeases = make(map[string]int64)
		s.releasedSlots = make(map[string]int64)
	}
	now := time.Now().UnixNano()
	s.reapSlotLeases(now)
	if acquired {
		if _, released := s.releasedSlots[slotID]; released {
			return false
		}
		if _, exists := s.slotLeases[slotID]; !exists && limit > 0 && s.inflight.Load() >= limit {
			return false
		}
		s.slotLeases[slotID] = now + executionSlotLeaseDuration.Nanoseconds()
	} else {
		delete(s.slotLeases, slotID)
		s.releasedSlots[slotID] = now + executionSlotLeaseDuration.Nanoseconds()
	}
	s.inflight.Store(safecast.IntToInt32Saturating(len(s.slotLeases)))
	return true
}

func (s *executionSlotLeases) reapSlotLeases(now int64) {
	for slotID, deadline := range s.slotLeases {
		if deadline <= now {
			delete(s.slotLeases, slotID)
		}
	}
	if now >= s.nextSlotCleanup {
		for slotID, deadline := range s.releasedSlots {
			if deadline <= now {
				delete(s.releasedSlots, slotID)
			}
		}
		s.nextSlotCleanup = now + time.Minute.Nanoseconds()
	}
	s.inflight.Store(safecast.IntToInt32Saturating(len(s.slotLeases)))
}
