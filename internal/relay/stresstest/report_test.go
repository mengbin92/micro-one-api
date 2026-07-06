package stresstest

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestReport_LatencyAndSuccess(t *testing.T) {
	r := NewRecorder(8)
	r.Record(Attempt{Duration: 10 * time.Millisecond, Succeeded: true})
	r.Record(Attempt{Duration: 20 * time.Millisecond, Succeeded: true})
	r.Record(Attempt{Duration: 30 * time.Millisecond, Succeeded: true})
	r.Record(Attempt{Duration: 100 * time.Millisecond, Retryable: true, FailoverReason: "5xx"})
	rp := r.Report(ConcurrencyCap{Limit: 0})
	if rp.SuccessRate() != 0.75 {
		t.Fatalf("success rate = %.4f, want 0.75", rp.SuccessRate())
	}
	if rp.Percentile(50) < 10 || rp.Percentile(50) > 20 {
		t.Fatalf("p50 = %.3f, want ~10-20ms", rp.Percentile(50))
	}
	if rp.Percentile(95) < 99 {
		t.Fatalf("p95 = %.3f, want >= 99ms", rp.Percentile(95))
	}
	if rp.FailoverSwitched != 1 || rp.FailoverReasons["5xx"] != 1 {
		t.Fatalf("failover = %d/%v, want 1/{5xx:1}", rp.FailoverSwitched, rp.FailoverReasons)
	}
}

func TestReport_ConcurrencyPeakAndCap(t *testing.T) {
	r := NewRecorder(64)
	r.Record(Attempt{InflightAtAttempt: 2, Succeeded: true})
	r.Record(Attempt{InflightAtAttempt: 5, Succeeded: true})
	r.Record(Attempt{InflightAtAttempt: 1, Succeeded: true})
	rp := r.Report(ConcurrencyCap{Limit: 4, Account: 42})
	if rp.ConcurrencyPeak != 5 {
		t.Fatalf("peak = %d, want 5", rp.ConcurrencyPeak)
	}
	if rp.ConcurrencyWithinCap() {
		t.Fatalf("peak 5 must exceed cap 4")
	}
	rp2 := r.Report(ConcurrencyCap{Limit: 5, Account: 42})
	if !rp2.ConcurrencyWithinCap() {
		t.Fatalf("peak 5 must be within cap 5")
	}
	// Unlimited cap is always satisfied.
	rp3 := r.Report(ConcurrencyCap{Limit: 0})
	if !rp3.ConcurrencyWithinCap() {
		t.Fatalf("unlimited cap must always be within cap")
	}
}

func TestReport_PrintContainsAllMetrics(t *testing.T) {
	r := NewRecorder(2)
	r.Record(Attempt{Duration: 5 * time.Millisecond, Succeeded: true})
	r.Record(Attempt{Duration: 7 * time.Millisecond, Retryable: true, FailoverReason: "429", RedisFallback: "acquire_error"})
	var buf bytes.Buffer
	r.Report(ConcurrencyCap{Limit: 2, Account: 1}).Print(&buf)
	out := buf.String()
	for _, key := range []string{"success_rate", "latency_ms_p50", "latency_ms_p95", "latency_ms_p99", "failover_switched", "redis_fallback_total", "concurrency_peak", "concurrency_cap", "concurrency_within_cap"} {
		if !strings.Contains(out, key) {
			t.Fatalf("report missing %q:\n%s", key, out)
		}
	}
	if !strings.Contains(out, "429=1") {
		t.Fatalf("report should include failover reason tally:\n%s", out)
	}
	if !strings.Contains(out, "acquire_error=1") {
		t.Fatalf("report should include redis fallback reason tally:\n%s", out)
	}
}
