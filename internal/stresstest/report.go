// Package stresstest provides a reusable harness for the relay-gateway stress
// tests required by docs/design/subscription-follow-up-roadmap.md §阶段 3 (Relay 稳定性).
//
// It collects per-attempt outcomes into the fixed metric set the roadmap
// demands — success rate, p50/p95/p99 latency, failover count and reasons,
// Redis fallback count, and observed account-concurrency peak vs the configured
// cap — and prints a deterministic report that doubles as a regression gate.
//
// The harness is dependency-free (stdlib only) so both the biz and server
// layers can drive it against either miniredis-backed or in-memory components.
package stresstest

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Attempt captures one relay attempt observed during a stress run. Exactly one
// of Success / Retryable / Failed should describe the terminal outcome.
type Attempt struct {
	// Duration is the wall-clock latency of the attempt (selection through
	// response).
	Duration time.Duration

	// Succeeded marks a 2xx terminal response.
	Succeeded bool
	// Retryable marks a failure that the failover loop is allowed to switch
	// away from (429/5xx/529/concurrency-full/rpm-full/session-window-full).
	Retryable bool
	// Failed marks a non-retryable terminal failure (e.g. exhausted failover).
	Failed bool

	// FailoverReason is set when this attempt triggered a cross-account
	// failover switch. It is the roadmap's "failover 原因" dimension
	// (429/5xx/529/concurrency/rpm/session_window/network_error/...).
	FailoverReason string
	// RedisFallback is set when this attempt degraded from a Redis-backed
	// component to the in-memory fallback (Redis error observed). The reason is
	// the RelayAccountConcurrencyFallbackTotal / RelayAccountRPMFallbackTotal
	// label ("acquire_error", "release_error", "count_error", ...).
	RedisFallback string

	// InflightAtAttempt is the observed in-flight account concurrency at the
	// moment this attempt acquired its slot, if any. Collected so the report can
	// prove the peak never exceeded the configured cap.
	InflightAtAttempt int32
}

// ConcurrencyCap is the configured per-account concurrency limit a run was
// executed under. Stress runs assert that the observed peak never exceeds it.
type ConcurrencyCap struct {
	// Limit is the configured SubscriptionAccount.Concurrency for the account
	// under load (0 = unlimited / unbounded).
	Limit int32
	// Account is the subscription account id the cap applies to.
	Account int64
}

// Report aggregates a set of Attempts into the fixed metric set.
type Report struct {
	Attempts []Attempt

	// FailoverSwitched counts cross-account switches (attempt.Retryable that
	// actually found a sibling). The roadmap's "failover 次数".
	FailoverSwitched int
	// FailoverExhausted counts retryable failures that did NOT find a sibling.
	FailoverExhausted int
	// FailoverReasons tallies switches per reason.
	FailoverReasons map[string]int
	// RedisFallbackTotal tallies Redis degradations.
	RedisFallbackTotal int
	// RedisFallbackReasons tallies them per reason.
	RedisFallbackReasons map[string]int

	// ConcurrencyPeak is the maximum observed InflightAtAttempt.
	ConcurrencyPeak int32
	// ConcurrencyCap carries the configured limit for the assertion.
	ConcurrencyCap ConcurrencyCap
}

// Recorder is a concurrency-safe collector that builds a Report.
type Recorder struct {
	mu                  sync.Mutex
	attempts            []Attempt
	failoverSwitched    int64
	failoverExhausted   int64
	failoverReasons     map[string]int
	redisFallbackTotal  int64
	redisFallbackReason map[string]int
	concurrencyPeak     atomic.Int32
}

// NewRecorder returns an empty Recorder sized for the expected attempt count.
func NewRecorder(capacity int) *Recorder {
	if capacity < 0 {
		capacity = 0
	}
	return &Recorder{
		attempts:            make([]Attempt, 0, capacity),
		failoverReasons:     make(map[string]int),
		redisFallbackReason: make(map[string]int),
	}
}

// Record appends one attempt. Safe for concurrent use by worker goroutines.
func (r *Recorder) Record(a Attempt) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.attempts = append(r.attempts, a)
	if a.FailoverReason != "" {
		// A failover event is "switched" when the overall outcome was not a
		// hard failure (the request either succeeded via the sibling, or the
		// attempt was denied/retryable and would switch). It is "exhausted"
		// only when the request terminally failed (no sibling could serve it).
		if a.Failed {
			r.failoverExhausted++
		} else {
			r.failoverSwitched++
		}
		r.failoverReasons[a.FailoverReason]++
	}
	if a.RedisFallback != "" {
		r.redisFallbackTotal++
		r.redisFallbackReason[a.RedisFallback]++
	}
	r.mu.Unlock()
	if a.InflightAtAttempt > 0 {
		for {
			cur := r.concurrencyPeak.Load()
			if a.InflightAtAttempt <= cur || r.concurrencyPeak.CompareAndSwap(cur, a.InflightAtAttempt) {
				break
			}
		}
	}
}

