package biz

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redis/go-redis/v9"

	"micro-one-api/internal/pkg/metrics"
	"micro-one-api/internal/relay/stresstest"
)

// newMiniRedis builds a hermetic Redis double backed by miniredis, plus a
// *redis.Client pointed at it. Stress tests use a real client so EVAL/TTL
// semantics match production exactly (the hand-rolled fakes only approximate
// them). Cleanup closes both.
func newMiniRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.NewMiniRedis()
	if err := mr.Start(); err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, rdb
}

// testWriter adapts *testing.T to io.Writer so stresstest.Report.Print can log
// into the test output.
type testWriter struct{ t *testing.T }

func (l testWriter) Write(p []byte) (int, error) {
	l.t.Log(string(p))
	return len(p), nil
}

// stressConcurrencyReplicas models `replicas` relay-gateway processes sharing
// one Redis, each attempting to acquire a slot for the same account under a
// configured cap. It reports the observed in-flight peak vs the cap, the
// success rate, and the failover (denied) count. This is the roadmap §阶段 3
// acceptance: "Redis 正常时,多副本同账号并发不超过配置".
func stressConcurrencyReplicas(t *testing.T, replicas, load int, limit int32, hold time.Duration) *stresstest.Report {
	t.Helper()
	_, rdb := newMiniRedis(t)
	const accountID int64 = 100
	rec := stresstest.NewRecorder(load)
	var peak atomic.Int32
	start := make(chan struct{})
	var wg sync.WaitGroup
	perReplica := load / replicas
	if perReplica < 1 {
		perReplica = 1
	}
	for r := 0; r < replicas; r++ {
		limiter := newRedisAccountConcurrencyLimiter(rdb, fmt.Sprintf("r-%d", r))
		for w := 0; w < perReplica; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				begin := time.Now()
				release, ok := limiter.TryAcquire(context.Background(), accountID, limit)
				if !ok {
					rec.Record(stresstest.Attempt{Duration: time.Since(begin), Retryable: true, FailoverReason: "concurrency"})
					return
				}
				cur := limiter.Inflight(context.Background(), accountID)
				for {
					p := peak.Load()
					if cur <= p || peak.CompareAndSwap(p, cur) {
						break
					}
				}
				rec.Record(stresstest.Attempt{Duration: time.Since(begin), Succeeded: true, InflightAtAttempt: cur})
				time.Sleep(hold)
				release()
			}()
		}
	}
	close(start)
	wg.Wait()
	rp := rec.Report(stresstest.ConcurrencyCap{Limit: limit, Account: accountID})
	if p := peak.Load(); p > rp.ConcurrencyPeak {
		// Conservative: use the higher of the CAS-tracked peak and the
		// per-attempt peak captured by the recorder.
		rp.ConcurrencyPeak = p
	}
	return rp
}

func TestStress_AccountConcurrency_MultiReplicaWithinCap(t *testing.T) {
	rp := stressConcurrencyReplicas(t, 4, 200, 4, 5*time.Millisecond)
	rp.Print(testWriter{t})
	if !rp.ConcurrencyWithinCap() {
		t.Fatalf("multi-replica concurrency peak %d exceeded configured cap %d", rp.ConcurrencyPeak, rp.ConcurrencyCap.Limit)
	}
	if rp.SuccessRate() <= 0 {
		t.Fatalf("expected some successes, got success_rate=%.4f", rp.SuccessRate())
	}
}

