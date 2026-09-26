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
| `summarize-regression.py` | Requires at least three baseline and candidate summaries, reports medians, and applies the 20% engineering gate. |
| `dashboard-index.py` | Builds one deterministic SQLite fixture and compares the old and new Dashboard indexes with identical queries. |
| `run-q3-ci.sh` / `q3-fixture.py` | Recreate old and current stacks on one Linux/amd64 runner, seed the same HTTP fixture, and run three full profiles each. |
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
  `--summary-export` flag. The CI comparison supplies `RESULTS_FILE` directly
  to `k6-baseline.js`, whose `handleSummary` writes the full summary there.
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

## Median and regression gate

Use at least three full summaries for each version. The gate compares medians,
rejects more than 20% latency or throughput regression, rejects a non-zero
dropped-iteration median, and caps the HTTP error rate at 1%:

```bash
python3 scripts/benchmark/summarize-regression.py \
  --baseline /path/to/baseline-1.json \
  --baseline /path/to/baseline-2.json \
  --baseline /path/to/baseline-3.json \
  --candidate /path/to/candidate-1.json \
  --candidate /path/to/candidate-2.json \
  --candidate /path/to/candidate-3.json \
  --output /path/to/regression-report.json
```

This is an engineering admission line, not a statistical significance claim.
Keep the machine, service data, configuration, k6 version, mock upstream and
arrival-rate profile fixed. If a representative protocol or execution path
changes, update the fixture before accepting a new baseline.

## Q3 Linux/amd64 CI comparison

The `Q3 Linux amd64 comparison` workflow runs on demand and when the workflow
file or this harness changes on `develop`. It checks out the pinned release
baseline (`Q3_BASELINE_REF`, currently `v0.32.2`) and the pushed candidate
into separate worktrees, then builds and runs each stack in the same
`ubuntu-24.04` job. Both use the same k6 v0.54.0 image, harness, fixed 2ms
mock, synthetic user/channel/token fixture and full eight-minute arrival
profile (`ITERATION_TARGET_RATE=10`). MySQL and Redis volumes are recreated
between versions. Transaction samples are cleared before each run.

The baseline ref is bumped to the latest release tag on every release; the
comparison is only meaningful within this job. The archived `ff518b1`
baseline was retired on 2026-09-26 after the first CI comparison measured a
consistent +25% chat-P95 regression accumulated over the 436 commits between
v0.17.1 and v0.32.2 — see `docs/runbooks/q3-rebaseline-2026-09-26.md`.

The artifact contains runner and commit fingerprints, three raw JSON streams
and three full summaries per version, Compose build logs, and the median
regression report. The runner can have different CPU and memory from the
archived 2026-08-10 host; compare the two versions **within this CI job**,
not their absolute latency against the archived host. The workflow's
20% threshold is an engineering gate, not a statistical confidence interval.

## Dashboard index fixture

The Dashboard query-plan check uses the same generated rows, parameters and
aggregate query for both indexes and retains every raw timing sample:

```bash
python3 scripts/benchmark/dashboard-index.py \
  --output /tmp/dashboard-index.json
```

The result proves SQLite plan selection and relative behavior for that fixture.
It is not a substitute for `EXPLAIN ANALYZE` and same-window timing on the
production MySQL shape.
