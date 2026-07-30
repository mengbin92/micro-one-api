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
// v0.11.0 Phase D §12 — load-aware selection is now live. The selector queries
// a LoadOracle (Redis-backed, injected by channel-service wiring) on every
// Select to refresh a cross-replica in-flight snapshot per account, so
// loadFactor de-rates a saturated account across ALL replicas, not just the
// local one. The local Acquire/Release hooks remain for in-process callers;
// when neither the oracle nor Acquire is wired, loadFactor is neutral (100)
// and the selector degrades safely to health + configured-weight weighting.
// A saturated account is still ultimately caught by its circuit breaker.
//
// Lifetime: one selector per ChannelUsecase (process-wide). It tracks runtime
// state per account id; account snapshots passed to Select are read-only.
//
// See docs/model-management-design.md §12.2.

type accountState struct {
	accountID            int64
	weight               int32           // configured weight (priority-derived)
	maxConcurrent        int32           // configured concurrency cap (from SubscriptionAccount.Concurrency)
	currentWeight        int64           // smooth WRR current weight (fixed-point scale)
	recentErrors         *SlidingCounter // last 60s error count
	inflight             atomic.Int32    // local in-flight requests (set by server)
	crossReplicaInflight atomic.Int32    // cross-replica inflight snapshot (Phase D #12)
	circuitOpenUntil     int64           // UnixNano; 0 = closed
}

// SubscriptionAccountSelector is the health-aware account selector.
// LoadOracle reports the cross-replica in-flight count for a subscription
// account. The relay-gateway tracks in-flight requests in Redis (ZSet under
// subscription_account:concurrency:<id>); the channel-service selector, running
// in a different process, queries it here so loadFactor de-rates a saturated
// account across all replicas, not just the local one. A nil oracle or a
// zero/error result falls back to the local atomic inflight (which stays 0 in
// production when no one calls Acquire), so the selector degrades safely to the
// health-only weighting that shipped before Phase D.
type LoadOracle interface {
	Inflight(ctx context.Context, accountID int64) int32
	// InflightBatch returns the cross-replica in-flight count for every
	// requested account in a single round-trip (pipelined where the backend
	// supports it). Implementations that only support per-account lookup can
	// fall back to looping Inflight. Absent/zero entries mean "no live load".
	InflightBatch(ctx context.Context, accountIDs []int64) map[int64]int32
}

type noopLoadOracle struct{}

func (noopLoadOracle) Inflight(context.Context, int64) int32                  { return 0 }
func (noopLoadOracle) InflightBatch(context.Context, []int64) map[int64]int32 { return nil }

type SubscriptionAccountSelector struct {
	mu         sync.Mutex
	accounts   map[int64]*accountState
	loadOracle LoadOracle // cross-replica in-flight source; nil = noop (inert)
}

// NewSubscriptionAccountSelector creates a new selector.
func NewSubscriptionAccountSelector() *SubscriptionAccountSelector {
	return &SubscriptionAccountSelector{accounts: make(map[int64]*accountState)}
}

// SetLoadOracle wires the cross-replica in-flight source. Safe to call before
// first selection; nil installs the noop oracle so loadFactor falls back to the
// local atomic counter. Used by channel-service wiring to inject the
// Redis-backed account-concurrency reader (Phase D #12).
func (s *SubscriptionAccountSelector) SetLoadOracle(oracle LoadOracle) {
	if s == nil {
		return
	}
	if oracle == nil {
		s.loadOracle = noopLoadOracle{}
		return
	}
	s.loadOracle = oracle
}

// prefetchInflight queries the cross-replica in-flight count for each
// candidate OUTSIDE the selector lock (MEDIUM-2). Returning a map keyed by
// account id lets Select write all snapshots in one locked pass. An absent key
// (or zero value) means "no live load reported" so the account is not derated
// by the cross-replica factor. A nil/noop oracle returns an empty map.
func (s *SubscriptionAccountSelector) prefetchInflight(ctx context.Context, candidates []*SubscriptionAccount) map[int64]int32 {
	out := make(map[int64]int32, len(candidates))
	if s == nil || s.loadOracle == nil {
		return out
	}
	ids := make([]int64, 0, len(candidates))
	for _, acct := range candidates {
		if acct != nil && acct.ID > 0 {
			ids = append(ids, acct.ID)
		}
	}
	if len(ids) == 0 {
		return out
	}
	// Prefer the batch API: the Redis oracle pipelines all ZCOUNTs into one
	// round-trip (MEDIUM-2), avoiding N serial RTTs per selection.
	if batched := s.loadOracle.InflightBatch(ctx, ids); batched != nil {
		return batched
	}
	for _, id := range ids {
		out[id] = s.loadOracle.Inflight(ctx, id)
	}
	return out
}