// Report materializes the aggregated report. capInfo is the configured
// concurrency cap the run was executed under; it is only used for the
// ConcurrencyCap field / assertion, never to recompute the peak.
func (r *Recorder) Report(capInfo ConcurrencyCap) *Report {
	if r == nil {
		return &Report{ConcurrencyCap: capInfo}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := &Report{
		Attempts:             append([]Attempt(nil), r.attempts...),
		FailoverSwitched:     int(r.failoverSwitched),
		FailoverExhausted:    int(r.failoverExhausted),
		FailoverReasons:      cloneMap(r.failoverReasons),
		RedisFallbackTotal:   int(r.redisFallbackTotal),
		RedisFallbackReasons: cloneMap(r.redisFallbackReason),
		ConcurrencyPeak:      r.concurrencyPeak.Load(),
		ConcurrencyCap:       capInfo,
	}
	return out
}

func cloneMap(m map[string]int) map[string]int {
	if len(m) == 0 {
		return map[string]int{}
	}
	cp := make(map[string]int, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

// SuccessRate is the fraction of attempts that succeeded.
func (rp *Report) SuccessRate() float64 {
	if rp == nil || len(rp.Attempts) == 0 {
		return 0
	}
	var ok int
	for _, a := range rp.Attempts {
		if a.Succeeded {
			ok++
		}
	}
	return float64(ok) / float64(len(rp.Attempts))
}

// Latencies returns the sorted attempt latencies in milliseconds.
func (rp *Report) Latencies() []float64 {
	if rp == nil {
		return nil
	}
	out := make([]float64, 0, len(rp.Attempts))
	for _, a := range rp.Attempts {
		out = append(out, float64(a.Duration.Microseconds())/1000.0)
	}
	sort.Float64s(out)
	return out
}

// Percentile returns the p-th latency percentile (0 < p <= 100) in ms, using
// nearest-rank interpolation. Returns 0 when there are no samples.
func (rp *Report) Percentile(p float64) float64 {
	lat := rp.Latencies()
	if len(lat) == 0 {
		return 0
	}
	if p <= 0 {
		return lat[0]
	}
	if p >= 100 {
		return lat[len(lat)-1]
	}
	// Nearest-rank: index = ceil(p/100 * N) - 1, clamped.
	idx := int(math.Ceil(p/100.0*float64(len(lat)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(lat) {
		idx = len(lat) - 1
	}
	return lat[idx]
}

// ConcurrencyWithinCap reports whether the observed peak never exceeded the
// configured cap. A zero / negative configured limit means "unlimited" and is
// always satisfied.
func (rp *Report) ConcurrencyWithinCap() bool {
	if rp == nil {
		return true
	}
	if rp.ConcurrencyCap.Limit <= 0 {
		return true
	}
	return rp.ConcurrencyPeak <= rp.ConcurrencyCap.Limit
}

// Print writes a fixed-format human-readable summary to w, including every
// roadmap-mandated metric. It is deterministic so a snapshot diff can gate CI.
func (rp *Report) Print(w io.Writer) {
	if rp == nil {
		return
	}
	n := len(rp.Attempts)
	var succeeded int
	for _, a := range rp.Attempts {
		if a.Succeeded {
			succeeded++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "attempts:               %d\n", n)
	fmt.Fprintf(&b, "succeeded:              %d\n", succeeded)
	fmt.Fprintf(&b, "success_rate:           %.4f\n", rp.SuccessRate())
	fmt.Fprintf(&b, "latency_ms_p50:         %.3f\n", rp.Percentile(50))
	fmt.Fprintf(&b, "latency_ms_p95:         %.3f\n", rp.Percentile(95))
	fmt.Fprintf(&b, "latency_ms_p99:         %.3f\n", rp.Percentile(99))
	fmt.Fprintf(&b, "failover_switched:      %d\n", rp.FailoverSwitched)
	fmt.Fprintf(&b, "failover_exhausted:     %d\n", rp.FailoverExhausted)
	fmt.Fprintf(&b, "failover_reasons:       %s\n", mapToCSV(rp.FailoverReasons))
	fmt.Fprintf(&b, "redis_fallback_total:   %d\n", rp.RedisFallbackTotal)
	fmt.Fprintf(&b, "redis_fallback_reasons: %s\n", mapToCSV(rp.RedisFallbackReasons))
	fmt.Fprintf(&b, "concurrency_peak:       %d\n", rp.ConcurrencyPeak)
	if rp.ConcurrencyCap.Limit > 0 {
		fmt.Fprintf(&b, "concurrency_cap:        %d\n", rp.ConcurrencyCap.Limit)
		fmt.Fprintf(&b, "concurrency_within_cap: %v\n", rp.ConcurrencyWithinCap())
	} else {
		fmt.Fprintf(&b, "concurrency_cap:        unlimited\n")
		fmt.Fprintf(&b, "concurrency_within_cap: true\n")
	}
	_, _ = io.WriteString(w, b.String())
}

// AssertStrings is the canonical expectation strings used by stress test
// assertions; tests call these so the roadmap wording stays in one place.
func (rp *Report) AssertStrings() (summary []string) {
	if rp == nil {
		return nil
	}
	summary = append(summary, fmt.Sprintf("success_rate=%.4f", rp.SuccessRate()))
	summary = append(summary, fmt.Sprintf("p95_ms=%.3f", rp.Percentile(95)))
	summary = append(summary, fmt.Sprintf("p99_ms=%.3f", rp.Percentile(99)))
	summary = append(summary, fmt.Sprintf("failover_switched=%d", rp.FailoverSwitched))
	summary = append(summary, fmt.Sprintf("failover_exhausted=%d", rp.FailoverExhausted))
	summary = append(summary, fmt.Sprintf("redis_fallback=%d", rp.RedisFallbackTotal))
	if rp.ConcurrencyCap.Limit > 0 {
		summary = append(summary, fmt.Sprintf("concurrency_peak=%d<=cap=%d(%v)", rp.ConcurrencyPeak, rp.ConcurrencyCap.Limit, rp.ConcurrencyWithinCap()))
	} else {
		summary = append(summary, fmt.Sprintf("concurrency_peak=%d(cap=unlimited)", rp.ConcurrencyPeak))
	}
	return summary
}

func mapToCSV(m map[string]int) string {
	if len(m) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%s=%d", k, m[k])
	}
	return b.String()
}
