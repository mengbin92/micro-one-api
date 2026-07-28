# Performance Baseline

## Test Environment

- Date: 2026-07-17
- Infrastructure: macOS 15.5 / Apple M4 Pro (18-core) / 48 GB RAM
- CPU: Apple M4 Pro (18-core: 6P + 12E)
- Memory: 48 GB
- Go Version: 1.26
- Kratos Version: v2.9.3-0.20260413003801-0284a5bcf92b (fork github.com/Yanhu007/kratos/v2)

## Baseline Metrics

### Phase 0 Baseline (Pre-Refactoring)

> Run the benchmark with: `k6 run scripts/benchmark/k6-baseline.js`

| Metric | Value | Notes |
|--------|-------|-------|
| **P50 Latency** | 12.5 ms | Aggregate across /healthz, /v1/models, /v1/chat/completions |
| **P95 Latency** | 38.0 ms | Aggregate baseline before Phase 1 latency work |
| **P99 Latency** | 62.0 ms | Aggregate baseline before Phase 1 latency work |
| **Error Rate** | 0.12% | Mostly chat 429/500 from mock upstream limits |
| **Throughput** | ~680 | req/s observed on local single-instance run |
| **Active Requests** | 200 peak | 8m staged ramp to 200 VUs |

### Endpoint-Specific Baselines

| Endpoint | P50 | P95 | P99 | Error Rate |
|----------|-----|-----|-----|------------|
| /healthz | 0.5 ms | 1.2 ms | 2.5 ms | 0.00% |
| /v1/models | 8.0 ms | 22.0 ms | 35.0 ms | 0.05% |
| /v1/chat/completions | 28.0 ms | 72.0 ms | 115.0 ms | 0.25% |

### gRPC Service Call Latency

| Service | P50 | P95 | P99 |
|---------|-----|-----|-----|
| identity-service | 3.0 ms | 8.0 ms | 14.0 ms |
| channel-service | 4.0 ms | 11.0 ms | 19.0 ms |
| billing-service | 5.0 ms | 15.0 ms | 28.0 ms |
| log-service | 2.0 ms | 6.0 ms | 12.0 ms |

### Cache Hit Rates (Pre-Implementation)

| Cache Type | L1 Hit Rate | L2 Hit Rate | Miss Rate |
|------------|-------------|-------------|-----------|
| Auth Cache | 78% | 18% | 4% |
| Channel Cache | 65% | 30% | 5% |
| Quota Cache | N/A | N/A | 100% (no cache) |

### Circuit Breaker State

| Service | State | Trips (24h) |
|---------|-------|-------------|
| identity-service | closed | 0 |
| channel-service | closed | 0 |
| billing-service | closed | 0 |
| log-service | closed | 0 |

## Target Metrics (Post-Refactoring)

Based on `docs/design/ARCHITECTURE_REFACTOR.md` §11:

| Metric | Baseline | Target | Improvement |
|--------|----------|--------|-------------|
| P95 Request Latency (no upstream) | 30-50ms | 5-10ms | ~80% |
| gRPC Calls/Request | 5 | 0-1 (cache hit) | ~90% |
| Throughput/Instance | ~500 req/s | ~2000 req/s | 4x |

## Data Source

Phase 0 numbers were recorded with:

- Tool: k6 v0.52.0 (`scripts/benchmark/k6-baseline.js`)
- Local command (example):
  ```bash
  export BASE_URL="http://localhost:8080"
  export API_KEY="sk-test-key"
  k6 run --out json=scripts/benchmark/results/phase0-baseline-2026-07-17.json scripts/benchmark/k6-baseline.js
  ```
- Raw results archived in: `scripts/benchmark/results/phase0-baseline-2026-07-17.json`

## How to Run Baseline Test

### Prerequisites

```bash
# Install k6
brew install k6  # macOS
# or download from https://k6.io/

# Set environment variables
export BASE_URL="http://localhost:8080"
export API_KEY="sk-your-test-key"
```

### Run Test

```bash
# Run baseline test
k6 run --out json=scripts/benchmark/results/results.json scripts/benchmark/k6-baseline.js

# Run with specific duration
k6 run --duration 5m --vus 50 scripts/benchmark/k6-baseline.js

# Generate HTML report
k6 run --out json=scripts/benchmark/results/results.json scripts/benchmark/k6-baseline.js
# Then use a tool like https://github.com/thedevsirk/k6-reporter to generate HTML
```

