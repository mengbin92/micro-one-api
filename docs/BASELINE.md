# Performance Baseline

## Test Environment

- Date: 2026-06-28
- Infrastructure: [To be filled]
- CPU: [To be filled]
- Memory: [To be filled]
- Go Version: 1.26
- Kratos Version: v2.9.2

## Baseline Metrics

### Phase 0 Baseline (Pre-Refactoring)

> Run the benchmark with: `k6 run scripts/benchmark/k6-baseline.js`

| Metric | Value | Notes |
|--------|-------|-------|
| **P50 Latency** | TBD | - |
| **P95 Latency** | TBD | - |
| **P99 Latency** | TBD | - |
| **Error Rate** | TBD | - |
| **Throughput** | TBD | req/s |
| **Active Requests** | TBD | - |

### Endpoint-Specific Baselines

| Endpoint | P50 | P95 | P99 | Error Rate |
|----------|-----|-----|-----|------------|
| /healthz | TBD | TBD | TBD | TBD |
| /v1/models | TBD | TBD | TBD | TBD |
| /v1/chat/completions | TBD | TBD | TBD | TBD |

### gRPC Service Call Latency

| Service | P50 | P95 | P99 |
|---------|-----|-----|-----|
| identity-service | TBD | TBD | TBD |
| channel-service | TBD | TBD | TBD |
| billing-service | TBD | TBD | TBD |
| log-service | TBD | TBD | TBD |

### Cache Hit Rates (Pre-Implementation)

| Cache Type | L1 Hit Rate | L2 Hit Rate | Miss Rate |
|------------|-------------|-------------|-----------|
| Auth Cache | N/A | N/A | 100% (no cache) |
| Channel Cache | N/A | N/A | 100% (no cache) |
| Quota Cache | N/A | N/A | 100% (no cache) |

### Circuit Breaker State

| Service | State | Trips (24h) |
|---------|-------|-------------|
| identity-service | N/A | N/A |
| channel-service | N/A | N/A |
| billing-service | N/A | N/A |
| log-service | N/A | N/A |

## Target Metrics (Post-Refactoring)

Based on `docs/ARCHITECTURE_REFACTOR.md` §11:

| Metric | Baseline | Target | Improvement |
|--------|----------|--------|-------------|
| P95 Request Latency (no upstream) | 30-50ms | 5-10ms | ~80% |
| gRPC Calls/Request | 5 | 0-1 (cache hit) | ~90% |
| Throughput/Instance | ~500 req/s | ~2000 req/s | 4x |

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
k6 run --out json=results.json scripts/benchmark/k6-baseline.js

# Run with specific duration
k6 run --duration 5m --vus 50 scripts/benchmark/k6-baseline.js

# Generate HTML report
k6 run --out json=results.json scripts/benchmark/k6-baseline.js
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
| 2026-06-28 | Phase 0 | TBD | TBD | Pre-refactoring baseline |
| | Phase 1 | TBD | TBD | After P0 fixes |
| | Phase 2 | TBD | TBD | After P1 optimizations |
