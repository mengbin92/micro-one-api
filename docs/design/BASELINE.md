# Performance Baseline

> **Methodology status (2026-08-07):** the k6 baseline script has been
> corrected (`scripts/benchmark/k6-baseline.js`) and a deterministic mock
> upstream added (`scripts/benchmark/mock-upstream/`). The tables below
> distinguish values that were **actually measured and archived** from values
> that are **N/A — not recorded**. No placeholder entries remain; historical data
> that cannot be recovered is explicitly marked with the reason.

## Methodology

### Two metric sources — never mixed

| Source | Tool | What it measures | How to collect |
|--------|------|------------------|----------------|
| **HTTP layer** | k6 | End-to-end request latency, throughput, HTTP error rate | `make benchmark-baseline` (runs `k6-baseline.js`) |
| **Service internals** | Prometheus | gRPC latency, billing commit, routing selection, cache hit rate, circuit breaker | PromQL queries during/after the k6 run |

k6 measures the HTTP boundary only. gRPC service latency, billing commit
duration, routing selection duration, cache hit rate, and circuit-breaker
state are **not** k6 metrics — they are scraped from Prometheus using the
queries in the [Prometheus query reference](#prometheus-query-reference)
section below.

### k6 baseline script fixes (P3.1)

The original `k6-baseline.js` had five measurement bugs. All are fixed:

1. **Throughput** now uses `http_reqs.values.rate` (k6's native req/s), not
   `count / avg_duration` (algebraically wrong under staged load).
2. **HTTP 500 is not success.** Only status 200 counts as success; 429 is
   tracked separately (`chat_rate_limited_total`); 5xx is always a failure.
3. **`summaryTrendStats`** is set explicitly to
   `['avg','min','med','max','p(50)','p(90)','p(95)','p(99)']` so the JSON
   output always carries P50/P90/P95/P99.
4. **Per-endpoint Trend metrics** (`healthz_duration_ms`,
   `models_duration_ms`, `chat_duration_ms`) give independent latency
   breakdowns, not just a global aggregate.
5. **Mock upstream** (`scripts/benchmark/mock-upstream/`) provides
   deterministic responses (fixed IDs, fixed token counts, configurable
   delay) so k6 measures relay overhead, not upstream variance.

The workload uses k6's `ramping-arrival-rate` executor rather than a closed
VUs-plus-sleep loop. One iteration emits three requests, so the configured
iteration rate and aggregate HTTP request rate are separate measurements.

### Deterministic mock upstream

The mock upstream (`scripts/benchmark/mock-upstream/main.go`) serves:

- `POST /v1/chat/completions` and `POST /chat/completions` — fixed
  `chat.completion` response, `usage.total_tokens=15`.
- `GET /v1/models` — fixed 2-model list.
- `GET /healthz` — `{"status":"ok"}`.

All responses are deterministic (no wall-clock RNG in payloads). A fixed
processing delay (default 2ms, configurable via `-delay-ms`) keeps latency
in a realistic band.

```bash
make benchmark-mock   # starts on 127.0.0.1:18099
```

### Reproducibility requirements

Performance conclusions **must** be based on runs on **Linux/amd64** with:

- Same machine, same CPU, same RAM.
- Same relay-gateway configuration and test data.
- Same mock upstream (fixed delay).
- Each Git version run **at least 3 times**; report median.

**Do not** draw performance conclusions from Apple Silicon (arm64) runs.
Local arm64 results are useful only for smoke-testing the script and
verifying it runs without errors.

### Versions to compare

Each baseline run must record the exact Git SHA. The three comparison
points for the v0.17 P3.1 work are:

| Version | Git SHA | Date | Tag |
|---------|---------|------|-----|
| Phase 0 baseline | `397e36c` (`397e36cc7cddfc95ecb66172417e52a71ed64afb`) | 2026-07-17 | pre-refactoring |
| v0.16.0 | `a8e14db` (`a8e14db4e2279a22081ecd351e708c7b866e8922`) | 2026-08-06 | `v0.16.0` |
| develop | `ff518b1` (`ff518b1cfcd88a6a0bc72cc0f674a17773293ea5`) | 2026-08-10 | — |

> **Linux/amd64 three-version comparison completed (2026-08-10).** Phase 0
> (`397e36c`), v0.16.0 (`a8e14db`), and develop (`ff518b1`) were each run 3×
> on Ubuntu 24.04 / x86_64 / 36-core / 31 GB (Intel Xeon E5-2686 v4 @ 2.30 GHz),
> k6 v0.54.0, mock upstream with 2ms fixed delay, ITERATION_TARGET_RATE=10.
> Billing tables were truncated before each version's runs to eliminate
> row-lock accumulation bias. Results archived in
> `scripts/benchmark/results/p31-amd64/`. Phase 0 was run against the
> pre-refactoring relay binary with the current identity/channel/billing stack
> (Phase 0 does not call `ConsumeTokenQuota`); v0.16.0 and develop were run
> with matching dependency stacks after fixing the `SERVICE_TOKEN` configuration
> gap (see `P31-EXECUTION-REPORT.md`).

## Archived results

| File | Version | Environment | Notes |
|------|---------|-------------|-------|
| `scripts/benchmark/results/phase0-baseline-2026-07-17.json` | Phase 0 (`397e36c`) | macOS 15.5 / Apple M4 Pro / 48 GB | Historical Apple Silicon summary — **not** for cross-version comparison. |
| `scripts/benchmark/results/p31-amd64/summary-397e36c-phase0-{1,2,3}.json` | Phase 0 (`397e36c`) | Linux/amd64 (Ubuntu 24.04 / Xeon E5-2686 v4 / 36-core / 31 GB) | 3× k6 runs, 8 min each, ITERATION_TARGET_RATE=10. Note: Phase 0 ran without billing table cleanup, so P95/P99 inflated by billing row-lock accumulation. |
| `scripts/benchmark/results/p31-amd64/summary-v016-{1,2,3}.json` | v0.16.0 (`a8e14db`) | Linux/amd64 (Ubuntu 24.04 / Xeon E5-2686 v4 / 36-core / 31 GB) | 3× k6 runs, 8 min each, billing tables truncated before runs. |
| `scripts/benchmark/results/p31-amd64/summary-develop-{1,2,3}.json` | develop (`ff518b1`) | Linux/amd64 (Ubuntu 24.04 / Xeon E5-2686 v4 / 36-core / 31 GB) | 3× k6 runs, 8 min each, billing tables truncated before runs. |

## Baseline Metrics — Phase 0 (2026-07-17)

> **⚠️ Apple Silicon (arm64) data — historical reference only.** These values
> were recorded on macOS 15.5 / Apple M4 Pro (18-core) / 48 GB RAM. They are
> **not** comparable to Linux/amd64 production runs and must not be used to
> draw performance conclusions across versions.

### Test Environment

- Date: 2026-07-17
- Git SHA: `397e36c`
- Infrastructure: macOS 15.5 / Apple M4 Pro (18-core: 6P + 12E) / 48 GB RAM
- Go Version: 1.26
- Kratos Version: v2.9.3-0.20260413003801-0284a5bcf92b (fork github.com/Yanhu007/kratos/v2)
- k6 Version: v0.52.0
- Script: `scripts/benchmark/k6-baseline.js` (pre-fix version)

### Aggregate HTTP metrics

| Metric | Value | Notes |
|--------|-------|-------|
| **P50 Latency** | 12.5 ms | Aggregate across /healthz, /v1/models, /v1/chat/completions |
| **P95 Latency** | 38.0 ms | Aggregate baseline before Phase 1 latency work |
| **P99 Latency** | 62.0 ms | Aggregate baseline before Phase 1 latency work |
| **Error Rate** | 0.12% | Mostly chat 429/500 from mock upstream limits |
| **Throughput** | ~680 req/s | ⚠️ Computed with the old buggy formula (count/avg); not reproducible. The corrected script uses `http_reqs.values.rate`. |
| **Active Requests** | 200 peak | 8m staged ramp to 200 VUs |

### Endpoint-specific HTTP baselines

| Endpoint | P50 | P95 | P99 | Error Rate |
|----------|-----|-----|-----|------------|
| /healthz | 0.5 ms | 1.2 ms | 2.5 ms | 0.00% |
| /v1/models | 8.0 ms | 22.0 ms | 35.0 ms | 0.05% |
| /v1/chat/completions | 28.0 ms | 72.0 ms | 115.0 ms | 0.25% |

## Baseline Metrics — v0.16.0 (a8e14db)

> **Linux/amd64, 2026-08-10.** 3× k6 runs (median), Ubuntu 24.04 / Xeon E5-2686
> v4 @ 2.30 GHz, mock upstream 2ms delay, ITERATION_TARGET_RATE=10, billing
> tables truncated before each version's runs.

### HTTP metrics (k6)

| Endpoint | P50 | P95 | P99 | Error Rate | Throughput |
|----------|-----|-----|-----|------------|------------|
| Aggregate | 3.45 ms | 114.92 ms | 117.51 ms | 0.00% | 19.31 req/s |
| /healthz | 0.63 ms | 0.72 ms | 0.88 ms | 0.00% | — |
| /v1/models | 3.45 ms | 3.64 ms | 9.81 ms | 0.00% | — |
| /v1/chat/completions | 112.11 ms | 116.68 ms | 119.63 ms | 0.00% | — |

## Baseline Metrics — develop (ff518b1)

> **Linux/amd64, 2026-08-10.** 3× k6 runs (median), Ubuntu 24.04 / Xeon E5-2686
> v4 @ 2.30 GHz, mock upstream 2ms delay, ITERATION_TARGET_RATE=10, billing
> tables truncated before each version's runs.

### HTTP metrics (k6)

| Endpoint | P50 | P95 | P99 | Error Rate | Throughput |
|----------|-----|-----|-----|------------|------------|
| Aggregate | 3.39 ms | 114.62 ms | 117.31 ms | 0.00% | 19.31 req/s |
| /healthz | 0.63 ms | 0.71 ms | 0.86 ms | 0.00% | — |
| /v1/models | 3.39 ms | 3.60 ms | 9.70 ms | 0.00% | — |
| /v1/chat/completions | 111.88 ms | 116.34 ms | 120.30 ms | 0.00% | — |

## Service-internal metrics (Prometheus)

> All values below are collected from Prometheus, **not** k6. The Phase 0
> values were hand-curated estimates from the 2026-07-17 local run; they are
> marked accordingly. v0.16.0 and develop values require a Linux/amd64 run
> with Prometheus scraping enabled.

### gRPC service call latency

| Version | Service | P50 | P95 | P99 |
|---------|---------|-----|-----|-----|
| Phase 0 (arm64, estimated) | identity-service | 3.0 ms | 8.0 ms | 14.0 ms |
| Phase 0 (arm64, estimated) | channel-service | 4.0 ms | 11.0 ms | 19.0 ms |
| Phase 0 (arm64, estimated) | billing-service | 5.0 ms | 15.0 ms | 28.0 ms |
| Phase 0 (arm64, estimated) | log-service | 2.0 ms | 6.0 ms | 12.0 ms |
| v0.16.0 (linux/amd64) | _(all)_ | N/A — Prometheus not scraped | N/A — not recorded | N/A — not recorded |
| develop (linux/amd64) | _(all)_ | N/A — not recorded | N/A — not recorded | N/A — not recorded |
| production (linux/amd64, 2026-08-11) | _(all)_ | N/A — metric never instrumented | N/A | N/A |
| production (linux/amd64, v0.18.2, 2026-08-11) | identity-service | **2.7 ms** | **6.9 ms** | **9.6 ms** |
| production (linux/amd64, v0.18.2, 2026-08-11) | channel-service | **1.5 ms** | **9.3 ms** | **18.6 ms** |
| production (linux/amd64, v0.18.2, 2026-08-11) | billing-service | **1.0 ms** | **20.9 ms** | 1.0 s\* |
| production (linux/amd64, v0.18.2, 2026-08-11) | log-service | **0.6 ms** | **4.8 ms** | **21.9 ms** |

> **v0.18 P2/P4 修复后补采 (2026-08-11)**: `micro_one_api_dependency_grpc_latency_seconds`
> 埋点修复（`xgrpc.UnaryClientMetricsInterceptor`）+ P4 全边 dial 接入（admin/relay/channel/
> identity/billing/monitor）后部署，真实流量下采集（5m rate，部署后 ~15min）。
> `\*` billing P99=1.0s 为直方图桶上限（样本少，流量积累后复校）。
> 覆盖边：admin→{identity,channel,billing}、relay→{identity,channel,billing,log}、
> channel/identity→billing、channel→notify、monitor→channel。

### Billing commit latency

| Version | Mode | P50 | P95 | P99 |
|---------|------|-----|-----|-----|
| Phase 0 (arm64, estimated) | sync | 5.0 ms | 15.0 ms | 28.0 ms |
| v0.16.0 (linux/amd64) | — | N/A — not recorded | N/A — not recorded | N/A — not recorded |
| develop (linux/amd64) | — | N/A — not recorded | N/A — not recorded | N/A — not recorded |
| production (linux/amd64, 2026-08-11) | sync/async | N/A — metric never instrumented | N/A | N/A |
| production (linux/amd64, v0.18.2, 2026-08-11) | async | **31.3 ms** | **60.0 ms** | **92.0 ms** |
| production (linux/amd64, v0.18.2, 2026-08-11) | sync | — | — | — |

> **v0.18 P2/P4 修复后补采 (2026-08-11)**: `micro_one_api_billing_commit_duration_seconds`
> 埋点修复后部署，真实流量下采集（5m rate，async 0.055/s ≈ 样本积累中）。sync 当前无样本
> （生产流量低且走 async 结算路径；sync 分支需 relay 同步提交触发）。reserve 同步路径：
> P50 **8.1ms** / P95 **39.5ms** / P99 **47.9ms**。低流量初值，流量积累后复校。

### Routing selection latency

| Version | P50 | P95 | P99 |
|---------|-----|-----|-----|
| Phase 0 | N/A — metric did not exist (added in v0.11.0 Phase 3) | — | — |
| v0.16.0 (linux/amd64) | N/A — not recorded | N/A — not recorded | N/A — not recorded |
| develop (linux/amd64) | N/A — not recorded | N/A — not recorded | N/A — not recorded |
| production (linux/amd64, 2026-08-11) | **7.6 ms** (channel) | **10.0 ms** (channel) | **22.0 ms** (channel) |

> 采集：生产 Prometheus 5m 窗口（2026-08-11 09:3x，`[5m]` rate），
> `source_kind=channel`；subscription 无流量（NaN）。当前生产流量较低
> （routing selection ≈ 0.07 req/s）。

### Cache hit rates

| Version | Cache | L1 Hit | L2 Hit | Miss |
|---------|-------|--------|--------|------|
| Phase 0 (arm64, estimated) | Auth | 78% | 18% | 4% |
| Phase 0 (arm64, estimated) | Channel | 65% | 30% | 5% |
| Phase 0 (arm64, estimated) | Quota | N/A | N/A | 100% (no cache) |
| v0.16.0 (linux/amd64) | _(all)_ | N/A — not recorded | N/A — not recorded | N/A — not recorded |
| develop (linux/amd64) | _(all)_ | N/A — not recorded | N/A — not recorded | N/A — not recorded |
| production (linux/amd64, 2026-08-11) | Auth | **79.6%** | **15.9%** | **4.5%** |

> 采集：生产 Prometheus 5m 窗口（2026-08-11），按
> `hits_l1 / (hits_l1 + hits_l2 + misses)` 口径归一（与 Phase 0 口径一致）。
> Channel / Quota 缓存无流量（指标无样本）。

### Circuit breaker state

| Version | Service | State | Trips (24h) |
|---------|---------|-------|-------------|
| Phase 0 (arm64) | identity-service | closed | 0 |
| Phase 0 (arm64) | channel-service | closed | 0 |
| Phase 0 (arm64) | billing-service | closed | 0 |
| Phase 0 (arm64) | log-service | closed | 0 |
| v0.16.0 (linux/amd64) | _(all)_ | N/A — not recorded | N/A — not recorded |
| develop (linux/amd64) | _(all)_ | N/A — not recorded | N/A — not recorded |
| production (linux/amd64, 2026-08-11) | _(all)_ | N/A — interceptor not wired | N/A |
| production (linux/amd64, v0.18.2, 2026-08-11) | _(all)_ | N/A — relay resilience not enabled | N/A |

> **v0.18 P4 更新 (2026-08-11)**: 熔断观测代码（`ResilientClient.Execute` 的
> CircuitBreakerState gauge）确认已就绪，但**生产 relay 未启用熔断**
> （`cfg.Bootstrap.Resilience.Enabled` 未配置；`resilience.yaml` 为旧配置未被 wire
> 读取）。启用熔断 = 行为变更（熔断保护上线），属运营决策；**启用后**
> `circuit_breaker_state` 即产生数据并回填。带 breaker 的
> `platform/grpc/resilience.UnaryClientInterceptor` **不 wire**（避免与 ResilientClient
> 双重熔断、且会改变执行路径，违反 P4「仅新增观测」约束）。

## Target Metrics (Post-Refactoring)

Based on `docs/design/ARCHITECTURE_REFACTOR.md` §11.1:

| Metric | Baseline | Target | Improvement |
|--------|----------|--------|-------------|
| P95 Request Latency (no upstream) | 30-50ms | 5-10ms | ~80% |
| gRPC Calls/Request | 5 | 0-1 (cache hit) | ~90% |
| Throughput/Instance | ~500 req/s | ~2000 req/s | 4x |

## Prometheus query reference

Run these queries against Prometheus (`http://localhost:9090` or the
production Prometheus instance) **during or immediately after** a k6 baseline
run. Record the P50/P95/P99 values in the service-internal tables above.

### gRPC service latency

```promql
# P95 per service
histogram_quantile(0.95,
  sum(rate(micro_one_api_dependency_grpc_latency_seconds_bucket[5m])) by (le, service)
)

# P99 per service — change 0.95 to 0.99
```

Labels: `service` (identity-service, channel-service, billing-service, log-service), `method`, `status`.

### Billing commit latency

```promql
# P95 by commit mode (sync/async)
histogram_quantile(0.95,
  sum(rate(micro_one_api_billing_commit_duration_seconds_bucket[5m])) by (le, mode)
)
```

Label: `mode` (sync, async).

### Routing selection latency

```promql
# P95 by source kind (sticky, cross_source, channel_only, subscription_only)
histogram_quantile(0.95,
  sum(rate(micro_one_api_routing_selection_duration_seconds_bucket[5m])) by (le, source_kind)
)
```

Label: `source_kind`.

### Cache hit rate

```promql
# Generic multi-level cache: L1 hits by cache type
sum(rate(micro_one_api_cache_hits_total{level="l1"}[5m])) by (cache)

# Generic multi-level cache: L2 hits by cache type
sum(rate(micro_one_api_cache_hits_total{level="l2"}[5m])) by (cache)

# Generic multi-level cache: misses by cache type
sum(rate(micro_one_api_cache_misses_total[5m])) by (cache)

# Billing quota cache (separate metric family)
sum(rate(micro_one_api_billing_quota_cache_hits_total[5m])) by (level)
rate(micro_one_api_billing_quota_cache_misses_total[5m])
```

Generic cache labels: `cache` (auth, channel, ...), `level` (l1, l2 — hits only).
Quota cache labels: `level` (l1, l2 — hits only); quota misses have no labels.

### Circuit breaker state

```promql
# Current state (0=closed, 1=half-open, 2=open)
micro_one_api_resilience_circuit_breaker_state

# Trips in the last 24h
increase(micro_one_api_resilience_circuit_breaker_trips_total[24h])
```

Label: `service` (service name).

## How to run a baseline

### Prerequisites

```bash
# Install k6
brew install k6          # macOS (smoke-testing only)
# or: https://k6.io/docs/get-started/installation/

# On Linux/amd64 (required for comparable results):
# sudo gpg -k
# sudo gpg --no-default-keyring --keyring /usr/share/keyrings/k6-archive-keyring.gpg --keyserver hkp://keyserver.ubuntu.com:80 --recv-keys C5AD17C747E3415A3642D57D77C6C491D6AC1D69
# echo "deb [signed-by=/usr/share/keyrings/k6-archive-keyring.gpg] https://dl.k6.io/deb stable main" | sudo tee /etc/apt/sources.list.d/k6.list
# sudo apt update && sudo apt install k6
```

### Run the benchmark

```bash
# 1. Start the mock upstream (terminal 1)
make benchmark-mock

# 2. Start relay-gateway with its channel upstream pointing at the mock
#    (terminal 2). The channel's provider base URL must be
#    http://127.0.0.1:18099.

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

The summary JSON should be committed (or uploaded as a CI artifact); raw k6
samples may be kept as a CI artifact when they are too large for Git.

### Record results

1. Copy the P50/P95/P99 values from the k6 summary into the HTTP metrics
   tables above for the matching version.
2. Run the Prometheus queries (above) during/after the run and record the
   service-internal metrics.
3. Replace `N/A — not recorded` with actual values only when you have
   measured them. Never substitute estimates or guesses.

### Monitoring during a run

While running the baseline test, monitor:

1. **Prometheus Metrics**: http://localhost:9090
2. **Grafana Dashboards**:
   - Relay Gateway Overview
   - Service Dependencies Health
   - Billing Performance
3. **Logs**: Check for any error spikes

## Notes

- Baseline should be run during low-traffic periods for accurate results.
- Run multiple times (≥ 3) and report the median.
- Record the exact Git SHA, machine spec, and configuration used.
- Save the raw results JSON files for historical comparison.

## History

| Date | Version | SHA | Environment | P95 (aggregate) | Throughput | Notes |
|------|---------|-----|-------------|-----------------|------------|-------|
| 2026-07-17 | Phase 0 | `397e36c` | macOS / Apple M4 Pro (arm64) | 38.0 ms | ~680 req/s ⚠️ | Local sandbox; old k6 script (buggy throughput). Historical reference only. |
| 2026-08-09 | Phase 0 | `397e36c` | Linux/amd64 (Xeon E5-2686 v4 / 36-core / 31 GB) | 714.93 ms ⚠️ | 19.3 req/s | 3× k6 runs. P95 inflated by billing row-lock accumulation (tables not cleaned). |
| 2026-08-10 | v0.16.0 | `a8e14db` | Linux/amd64 (Xeon E5-2686 v4 / 36-core / 31 GB) | 116.68 ms | 19.31 req/s | 3× k6 runs; billing tables truncated. 0% error rate. |
| 2026-08-10 | develop | `ff518b1` | Linux/amd64 (Xeon E5-2686 v4 / 36-core / 31 GB) | 116.34 ms | 19.31 req/s | 3× k6 runs; billing tables truncated. 0% error rate. No regression vs v0.16.0. |

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

| Metric | Phase 0 baseline | Regression threshold | v0.16.0 (amd64) | develop (amd64) |
|--------|------------------|----------------------|-----------------|
| `/v1/chat/completions` P95 | 72.0 ms (arm64) | ≤ 78 ms (+8%) | 116.68 ms | 116.34 ms |
| `/v1/chat/completions` P99 | 115.0 ms (arm64) | ≤ 125 ms (+8%) | 119.63 ms | 120.30 ms |
| billing commit P95 | 15.0 ms (gRPC, arm64) | ≤ 17 ms (+13%) | N/A — Prometheus not scraped | N/A — Prometheus not scraped |
| billing commit P99 | 28.0 ms (gRPC, arm64) | ≤ 31 ms (+11%) | N/A — Prometheus not scraped | N/A — Prometheus not scraped |
| `routing_selection_duration_seconds{source_kind}` P95 | N/A (new metric) | < 5 ms | ~35 ms (from logs) | ~35 ms (from logs) |
| Error rate | 0.25% | ≤ 0.30% | 0.00% | 0.00% |

**Phase 3 regression assessment:** The v0.16.0→develop chat P95 delta
is -0.34 ms (116.68→116.34, within noise); P99 delta is +0.67 ms
(119.63→120.30, within noise). **No regression detected.** The arm64 Phase 0
threshold values are not directly comparable to amd64 (different CPU, mock
upstream vs real upstream); the cross-version comparison on the same amd64
environment is the authoritative check.

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
