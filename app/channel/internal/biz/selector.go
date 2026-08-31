package biz

import (
	"context"
	"math"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"micro-one-api/pkg/safecast"
)

// WeightedSelector selects channels using a weighted round-robin
// algorithm that considers response time, success rate, and configured weight.
type WeightedSelector struct {
	mu       sync.Mutex
	channels map[int64]*channelState // channelID → runtime state
}

// channelState holds runtime state for a channel.
type channelState struct {
	channel          *Channel
	weight           int32           // configured weight
	currentWeight    int64           // smooth WRR current weight (fixed-point scale)
	recentLatency    *SlidingWindow  // last 100 request latencies
	recentErrors     *SlidingCounter // last 60s error count
	inflight         atomic.Int32    // current in-flight requests
	maxConcurrent    int32           // max concurrent requests
	lastFailure      time.Time       // last failure time
	circuitOpenUntil int64           // Unix timestamp for circuit open
}

// SlidingWindow tracks recent latency values using a fixed-capacity ring
// buffer so memory is bounded regardless of how many values are observed.
type SlidingWindow struct {
	mu     sync.Mutex
	values []int64 // ring buffer; length == capacity once filled
	head   int     // next write position
	count  int     // number of valid entries (== len(values) when full)
}

// NewSlidingWindow creates a new sliding window for latency tracking.
// max must be > 0; a non-positive value falls back to a sensible default.
func NewSlidingWindow(max int) *SlidingWindow {
	if max <= 0 {
		max = 100
	}
	return &SlidingWindow{
		values: make([]int64, 0, max),
	}
}

// Add adds a value to the window. O(1) amortized; never grows the backing
// array beyond the configured capacity, avoiding the memory leak present in
// the previous `w.values = w.values[1:]` implementation.
func (w *SlidingWindow) Add(value int64) {
	w.mu.Lock()
	defer w.mu.Unlock()

	cap := cap(w.values)
	if cap == 0 {
		cap = 100
	}

	if len(w.values) < cap {
		// Still filling the buffer.
		w.values = append(w.values, value)
		w.head = (w.head + 1) % cap
		w.count = len(w.values)
		return
	}

	// Buffer full: overwrite the oldest entry at head and advance.
	w.values[w.head] = value
	w.head = (w.head + 1) % cap
	w.count = len(w.values)
}

// P95 returns the 95th percentile latency. O(n log n) via sort.Slice
// (replaces the previous O(n²) bubble sort).
func (w *SlidingWindow) P95() time.Duration {
	w.mu.Lock()
	n := len(w.values)
	if n == 0 {
		w.mu.Unlock()
		return 0
	}
	sorted := make([]int64, n)
	copy(sorted, w.values)
	w.mu.Unlock()

	slices.Sort(sorted)

	idx := max(int(math.Ceil(float64(n)*0.95))-1, 0)
	if idx >= n {
		idx = n - 1
	}
	return time.Duration(sorted[idx])
}

// SlidingCounter tracks recent error and total request counts in per-second
// buckets over a fixed window. Review channel-H1: the previous version recorded
// only errors and exposed them as errors-per-second, which made Rate()'s name a
// lie — callers compared it to a 0..1 threshold as if it were an error *ratio*,
// so low-traffic channels tripped on a handful of failures. It now tracks both
// errors and totals so Rate() returns a true error ratio (errors/total).
type SlidingCounter struct {
	mu          sync.Mutex
	errors      map[int64]int // timestamp (unix seconds) → error count
	totals      map[int64]int // timestamp (unix seconds) → total request count
	window      time.Duration
	lastCleanup int64 // unix seconds of last cleanup; initialized on first use
}

// NewSlidingCounter creates a new sliding counter.
func NewSlidingCounter(window time.Duration) *SlidingCounter {
	return &SlidingCounter{
		errors: make(map[int64]int),
		totals: make(map[int64]int),
		window: window,
	}
}

// RecordOutcome records a request outcome for the current timestamp: every
// call bumps the total counter, failures additionally bump the error counter.
// This is the primary recording API (replaces the old error-only Increment).
func (c *SlidingCounter) RecordOutcome(success bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now().Unix()
	c.totals[now]++
	if !success {
		c.errors[now]++
	}
	c.cleanup(now)
}

// Increment records a single failure for the current timestamp. Preserved for
// backward compatibility; new callers should prefer RecordOutcome so totals are
// tracked and Rate() returns a meaningful ratio.
func (c *SlidingCounter) Increment() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now().Unix()
	c.totals[now]++
	c.errors[now]++
	c.cleanup(now)
}