// Select picks one account from the tier using smooth WRR × health factor.
// Returns ErrSubscriptionAccountNotFound when no candidate is selectable
// (empty tier). Candidates are assumed pre-filtered for status/quota/
// runtime-blocked by the caller (SelectSubscriptionAccount).
func (s *SubscriptionAccountSelector) Select(ctx context.Context, group string, candidates []*SubscriptionAccount) (*SubscriptionAccount, error) {
	if len(candidates) == 0 {
		return nil, ErrSubscriptionAccountNotFound
	}
	// Phase D #12: refresh the cross-replica in-flight snapshot for each
	// candidate BEFORE taking the lock. The relay-gateway maintains a Redis
	// ZSet per account (subscription_account:concurrency:<id>); the LoadOracle
	// counts its members so loadFactor de-rates a saturated account across ALL
	// replicas, not just this process. Querying outside the selector lock
	// keeps per-candidate Redis RTTs off the hot-path critical section
	// (MEDIUM-2): N candidates cost N lookups, but they no longer block every
	// other concurrent Select in this process. A nil/noop oracle yields an
	// empty map, so loadFactor falls back to the local atomic inflight.
	crossReplica := s.prefetchInflight(ctx, candidates)

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
		// Store the cross-replica snapshot unconditionally (MEDIUM-1): an idle
		// account reads 0, which must overwrite a previously-high value so the
		// account is no longer derated once it drains. Only writing on n>0
		// would pin the snapshot at its peak forever.
		state.crossReplicaInflight.Store(crossReplica[acct.ID])
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
		// Always sync maxConcurrent so an admin edit (including lowering the
		// cap back to 0 = unlimited) takes effect. Gating on >0 would leave a
		// stale cap sticky after a config change (L2).
		existing.maxConcurrent = acct.Concurrency
		return existing
	}
	state := &accountState{
		accountID:     acct.ID,
		weight:        accountSelectorWeight(acct),
		maxConcurrent: acct.Concurrency,
		recentErrors:  NewSlidingCounter(60 * time.Second),
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

// loadFactor de-rates an account as its in-flight count climbs toward
// maxConcurrent, so a saturated account receives less traffic than an idle one.
// Returns 100 (neutral) when no load is tracked.
//
// v0.11.0 Phase D #12: no longer inert. The cross-replica in-flight snapshot
// (refreshed in Select via LoadOracle, querying the relay-gateway's Redis
// ZSet) is used when available; otherwise the local atomic inflight applies.
// We take max(local, crossReplica) so a busy local replica is never hidden by
// a stale or zeroed cross-replica reading. Bands are relative to
// maxConcurrent (from SubscriptionAccount.Concurrency) so the same thresholds
// apply regardless of the configured ceiling; when maxConcurrent is unset (0)
// loadFactor is neutral. Mirrors the channel-side bands (100/80/50/20/1).
// See docs/model-management-design.md §12.2 and docs/releases/review-v0.11.0.md.
func (st *accountState) loadFactor() int32 {
	if st == nil {
		return 100
	}
	if st.maxConcurrent <= 0 {
		// No concurrency cap configured: fall back to the legacy absolute bands
		// on the local counter so a non-zero Acquire still de-rates. When no
		// load is tracked (the pre-Phase-D default) this returns 100.
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
	local := st.inflight.Load()
	cross := st.crossReplicaInflight.Load()
	inflight := local
	if cross > inflight {
		inflight = cross
	}
	if inflight <= 0 {
		return 100
	}
	util := float64(inflight) / float64(st.maxConcurrent)
	switch {
	case util < 0.4:
		return 100
	case util < 0.6:
		return 80
	case util < 0.75:
		return 50
	case util < 0.9:
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