### Record Results

Update the tables above with the results from your test run.

## Monitoring During Test

While running the baseline test, monitor:

1. **Prometheus Metrics**: http://localhost:9090
2. **Grafana Dashboards**:
   - Relay Gateway Overview
   - Service Dependencies Health
   - Billing Performance
3. **Logs**: Check for any error spikes

## Notes

- Baseline should be run during low-traffic periods for accurate results
- Run multiple times and average the results
- Record the exact configuration used (CPU, memory, etc.)
- Save the raw results JSON files for historical comparison

## History

| Date | Phase | P95 Latency | Throughput | Notes |
|------|-------|-------------|------------|-------|
| 2026-07-17 | Phase 0 | 38.0 ms | ~680 req/s | Local sandbox baseline; see scripts/benchmark/results/phase0-baseline-2026-07-17.json |
| | Phase 1 | TBD | TBD | After P0 fixes |
| | Phase 2 | TBD | TBD | After P1 optimizations |
| 2026-07-28 | v0.11.0 Ph1-3 | TBD | TBD | Selection-event recording + routing metrics added; see v0.11 section below |

## v0.11.0 Phase 3 §3.8: Observability Hot-Path Regression Baseline

> Goal: confirm the Phase 3 selection/execution boundary records
> (SelectionEvent at every Plan() path) and the routing Prometheus metrics
> (§3.5) do NOT regress the relay hot path. Re-run the k6 baseline after
> merging Phase 3 and compare to the Phase 0 row above.

### What changed on the hot path

1. **Plan() boundary**: one SelectionEvent is constructed and emitted on every
   selection path (sticky hit, cross-source, channel-only, subscription-only).
   The event is a stack struct (~15 fields); the recorder is a no-op by default
   and a struct-field write + one function call when wired. No allocation on
   the happy path when the recorder is no-op.
2. **SelectionRecorder**: when wired (production), each event triggers 1-4
   Prometheus `WithLabelValues().Inc()` calls (counter increment, ~50ns each)
   and one structured `zap.Info` call. The structured log is the dominant
   cost; it runs on the relay goroutine and is NOT on the billing async path.
3. **Cross-source weight fix (§3.1)**: replaced `Weight: 1` with
   `subscriptionRouteWeight(account)` — one branch + int64 arithmetic, no
   allocation. Negligible vs. the gRPC selection calls that follow.

### How to measure

```bash
# 1. Before merging Phase 3 (baseline):
k6 run scripts/benchmark/k6-baseline.js --summary-export=phase3-before.json

# 2. After merging Phase 3:
k6 run scripts/benchmark/k6-baseline.js --summary-export=phase3-after.json

# 3. Compare P95/P99 of /v1/chat/completions and billing commit duration.
```

### Metrics to watch (no regression target)

| Metric | Phase 0 baseline | Regression threshold | Phase 3 result |
|--------|------------------|----------------------|-----------------|
| `/v1/chat/completions` P95 | 72.0 ms | ≤ 78 ms (+8%) | TBD |
| `/v1/chat/completions` P99 | 115.0 ms | ≤ 125 ms (+8%) | TBD |
| billing commit P95 | 15.0 ms (gRPC) | ≤ 17 ms (+13%) | TBD |
| billing commit P99 | 28.0 ms (gRPC) | ≤ 31 ms (+11%) | TBD |
| `routing_selection_duration_seconds{source_kind}` P95 | N/A (new) | < 5 ms | TBD |
| Error rate | 0.25% | ≤ 0.30% | TBD |

### If a regression is detected

The SelectionRecorder seam is designed for cheap removal: setting it back to
the no-op recorder (via `RelayUsecase.SetSelectionRecorder(nil)` or simply not
calling it) drops the Plan() overhead to one stack-struct construction + one
method call on a nil-safe interface. The Prometheus metrics stay defined (zero
values), so dashboards don't break. The structured log is the only
non-trivial cost and it can be gated behind a level check (`zap.Info` with a
sampled logger) without code changes.

### Async/best-effort option

If the synchronous log write proves too expensive under high load, the
SelectionRecorder can be wrapped in a buffered async writer (channel + flush
goroutine) without changing the interface — the event struct is already
self-contained and safe to copy.
