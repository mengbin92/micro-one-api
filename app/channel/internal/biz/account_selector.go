package biz

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"micro-one-api/pkg/safecast"
)

// SubscriptionAccountSelector (P2 #7) selects a subscription account from a
// priority tier using a load-aware weighted round-robin algorithm, upgrading
// the previous uniform-random pick. Mirrors the channel WeightedSelector:
// smooth WRR over configured weight, scaled by a health factor (from recent
// relay outcomes recorded via RecordAccountHealth) and a load factor (from
// the in-flight count maintained via Acquire/Release). The previous random
// selector spread load evenly but ignored live health and saturation, so a
// failing or saturated account kept receiving as much traffic as a healthy
// idle one.
//
// Lifetime: one selector per ChannelUsecase (process-wide). It tracks runtime
// state per account id; account snapshots passed to Select are read-only.
//
// See docs/model-management-design.md §9.3 #7 / §9.4 P2.

type accountState struct {
	accountID        int64
	weight           int32           // configured weight (priority-derived)
	currentWeight    int32           // smooth WRR current weight
	recentErrors     *SlidingCounter // last 60s error count
	inflight         atomic.Int32    // current in-flight requests (set by server)
	circuitOpenUntil int64           // UnixNano; 0 = closed
}

// SubscriptionAccountSelector is the load-aware account selector.
type SubscriptionAccountSelector struct {
	mu       sync.Mutex
	accounts map[int64]*accountState
}

// NewSubscriptionAccountSelector creates a new selector.
func NewSubscriptionAccountSelector() *SubscriptionAccountSelector {
	return &SubscriptionAccountSelector{accounts: make(map[int64]*accountState)}
}

// Select picks one account from the tier using smooth WRR × health factor.
// Returns ErrSubscriptionAccountNotFound when no candidate is selectable
// (empty tier). Candidates are assumed pre-filtered for status/quota/
// runtime-blocked by the caller (SelectSubscriptionAccount).
func (s *SubscriptionAccountSelector) Select(ctx context.Context, group string, candidates []*SubscriptionAccount) (*SubscriptionAccount, error) {
	if len(candidates) == 0 {
		return nil, ErrSubscriptionAccountNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UnixNano()
	var best *accountState
	var bestWeight int32 = math.MinInt32

	for _, acct := range candidates {
		if acct == nil {
			continue
		}
		state, ok := s.accounts[acct.ID]
		if !ok {
			state = s.updateAccountLocked(acct)
		}
		// Skip circuit-opened accounts.
		if state.circuitOpenUntil > 0 && state.circuitOpenUntil > now {
			continue
		}
		// Dynamic weight = configured weight × health factor × load factor.
		// Health factor is driven by RecordAccountHealth; load factor is driven
		// by Acquire/Release. Until the feedback loop is wired at the relay
		// gateway (see TODO below), both default to neutral and the selector
		// degrades to smooth WRR by configured weight.
		dynamicWeight := state.weight * state.healthFactor() * state.loadFactor()
		state.currentWeight += dynamicWeight
		if state.currentWeight > bestWeight {
			bestWeight = state.currentWeight
			best = state
		}
	}
	if best == nil {
		return nil, ErrSubscriptionAccountNotFound
	}
	totalWeight := s.totalWeight(candidates)
	if totalWeight > 0 {
		best.currentWeight -= totalWeight
	}
	for _, acct := range candidates {
		if acct != nil && acct.ID == best.accountID {
			return acct, nil
		}
	}
	return nil, ErrSubscriptionAccountNotFound
}

// Acquire reserves an in-flight slot for an account. Paired with Release.
// Called by the relay gateway at dispatch time so the selector's load factor
// reflects live saturation. A non-positive id is a no-op.
//
// If Acquire is called for an account the selector has not yet seen via Select,
// it creates a state with a neutral weight (1); the weight is corrected on the
// next Select that touches this account. This is safe because Acquire only
// inflates inflight, which loadFactor reads directly.
func (s *SubscriptionAccountSelector) Acquire(accountID int64) {
	if accountID <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.accounts[accountID]
	if !ok {
		state = &accountState{
			accountID:    accountID,
			weight:       1,
			recentErrors: NewSlidingCounter(60 * time.Second),
		}
		s.accounts[accountID] = state
	}
	state.inflight.Add(1)
}

// Release frees an in-flight slot. Idempotent via the atomic floor at 0.
func (s *SubscriptionAccountSelector) Release(accountID int64) {
	if accountID <= 0 {
		return
	}
	s.mu.Lock()
	state, ok := s.accounts[accountID]
	s.mu.Unlock()
	if !ok {
		return
	}
	// Decrement but never go negative.
	for {
		cur := state.inflight.Load()
		if cur <= 0 {
			return
		}
		if state.inflight.CompareAndSwap(cur, cur-1) {
			return
		}
	}
}

// RecordAccountHealth records a relay outcome for an account, feeding the
// health factor. success=false increments the sliding 60s error counter and
// may trip the circuit breaker; success=true is a no-op on the counter (the
// window ages errors out over time).
//
// TODO(feedback-loop): the relay gateway does not yet call this method (nor
// Acquire/Release) at dispatch time because the channel-service gRPC surface
// exposes no RecordSubscriptionAccountHealth RPC. Until that RPC + its client
// adapter are added, the selector's health/load/circuit features are inert and
// selection is plain smooth-WRR-by-weight. See docs/model-management-design.md
// §9.3 #7.
func (s *SubscriptionAccountSelector) RecordAccountHealth(accountID int64, success bool) {
	if accountID <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.accounts[accountID]
	if !ok {
		state = &accountState{
			accountID:    accountID,
			weight:       1,
			recentErrors: NewSlidingCounter(60 * time.Second),
		}
		s.accounts[accountID] = state
	}
	if !success {
		state.recentErrors.Increment()
	}
	state.updateCircuitBreaker()
}

// RemoveAccount drops the runtime state for an account (e.g. after delete).
func (s *SubscriptionAccountSelector) RemoveAccount(accountID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.accounts, accountID)
}