// Rate returns the error *ratio* (errors/total) over the window, in [0,1].
// Returns 0 when no requests have been recorded so callers can apply a
// minimum-sample threshold (see circuitBreakerMinRequests) before tripping.
func (c *SlidingCounter) Rate() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now().Unix()
	c.cleanup(now)

	total := 0
	errs := 0
	for _, n := range c.totals {
		total += n
	}
	for _, n := range c.errors {
		errs += n
	}
	if total == 0 {
		return 0
	}
	return float64(errs) / float64(total)
}

// Total returns the total number of recorded requests in the window. Used by
// circuit-breaker logic to enforce a minimum-sample threshold.
func (c *SlidingCounter) Total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now().Unix()
	c.cleanup(now)
	total := 0
	for _, n := range c.totals {
		total += n
	}
	return total
}

// cleanup removes buckets older than the window. It runs at most once per
// second; lastCleanup is now actually maintained (the previous code never
// updated it, so cleanup was effectively dead).
func (c *SlidingCounter) cleanup(now int64) {
	if c.lastCleanup != 0 && now-c.lastCleanup < 1 {
		return
	}
	cutoff := now - int64(c.window.Seconds())
	for ts := range c.errors {
		if ts < cutoff {
			delete(c.errors, ts)
		}
	}
	for ts := range c.totals {
		if ts < cutoff {
			delete(c.totals, ts)
		}
	}
	c.lastCleanup = now
}

// NewWeightedSelector creates a new weighted channel selector.
func NewWeightedSelector() *WeightedSelector {
	return &WeightedSelector{
		channels: make(map[int64]*channelState),
	}
}

// UpdateChannel updates the runtime state for a channel.
func (s *WeightedSelector) UpdateChannel(channel *Channel) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updateChannelLocked(channel)
}

// updateChannelLocked updates channel state. The caller MUST hold s.mu.
func (s *WeightedSelector) updateChannelLocked(channel *Channel) {
	if existing, ok := s.channels[channel.ID]; ok {
		existing.channel = channel
		existing.weight = configuredSelectorWeight(channel)
		if openUntil := selectorCircuitOpenUntil(channel); openUntil > existing.circuitOpenUntil {
			existing.circuitOpenUntil = openUntil
		}
		// Keep runtime state (latency, errors, etc.)
		return
	}

	// Initialize new channel state
	s.channels[channel.ID] = &channelState{
		channel:          channel,
		weight:           configuredSelectorWeight(channel),
		currentWeight:    0,
		recentLatency:    NewSlidingWindow(100),
		recentErrors:     NewSlidingCounter(60 * time.Second),
		maxConcurrent:    100, // Default max concurrent
		circuitOpenUntil: selectorCircuitOpenUntil(channel),
	}
}

func configuredSelectorWeight(channel *Channel) int32 {
	if channel.Weight > 0 {
		return safecast.Uint32ToInt32Saturating(channel.Weight)
	}
	if channel.Priority > 0 {
		return safecast.Int64ToInt32Saturating(channel.Priority)
	}
	return 1
}

func selectorCircuitOpenUntil(channel *Channel) int64 {
	if channel.CircuitOpenedUntil <= 0 {
		return 0
	}
	return time.Unix(channel.CircuitOpenedUntil, 0).UnixNano()
}

// RemoveChannel removes a channel from the selector.
func (s *WeightedSelector) RemoveChannel(channelID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.channels, channelID)
}

