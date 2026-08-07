# relay-gateway performance benchmark

Deterministic, reproducible HTTP-layer benchmark for relay-gateway, using k6
against a fixed mock upstream. All gRPC service latency, billing commit, routing
selection, cache hit rate, and circuit-breaker metrics are collected **from
Prometheus**, not k6 — see `docs/design/BASELINE.md` for the full methodology.

## Files

| File | Purpose |
|------|---------|
| `mock-upstream/main.go` | Deterministic, dependency-free OpenAI-compatible upstream. Fixed IDs, fixed token counts, configurable delay. |
| `k6-baseline.js` | k6 load profile hitting `/healthz`, `/v1/models`, `/v1/chat/completions`. Per-endpoint metrics, corrected throughput. |
| `k6-relay-subscription-stress.js` | Pre-prod stress test for subscription-account paths (session sticky, failover, concurrency). |
| `results/` | Archived raw k6 samples and aggregate summary JSON files. |

## What the k6 baseline measures (and what it doesn't)

**Measures (k6, HTTP layer):**

- Per-endpoint latency P50/P90/P95/P99 (`healthz_duration_ms`,
  `models_duration_ms`, `chat_duration_ms`).
- Throughput in req/s (`http_reqs.values.rate` — the correct k6 native rate,
  not count/avg-duration). The workload uses `ramping-arrival-rate`; one
  iteration emits three requests, so the target iteration rate and observed
  request rate must not be conflated.
- Error rate, where 5xx is always a failure and 429 is tracked separately,
  never counted as success.

**Does NOT measure (collect from Prometheus):**

| Metric | Prometheus query |
|--------|-----------------|
| gRPC service latency | `histogram_quantile(0.95, sum(rate(micro_one_api_dependency_grpc_latency_seconds_bucket[5m])) by (le, service))` |
| billing commit latency | `histogram_quantile(0.95, sum(rate(micro_one_api_billing_commit_duration_seconds_bucket[5m])) by (le, mode))` |
| routing selection latency | `histogram_quantile(0.95, sum(rate(micro_one_api_routing_selection_duration_seconds_bucket[5m])) by (le, source_kind))` |
| cache hit rate | See `docs/design/BASELINE.md` for separate generic L1/L2 and billing quota-cache queries. |
| circuit breaker state | `micro_one_api_resilience_circuit_breaker_state` |

## Quick start

```bash
# 1. Start the mock upstream (terminal 1)
make benchmark-mock

# 2. Start relay-gateway with its channel upstream pointing at the mock
#    (terminal 2). The channel's provider base URL must be
#    http://127.0.0.1:18099 (the mock upstream default).

# 3. Run the benchmark (terminal 3)
export BASE_URL=http://localhost:8080
export API_KEY=sk-your-test-key
make benchmark-baseline
```

Raw k6 samples and the aggregate summary are archived separately:

```text
scripts/benchmark/results/raw-<sha>-<timestamp>.json
scripts/benchmark/results/summary-<sha>-<timestamp>.json
```

The summary should be committed or uploaded as a CI artifact; raw samples may
remain a CI artifact when they are too large for Git.

## Smoke mode

`SMOKE=1` collapses the six hard-coded stages (6m+) into a ~35s run, for
verifying the script end-to-end on any machine before a full-length run:

```bash
BASE_URL=$BASE_URL API_KEY=$API_KEY SMOKE=1 \
  ITERATION_START_RATE=5 ITERATION_TARGET_RATE=20 \
  k6 run scripts/benchmark/k6-baseline.js
```

The SMOKE profile is NOT comparable to the full stages — it exists only to
verify syntax, summary export and raw output.

## Known k6 quirks

- `make benchmark-baseline` writes the summary via k6's native
  `--summary-export` flag (the old `RESULTS_FILE=` env var is not read by k6
  and produced no file).
- On some k6 versions (observed: v2.1.0 devel), the `--summary-export` JSON
  reports rate metrics with `passes`/`fails` swapped and unreliable
  `thresholds` entries. Trust the stdout summary and the process exit code
  (0 = all thresholds passed). Use the same k6 version for all comparison runs.

## Reproducibility requirements

Performance conclusions must be based on runs on **Linux/amd64** with:

- Same machine / same CPU / same RAM.
- Same relay-gateway configuration and test data.
- Same mock upstream (this directory, fixed delay).
- Each Git version (Phase 0 / v0.16.0 / develop) run at least 3 times.

Do not draw performance conclusions from Apple Silicon (arm64) runs alone —
local results are useful for smoke-testing the script, not for comparison.

When comparing Phase 0 or `v0.16.0`, keep this benchmark harness at the current
P3.1 commit and use the historical Git SHA only for the service worktree. Those
historical versions do not contain `make benchmark-mock` or the mock-upstream
source.