// GetStats returns a snapshot of the selector's runtime state per account.
// Observability seam for the admin UI / tests.
func (s *SubscriptionAccountSelector) GetStats() map[int64]AccountSelectorStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	stats := make(map[int64]AccountSelectorStats, len(s.accounts))
	for id, st := range s.accounts {
		stats[id] = AccountSelectorStats{
			AccountID:     id,
			Weight:        st.weight,
			CurrentWeight: st.currentWeight,
			Inflight:      st.inflight.Load(),
			ErrorRate:     st.recentErrors.Rate(),
			IsCircuitOpen: st.circuitOpenUntil > 0,
		}
	}
	return stats
}

// AccountSelectorStats is the observability snapshot for one account.
type AccountSelectorStats struct {
	AccountID     int64
	Weight        int32
	CurrentWeight int32
	Inflight      int32
	ErrorRate     float64
	IsCircuitOpen bool
}

func (s *SubscriptionAccountSelector) updateAccountLocked(acct *SubscriptionAccount) *accountState {
	if existing, ok := s.accounts[acct.ID]; ok {
		existing.weight = accountSelectorWeight(acct)
		return existing
	}
	state := &accountState{
		accountID:    acct.ID,
		weight:       accountSelectorWeight(acct),
		recentErrors: NewSlidingCounter(60 * time.Second),
	}
	s.accounts[acct.ID] = state
	return state
}

func accountSelectorWeight(acct *SubscriptionAccount) int32 {
	if acct == nil {
		return 1
	}
	if acct.Priority > 0 {
		return safecast.Int64ToInt32Saturating(acct.Priority)
	}
	return 1
}

func (s *SubscriptionAccountSelector) totalWeight(candidates []*SubscriptionAccount) int32 {
	var total int32
	for _, acct := range candidates {
		if acct == nil {
			continue
		}
		if state, ok := s.accounts[acct.ID]; ok {
			total += state.weight
		}
	}
	return total
}

// loadFactor de-rates an account as its in-flight count climbs, so a saturated
// account receives less traffic than an idle one. Returns 100 when no load is
// tracked. The band thresholds mirror the channel WeightedSelector.
//
// NOTE: this only takes effect once Acquire/Release are called at the relay
// gateway dispatch boundary. The cross-service feedback loop (relay-gateway →
// channel-service gRPC) is not yet wired; see the TODO in the package doc.
func (st *accountState) loadFactor() int32 {
	inflight := st.inflight.Load()
	switch {
	case inflight <= 0:
		return 100
	case inflight < 10:
		return 80
	case inflight < 20:
		return 50
	case inflight < 50:
		return 20
	default:
		return 1
	}
}

// healthFactor returns 0-100 based on the recent error rate, mirroring the
// channel WeightedSelector bands so operators see consistent de-rating.
func (st *accountState) healthFactor() int32 {
	errorRate := st.recentErrors.Rate()
	switch {
	case errorRate < 0.01:
		return 100
	case errorRate < 0.05:
		return 80
	case errorRate < 0.10:
		return 50
	case errorRate < 0.30:
		return 20
	default:
		return 1
	}
}

// updateCircuitBreaker trips the circuit for 30s when the error rate is very
// high (>0.5 errors/sec) and clears it once the open window has elapsed.
// Because SlidingCounter errors decay naturally over the 60s window, a drop in
// failures lets the circuit close after the open window; there is no explicit
// success-driven decrement (the counter has none), so recovery is time-based.
func (st *accountState) updateCircuitBreaker() {
	now := time.Now().UnixNano()
	// If already open, only clear once the window has elapsed.
	if st.circuitOpenUntil > 0 {
		if st.circuitOpenUntil < now {
			st.circuitOpenUntil = 0
		}
		return
	}
	// Closed: trip only when the error rate is critically high.
	if st.recentErrors.Rate() > 0.5 {
		st.circuitOpenUntil = now + int64(30*time.Second)
	}
}
