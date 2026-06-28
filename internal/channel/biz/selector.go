package biz

import (
	"context"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	commonv1 "micro-one-api/api/common/v1"
)

// WeightedSelector selects channels using a weighted round-robin
// algorithm that considers response time, success rate, and configured weight.
type WeightedSelector struct {
	mu       sync.Mutex
	channels map[int64]*channelState // channelID → runtime state
}

// channelState holds runtime state for a channel.
type channelState struct {
	channel          *commonv1.ChannelInfo
	weight           uint32          // configured weight
	currentWeight    int32           // smooth WRR current weight
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

	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	idx := int(math.Ceil(float64(n)*0.95)) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= n {
		idx = n - 1
	}
	return time.Duration(sorted[idx])
}

// SlidingCounter tracks recent error counts in per-second buckets.
type SlidingCounter struct {
	mu          sync.Mutex
	counts      map[int64]int // timestamp (unix seconds) → count
	window      time.Duration
	lastCleanup int64 // unix seconds of last cleanup; initialized on first use
}

// NewSlidingCounter creates a new sliding counter.
func NewSlidingCounter(window time.Duration) *SlidingCounter {
	return &SlidingCounter{
		counts: make(map[int64]int),
		window: window,
	}
}

// Increment increments the counter for the current timestamp.
func (c *SlidingCounter) Increment() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now().Unix()
	c.counts[now]++
	c.cleanup(now)
}

// Rate returns the error rate over the window as errors-per-second.
func (c *SlidingCounter) Rate() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now().Unix()
	c.cleanup(now)

	if len(c.counts) == 0 {
		return 0
	}

	total := 0
	for _, count := range c.counts {
		total += count
	}

	return float64(total) / c.window.Seconds()
}

// cleanup removes buckets older than the window. It runs at most once per
// second; lastCleanup is now actually maintained (the previous code never
// updated it, so cleanup was effectively dead).
func (c *SlidingCounter) cleanup(now int64) {
	if c.lastCleanup != 0 && now-c.lastCleanup < 1 {
		return
	}
	cutoff := now - int64(c.window.Seconds())
	for ts := range c.counts {
		if ts < cutoff {
			delete(c.counts, ts)
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
func (s *WeightedSelector) UpdateChannel(channel *commonv1.ChannelInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updateChannelLocked(channel)
}

// updateChannelLocked updates channel state. The caller MUST hold s.mu.
func (s *WeightedSelector) updateChannelLocked(channel *commonv1.ChannelInfo) {
	if existing, ok := s.channels[channel.Id]; ok {
		existing.channel = channel
		existing.weight = configuredSelectorWeight(channel)
		if openUntil := selectorCircuitOpenUntil(channel); openUntil > existing.circuitOpenUntil {
			existing.circuitOpenUntil = openUntil
		}
		// Keep runtime state (latency, errors, etc.)
		return
	}

	// Initialize new channel state
	s.channels[channel.Id] = &channelState{
		channel:          channel,
		weight:           configuredSelectorWeight(channel),
		currentWeight:    0,
		recentLatency:    NewSlidingWindow(100),
		recentErrors:     NewSlidingCounter(60 * time.Second),
		maxConcurrent:    100, // Default max concurrent
		circuitOpenUntil: selectorCircuitOpenUntil(channel),
	}
}

func configuredSelectorWeight(channel *commonv1.ChannelInfo) uint32 {
	if channel.Weight > 0 {
		return channel.Weight
	}
	if channel.Priority > 0 {
		return uint32(channel.Priority)
	}
	return 1
}

func selectorCircuitOpenUntil(channel *commonv1.ChannelInfo) int64 {
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
func (s *WeightedSelector) Select(ctx context.Context, group string, candidates []*commonv1.ChannelInfo) (*commonv1.ChannelInfo, error) {
	if len(candidates) == 0 {
		return nil, ErrChannelNotFound
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Update channel states
	for _, ch := range candidates {
		if _, ok := s.channels[ch.Id]; !ok {
			s.updateChannelLocked(ch)
		}
	}

	var best *channelState
	var bestWeight int32 = math.MinInt32

	now := time.Now().UnixNano()

	for _, ch := range candidates {
		state, ok := s.channels[ch.Id]
		if !ok {
			continue
		}

		// Skip circuit-opened channels
		if state.circuitOpenUntil > 0 && state.circuitOpenUntil > now {
			continue
		}

		// Skip overloaded channels
		inflight := state.inflight.Load()
		if inflight >= state.maxConcurrent {
			continue
		}

		// Dynamic weight = static weight × health factor × latency factor
		dynamicWeight := int32(state.weight) * state.healthFactor() * state.latencyFactor()

		// Smooth WRR: current += dynamic, track max
		state.currentWeight += dynamicWeight
		if state.currentWeight > bestWeight {
			bestWeight = state.currentWeight
			best = state
		}
	}

	if best == nil {
		return nil, ErrChannelNotFound
	}

	// Decrement selected channel's current weight by total
	totalWeight := s.totalWeight(candidates)
	if totalWeight > 0 {
		best.currentWeight -= totalWeight
	}

	best.inflight.Add(1)
	return best.channel, nil
}

// totalWeight calculates the total weight of all candidates.
func (s *WeightedSelector) totalWeight(candidates []*commonv1.ChannelInfo) int32 {
	var total int32
	for _, ch := range candidates {
		if state, ok := s.channels[ch.Id]; ok {
			total += int32(state.weight)
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

	// Update error rate
	if !success {
		state.recentErrors.Increment()
		state.lastFailure = time.Now()
	}

	// Decrement in-flight
	state.inflight.Add(-1)

	// Update circuit breaker state
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

// updateCircuitBreaker updates the circuit breaker state based on recent errors.
func (cs *channelState) updateCircuitBreaker() {
	errorRate := cs.recentErrors.Rate()

	// Trip circuit breaker if error rate is very high
	if errorRate > 0.5 {
		// Open circuit for 30 seconds
		cs.circuitOpenUntil = time.Now().UnixNano() + (30 * time.Second).Nanoseconds()
	}

	// Reset if time has passed
	now := time.Now().UnixNano()
	if cs.circuitOpenUntil > 0 && cs.circuitOpenUntil < now {
		cs.circuitOpenUntil = 0
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
	Weight        uint32
	CurrentWeight int32
	Inflight      int32
	P95Latency    time.Duration
	ErrorRate     float64
	IsCircuitOpen bool
}
