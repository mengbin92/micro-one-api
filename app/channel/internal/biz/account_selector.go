package biz

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"micro-one-api/pkg/safecast"
)

// SubscriptionAccountSelector selects a subscription account from a priority
// tier using health-aware smooth weighted round-robin. Configured weight is
// scaled by recent relay health; a circuit breaker temporarily removes accounts
// with sustained failures.
//
// v0.11.0 Phase 3 §3.2 — load-aware selection status (explicit decision):
// Acquire/Release and the in-flight-derived loadFactor are RETAINED but are
// INERT in production: no relay-gateway dispatch seam calls them, so
// loadFactor always returns 100 (neutral) and inflight stays 0. They are NOT
// advertised as a working load-aware feature. Wiring real in-flight feedback
// would require a cross-service RPC on the relay hot path; the roadmap defers
// that until a Redis-backed concurrent-state or async best-effort seam is
// added. The selector degrades safely when loadFactor is inert: health + the
// configured weight still drive distribution, and a saturated account is only
// caught by its circuit breaker (not by in-flight saturation). See
// TestSubscriptionAccountSelector_LoadFactorInertInProduction for the
// degradation contract.
//
// Lifetime: one selector per ChannelUsecase (process-wide). It tracks runtime
// state per account id; account snapshots passed to Select are read-only.
//
// See docs/model-management-design.md §12.2.

type accountState struct {
	accountID        int64
	weight           int32           // configured weight (priority-derived)
	currentWeight    int64           // smooth WRR current weight (fixed-point scale)
	recentErrors     *SlidingCounter // last 60s error count
	inflight         atomic.Int32    // current in-flight requests (set by server)
	circuitOpenUntil int64           // UnixNano; 0 = closed
}

// SubscriptionAccountSelector is the health-aware account selector.
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
	var bestWeight int64 = math.MinInt64

	for _, acct := range candidates {
		if acct == nil {
			continue
		}
		// Refresh configured fields on every selection. Runtime state (current
		// weight, errors, in-flight count) is preserved by updateAccountLocked,
		// while admin changes such as a new priority take effect immediately.
		state := s.updateAccountLocked(acct)
		// Skip circuit-opened accounts.
		if state.circuitOpenUntil > 0 && state.circuitOpenUntil > now {
			continue
		}
		effectiveWeight := accountEffectiveWeight(state)
		state.currentWeight += effectiveWeight
		if state.currentWeight > bestWeight {
			bestWeight = state.currentWeight
			best = state
		}
	}
	if best == nil {
		return nil, ErrSubscriptionAccountNotFound
	}
	totalWeight := s.totalEffectiveWeight(candidates, now)
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
//
// v0.11.0 Phase 3 §3.2: this is an INERT reserved hook — production relay
// dispatch does not call it, so loadFactor stays neutral (100) and the
// selector never de-rates on in-flight saturation. It is retained so a future
// Redis-backed or async best-effort seam can populate it without changing the
// public API. A non-positive id is a no-op.
//
// If Acquire is called for an account the selector has not yet seen via Select,
// it creates a state with a neutral weight (1); the weight is corrected on the
// next Select that touches this account.
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

// RecordAccountHealth records a real upstream outcome for an account. A
// failure increments the sliding 60-second error counter and may open the
// circuit; successes rely on old errors aging out of the window. Relay-gateway
// reports outcomes through the dedicated RecordSubscriptionAccountHealth RPC.
// Local admission failures (concurrency/RPM/session limits) are not recorded.
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
	CurrentWeight int64
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

// accountSelectorWeight returns the configured within-tier weight for an
// account. v0.11.0 Phase 3 §3.1: Weight is the explicit field; Priority is for
// layering only. When Weight is unset (0) we fall back to Priority-derived to
// preserve legacy deployments, then to 1 — but a configured Weight always wins
// so operators can set 100:20 ratios without the value collapsing to 1.
func accountSelectorWeight(acct *SubscriptionAccount) int32 {
	if acct == nil {
		return 1
	}
	if acct.Weight > 0 {
		return acct.Weight
	}
	if acct.Priority > 0 {
		return safecast.Int64ToInt32Saturating(acct.Priority)
	}
	return 1
}

// accountEffectiveWeight keeps health/load factors in fixed-point form rather
// than rounding them back to the configured integer weight. This preserves
// ratios such as 100%:20% even when both accounts use the default weight 1.
func accountEffectiveWeight(state *accountState) int64 {
	if state == nil {
		return 0
	}
	return int64(state.weight) * int64(state.healthFactor()) * int64(state.loadFactor())
}

func (s *SubscriptionAccountSelector) totalEffectiveWeight(candidates []*SubscriptionAccount, now int64) int64 {
	var total int64
	for _, acct := range candidates {
		if acct == nil {
			continue
		}
		if state, ok := s.accounts[acct.ID]; ok {
			if state.circuitOpenUntil > 0 && state.circuitOpenUntil > now {
				continue
			}
			total += accountEffectiveWeight(state)
		}
	}
	return total
}

// loadFactor de-rates an account as its in-flight count climbs, so a saturated
// account receives less traffic than an idle one. Returns 100 when no load is
// tracked.
//
// v0.11.0 Phase 3 §3.2: INERT in production — Acquire/Release are not called
// by relay dispatch, so inflight is always 0 and loadFactor always returns
// 100 (neutral). The band thresholds are retained for the future Redis-backed
// seam. Do NOT advertise this as a working load-aware feature until that seam
// is wired and a fault test proves degradation is safe.
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