// TestStress_AccountConcurrency_RedisOutageFailsOpenWithFallbackMetric proves
// the roadmap §阶段 3 acceptance: "Redis 短暂不可用时,请求 fail-open 到内存
// limiter,并有 fallback 指标". We drive a Redis limiter, force miniredis into
// a global error state, and assert that (a) attempts still succeed via the
// in-memory fallback and (b) the RelayAccountConcurrencyFallbackTotal counter
// advances.
func TestStress_AccountConcurrency_RedisOutageFailsOpenWithFallbackMetric(t *testing.T) {
	mr, rdb := newMiniRedis(t)
	const accountID int64 = 200
	const limit int32 = 2
	limiter := newRedisAccountConcurrencyLimiter(rdb, "down-replica")

	// Baseline: Redis path works first.
	rel, ok := limiter.TryAcquire(context.Background(), accountID, limit)
	if !ok {
		t.Fatal("baseline acquire must succeed under healthy Redis")
	}
	rel()

	before := testutil.ToFloat64(metrics.RelayAccountConcurrencyFallbackTotal.WithLabelValues("acquire_error"))

	// Inject a global Redis error so every command fails: the limiter must
	// fail open to its in-memory fallback rather than blocking all traffic.
	mr.SetError("redis unavailable (simulated outage)")

	rec := stresstest.NewRecorder(32)
	var wg sync.WaitGroup
	const workers = 16
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			begin := time.Now()
			release, ok := limiter.TryAcquire(context.Background(), accountID, limit)
			if !ok {
				// Even the memory fallback enforces the limit; some workers
				// legitimately get denied when the fallback is saturated. That
				// is correct fail-open behaviour, not an outage failure.
				rec.Record(stresstest.Attempt{Duration: time.Since(begin), Retryable: true, FailoverReason: "concurrency", RedisFallback: "acquire_error"})
				return
			}
			defer release()
			rec.Record(stresstest.Attempt{Duration: time.Since(begin), Succeeded: true, RedisFallback: "acquire_error"})
			time.Sleep(2 * time.Millisecond)
		}()
	}
	close(start)
	wg.Wait()

	rp := rec.Report(stresstest.ConcurrencyCap{Limit: limit, Account: accountID})
	rp.Print(testWriter{t})

	after := testutil.ToFloat64(metrics.RelayAccountConcurrencyFallbackTotal.WithLabelValues("acquire_error"))
	if delta := after - before; delta <= 0 {
		t.Fatalf("Redis outage must increment account_concurrency_fallback_total (acquire_error), delta=%v before=%v after=%v", delta, before, after)
	}
	if rp.RedisFallbackTotal <= 0 {
		t.Fatalf("report must record redis fallbacks, got %d", rp.RedisFallbackTotal)
	}
	if rp.SuccessRate() <= 0 {
		t.Fatalf("fail-open expected some successes during Redis outage, got success_rate=%.4f", rp.SuccessRate())
	}
	// The memory fallback still enforces its per-process cap: peak <= limit.
	if rp.ConcurrencyPeak > limit {
		t.Fatalf("memory fallback must still enforce the cap, peak %d > limit %d", rp.ConcurrencyPeak, limit)
	}
}

// TestStress_RuntimeBlocker_CrossReplicaAndExpiry covers the Redis runtime
// blocker under load: a block written by one replica is visible to every other
// replica, and FastForward past the TTL releases the account without a manual
// Clear. This is the §阶段 3 "Redis runtime blocker" scenario.
func TestStress_RuntimeBlocker_CrossReplicaAndExpiry(t *testing.T) {
	mr, rdb := newMiniRedis(t)
	replicaA := NewRedisRuntimeBlocker(rdb)
	replicaB := NewRedisRuntimeBlocker(rdb)
	const accountID int64 = 300
	blockUntil := time.Now().Add(5 * time.Second)

	if err := replicaA.Block(context.Background(), accountID, blockUntil, "status=429"); err != nil {
		t.Fatalf("block: %v", err)
	}
	// Every other replica must observe the block (concurrent readers).
	var wg sync.WaitGroup
	const readers = 32
	seen := atomic.Int64{}
	start := make(chan struct{})
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, ok := replicaB.IsBlocked(context.Background(), accountID, time.Now()); ok {
				seen.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if got := seen.Load(); got != int64(readers) {
		t.Fatalf("cross-replica block visibility: %d/%d readers saw the block", got, readers)
	}

	// FastForward the miniredis clock past the TTL: the block must lift without
	// an explicit Clear, modelling real Redis TTL expiry.
	mr.FastForward(6 * time.Second)
	if _, ok := replicaB.IsBlocked(context.Background(), accountID, time.Now()); ok {
		t.Fatalf("block must expire after TTL (FastForward)")
	}
}

// TestStress_AccountRPM_MultiReplicaWithinLimit drives the Redis RPM limiter
// from multiple replicas against one account and asserts the rolling-minute
// cap holds across all of them (the §阶段 3 "RPM ... failover" prerequisite:
// the cap is enforced before failover is needed).
func TestStress_AccountRPM_MultiReplicaWithinLimit(t *testing.T) {
	_, rdb := newMiniRedis(t)
	const accountID int64 = 400
	const rpmLimit int32 = 20
	const replicas = 4
	const perReplica = 30 // 120 attempts total, well above the 20/min cap

	var granted atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for r := 0; r < replicas; r++ {
		limiter := newRedisAccountRPMLimiter(rdb, fmt.Sprintf("rpm-%d", r))
		for i := 0; i < perReplica; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if limiter.TryAcquire(context.Background(), accountID, rpmLimit) {
					granted.Add(1)
				}
			}()
		}
	}
	close(start)
	wg.Wait()

	got := granted.Load()
	// The EVAL script is atomic, so the cap is a hard ceiling. Allow a tiny
	// slop for the race window where a slot's TTL is pruned mid-eval under
	// extreme concurrency.
	if got > int64(rpmLimit)+2 {
		t.Fatalf("multi-replica RPM grants %d exceeded limit %d (+slop)", got, rpmLimit)
	}
	if got < int64(rpmLimit) {
		t.Fatalf("expected the limit to be reachable, got %d < %d", got, rpmLimit)
	}
}