// Select implements smooth weighted round-robin with health awareness.
// Algorithm: nginx-style smooth WRR + dynamic weight adjustment.
func (s *WeightedSelector) Select(ctx context.Context, group string, candidates []*Channel) (*Channel, error) {
	if len(candidates) == 0 {
		return nil, ErrChannelNotFound
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Refresh configured channel fields on every selection while preserving
	// runtime health/latency/current-weight state. This makes admin updates
	// visible even when the channel id already exists in the selector.
	for _, ch := range candidates {
		if ch != nil {
			s.updateChannelLocked(ch)
		}
	}

	var best *channelState
	var bestWeight int64 = math.MinInt64

	now := time.Now().UnixNano()

	for _, ch := range candidates {
		if ch == nil {
			continue
		}
		state, ok := s.channels[ch.ID]
		if !ok {
			continue
		}

		// channel-H1: skip open channels; half-open channels (sentinel) are
		// eligible but we arm exactly one probe below.
		if st := state.breakerState(now); st == circuitOpen {
			continue
		}

		// Skip overloaded channels
		inflight := state.inflight.Load()
		if inflight >= state.maxConcurrent {
			continue
		}

		effectiveWeight := channelEffectiveWeight(state)
		state.currentWeight += effectiveWeight
		if state.currentWeight > bestWeight {
			bestWeight = state.currentWeight
			best = state
		}
	}

	if best == nil {
		return nil, ErrChannelNotFound
	}

	// Decrement selected channel's current weight by total effective weight.
	totalWeight := s.totalEffectiveWeight(candidates, now)
	if totalWeight > 0 {
		best.currentWeight -= totalWeight
	}

	// channel-H1: if the chosen channel's open window has elapsed, arm the
	// half-open sentinel so the breaker resolves on the first probe outcome
	// (recordBreakerOutcome in RecordHealth).
	if best.breakerState(now) == circuitHalfOpen {
		best.circuitOpenUntil = circuitHalfOpenSentinel
	}

	best.inflight.Add(1)
	return best.channel, nil
}

// channelEffectiveWeight keeps health/latency factors in fixed-point form so
// low configured weights retain their dynamic ratios instead of rounding to
// the same integer bucket.
func channelEffectiveWeight(state *channelState) int64 {
	if state == nil {
		return 0
	}
	return int64(state.weight) * int64(state.healthFactor()) * int64(state.latencyFactor()) * int64(state.loadFactor())
}

func (s *WeightedSelector) totalEffectiveWeight(candidates []*Channel, now int64) int64 {
	var total int64
	for _, ch := range candidates {
		if ch == nil {
			continue
		}
		if state, ok := s.channels[ch.ID]; ok {
			if state.circuitOpenUntil > 0 && state.circuitOpenUntil > now {
				continue
			}
			if state.inflight.Load() >= state.maxConcurrent {
				continue
			}
			total += channelEffectiveWeight(state)
		}
	}
	return total
}

// RecordHealth records a health check result for a channel.
func (s *WeightedSelector) RecordHealth(channelID int64, success bool, latency int64, err string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, ok := s.channels[channelID]
	if !ok {
		return
	}

	// Update latency
	state.recentLatency.Add(latency)

	// Update error rate (channel-H1: record both totals and errors so the
	// circuit breaker sees a true error ratio, not errors-per-second).
	state.recentErrors.RecordOutcome(success)
	if !success {
		state.lastFailure = time.Now()
	}

	// Decrement in-flight
	state.inflight.Add(-1)

	// channel-H1: if this channel is half-open, the just-recorded outcome is
	// the probe result — close on success, re-open on failure.
	state.recordBreakerOutcome(success)

	// Update circuit breaker state (trip check for closed channels).
	state.updateCircuitBreaker()
}

// healthFactor returns 0-100 based on recent error rate.
func (cs *channelState) healthFactor() int32 {
	errorRate := cs.recentErrors.Rate()
	switch {
	case errorRate < 0.01:
		return 100 // <1% error → full weight
	case errorRate < 0.05:
		return 80 // <5% error → 80% weight
	case errorRate < 0.10:
		return 50 // <10% error → 50% weight
	case errorRate < 0.30:
		return 20 // <30% error → 20% weight
	default:
		return 1 // >30% error → minimal weight
	}
}

// loadFactor proportionally de-rates a channel as its in-flight count climbs
// toward maxConcurrent, so a saturated channel receives less traffic than an
// idle one. Unlike the subscription-account selector (which tracks load in the
// relay gateway, a different process), the channel WeightedSelector owns the
// full in-flight lifecycle in-process: Select increments inflight and
// RecordHealth decrements it, so this factor is live, not inert.
//
// Bands are relative to maxConcurrent (default 100) so the same thresholds apply
// regardless of the configured ceiling: <40% load keeps full weight, ≥90%
// drops to near-zero (1) while the hard inflight>=maxConcurrent skip in Select
// remains the last line of defense. Mirrors the account-side bands (100/80/50/20/1)
// so operators see consistent de-rating. See docs/model-management-design.md §12.2
// and docs/releases/review-v0.11.0.md (Phase D #12).
func (cs *channelState) loadFactor() int32 {
	if cs == nil || cs.maxConcurrent <= 0 {
		return 100
	}
	inflight := cs.inflight.Load()
	if inflight <= 0 {
		return 100
	}
	util := float64(inflight) / float64(cs.maxConcurrent)
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

// latencyFactor returns 50-100 based on p95 latency.
func (cs *channelState) latencyFactor() int32 {
	p95 := cs.recentLatency.P95()
	switch {
	case p95 < 500*time.Millisecond:
		return 100
	case p95 < 2*time.Second:
		return 80
	case p95 < 5*time.Second:
		return 50
	default:
		return 20
	}
}

// Circuit-breaker thresholds (channel-H1). The previous logic tripped on a
// raw errors-per-second value compared against a 0..1 ratio threshold, had no
// minimum-sample guard, and recovered by fully opening the floodgates after a
// fixed timer — so low-traffic channels blew on a couple of errors and a sick
// channel was re-drowned the instant the timer expired.
const (
	circuitBreakerErrorThreshold = 0.5 // trip when >50% of requests fail
	circuitBreakerMinRequests    = 10  // but only after this many samples
	circuitBreakerOpenDuration   = 30 * time.Second
	circuitBreakerHalfOpenProbes = 1 // requests let through while half-open
)

// circuitState is the breaker state of a channel/account: closed (healthy),
// open (failing fast), or halfOpen (probing after the open window elapsed).
type circuitState int

const (
	circuitClosed circuitState = iota
	circuitOpen
	circuitHalfOpen
)

// halfOpenUntil is encoded inside circuitOpenUntil: when the open window
// elapses the channel flips to half-open by setting circuitOpenUntil to a
// sentinel far in the future and arming halfOpenProbes; a probe success closes
// the circuit, a probe failure re-opens it. This keeps the existing
// "circuitOpenUntil > now means skip" invariant in Select intact while adding
// a graduated recovery path.

// updateCircuitBreaker updates the circuit breaker state based on recent errors.
// channel-H1: enforces a minimum-sample threshold and implements a half-open
// probing state instead of unconditionally re-admitting full traffic.
func (cs *channelState) updateCircuitBreaker() {
	now := time.Now().UnixNano()
	switch cs.breakerState(now) {
	case circuitOpen:
		// Still within the open window; nothing to do (Select skips us).
		return
	case circuitHalfOpen:
		// Probe outcome has already been applied by RecordHealth via
		// recordBreakerOutcome. Stay half-open until a probe resolves.
		return
	case circuitClosed:
		// Closed: trip only once we have enough samples AND a high ratio.
		if cs.recentErrors.Total() < circuitBreakerMinRequests {
			return
		}
		if cs.recentErrors.Rate() > circuitBreakerErrorThreshold {
			cs.circuitOpenUntil = now + circuitBreakerOpenDuration.Nanoseconds()
		}
	}
}

// breakerState derives the current breaker state from circuitOpenUntil.
func (cs *channelState) breakerState(now int64) circuitState {
	if cs.circuitOpenUntil <= 0 {
		return circuitClosed
	}
	if cs.circuitOpenUntil == circuitHalfOpenSentinel {
		return circuitHalfOpen
	}
	if cs.circuitOpenUntil > now {
		return circuitOpen
	}
	// Open window elapsed → transition to half-open (armed by Select/Record).
	return circuitHalfOpen
}

// circuitHalfOpenSentinel marks a channel as half-open. It is an arbitrarily
// large timestamp so the "circuitOpenUntil > now" skip in Select keeps working
// until a probe resolves the state.
const circuitHalfOpenSentinel = math.MaxInt64

// recordBreakerOutcome advances the half-open state machine from RecordHealth.
// A probe success closes the circuit; a probe failure re-opens it for another
// full open window. Called under the selector lock.
func (cs *channelState) recordBreakerOutcome(success bool) {
	now := time.Now().UnixNano()
	if cs.breakerState(now) != circuitHalfOpen {
		return
	}
	if success {
		cs.circuitOpenUntil = 0 // half-open → closed
	} else {
		// half-open → reopened
		cs.circuitOpenUntil = now + circuitBreakerOpenDuration.Nanoseconds()
	}
}

// GetState returns the current state of a channel.
func (s *WeightedSelector) GetState(channelID int64) (*channelState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.channels[channelID]
	return state, ok
}

// GetStats returns statistics for all channels.
func (s *WeightedSelector) GetStats() map[int64]ChannelStats {
	s.mu.Lock()
	defer s.mu.Unlock()

	stats := make(map[int64]ChannelStats)
	for id, state := range s.channels {
		stats[id] = ChannelStats{
			ChannelID:     id,
			Weight:        state.weight,
			CurrentWeight: state.currentWeight,
			Inflight:      state.inflight.Load(),
			P95Latency:    state.recentLatency.P95(),
			ErrorRate:     state.recentErrors.Rate(),
			IsCircuitOpen: state.circuitOpenUntil > 0,
		}
	}
	return stats
}

// ChannelStats holds statistics for a channel.
type ChannelStats struct {
	ChannelID     int64
	Weight        int32
	CurrentWeight int64
	Inflight      int32
	P95Latency    time.Duration
	ErrorRate     float64
	IsCircuitOpen bool
}
