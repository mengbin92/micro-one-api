// k6 baseline benchmark for relay-gateway.
//
// Measures relay-gateway HTTP overhead against a deterministic mock upstream.
// k6 collects HTTP-layer metrics only (latency, throughput, error rate). All
// gRPC service latency, billing commit, routing selection, cache hit, and
// circuit-breaker metrics are collected separately from Prometheus — see the
// "Prometheus queries" section in docs/design/BASELINE.md.
//
// ── What this script fixes vs. the original k6-baseline.js ────────────────
//   1. Throughput uses http_reqs.values.rate (k6's built-in req/s), NOT
//      count / avg duration (which is algebraically wrong under staged load).
//   2. HTTP 500 is NOT counted as success — only 200 (429 is explicitly
//      flagged as rate-limited and never counted as success).
//   3. summaryTrendStats is set explicitly so p(50)/p(90)/p(95)/p(99) are
//      always reported.
//   4. Per-endpoint Trend metrics (healthz/models/chat) are emitted so each
//      path can be compared independently.
//   5. The mock upstream contract is documented; the relay-gateway channel
//      config must point at it so there is zero real upstream network I/O.
//   6. k6 HTTP metrics and Prometheus (gRPC/cache/circuit-breaker) metrics
//      are clearly separated — this script does NOT scrape Prometheus.
//
// ── Prerequisites ─────────────────────────────────────────────────────────
//   - relay-gateway running, its channel upstream base URL set to the mock
//     upstream (default http://127.0.0.1:18099). See
//     scripts/benchmark/mock-upstream/main.go.
//   - A valid API key / token configured in identity-service for the relay.
//   - Run `make benchmark-mock` to start the mock upstream, then this script.
//
// ── Usage ─────────────────────────────────────────────────────────────────
//   export BASE_URL=http://localhost:8080
//   export API_KEY=sk-your-test-key
//   RESULTS_FILE=scripts/benchmark/results/summary-<sha>-<timestamp>.json \
//   k6 run scripts/benchmark/k6-baseline.js \
//     --out json=scripts/benchmark/results/raw-<sha>-<timestamp>.json
//
//   Override load profile. ITERATION_TARGET_RATE is iterations/sec; each
//   iteration emits three HTTP requests, so the observed request rate is
//   approximately three times the iteration rate when there are no drops:
//   k6 run -e ITERATION_TARGET_RATE=200 -e RAMP_HOLD=2m scripts/benchmark/k6-baseline.js

import http from 'k6/http';
import { Trend, Rate, Counter } from 'k6/metrics';

// ── Per-endpoint custom metrics ────────────────────────────────────────────
// k6 already aggregates http_req_duration globally; these separate Trends give
// us an endpoint breakdown. We also keep explicit success/error counters per
// endpoint so 5xx is never silently counted as success.
const healthzDuration = new Trend('healthz_duration_ms', true);
const modelsDuration = new Trend('models_duration_ms', true);
const chatDuration = new Trend('chat_duration_ms', true);

const healthzOk = new Rate('healthz_ok');
const modelsOk = new Rate('models_ok');
const chatOk = new Rate('chat_ok');
const chat429 = new Counter('chat_rate_limited_total');

// ── Configuration ──────────────────────────────────────────────────────────
const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const API_KEY = __ENV.API_KEY || 'sk-test-key';

// Load profile — open-model arrival rate, overridable via env for quick smoke
// runs. One iteration sends three requests (healthz, models, chat).
const ITERATION_START_RATE = parseInt(__ENV.ITERATION_START_RATE || '10', 10);
const ITERATION_TARGET_RATE = parseInt(
  __ENV.ITERATION_TARGET_RATE || __ENV.RAMP_TARGET || '200',
  10,
);
const RAMP_HOLD = __ENV.RAMP_HOLD || '2m';
const PREALLOCATED_VUS = parseInt(__ENV.PREALLOCATED_VUS || '100', 10);
const MAX_VUS = parseInt(__ENV.MAX_VUS || '1000', 10);

// SMOKE=1 collapses the six hard-coded stages (6m+) into a ~35s run. It is
// meant for verifying the script end-to-end (syntax, summary export, raw
// output) on any machine before committing to a full-length run on
// Linux/amd64. The SMOKE profile is NOT comparable to the full stages.
const SMOKE = __ENV.SMOKE === '1' || __ENV.SMOKE === 'true';
const STAGE = SMOKE ? '5s' : '1m';

export const options = {
  // summaryTrendStats: explicitly request these percentiles so the JSON/CSV
  // output and stdout summary always carry p(50)/p(90)/p(95)/p(99). Without
  // this, k6 defaults to avg/min/med/max/p(90)/p(95) and omits p(50)/p(99).
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(50)', 'p(90)', 'p(95)', 'p(99)'],
  scenarios: {
    baseline: {
      executor: 'ramping-arrival-rate',
      startRate: ITERATION_START_RATE,
      timeUnit: '1s',
      preAllocatedVUs: PREALLOCATED_VUS,
      maxVUs: MAX_VUS,
      exec: 'allEndpoints',
      stages: SMOKE
        ? [
            { duration: '5s', target: Math.round(ITERATION_TARGET_RATE * 0.5) },
            { duration: '5s', target: ITERATION_TARGET_RATE },
            { duration: '5s', target: Math.round(ITERATION_TARGET_RATE * 0.5) },
            { duration: '10s', target: ITERATION_TARGET_RATE },
            { duration: '5s', target: ITERATION_TARGET_RATE },
            { duration: '5s', target: 0 },
          ]
        : [
            { duration: '1m', target: Math.round(ITERATION_TARGET_RATE * 0.05) },
            { duration: '1m', target: Math.round(ITERATION_TARGET_RATE * 0.25) },
            { duration: '1m', target: Math.round(ITERATION_TARGET_RATE * 0.50) },
            { duration: '2m', target: ITERATION_TARGET_RATE },
            { duration: RAMP_HOLD, target: ITERATION_TARGET_RATE },
            { duration: '1m', target: 0 },
          ],
    },
  },
  thresholds: {
    // 95% of all requests must complete below 500ms.
    'http_req_duration': ['p(95)<500'],
    // k6's built-in http_req_failed counts non-1xx/2xx/3xx as failed. We
    // keep it < 5%; the per-endpoint _ok rates are stricter.
    'http_req_failed': ['rate<0.05'],
    // Per-endpoint success-rate gates.
    'healthz_ok': ['rate>0.999'],
    'models_ok': ['rate>0.995'],
    // chat allows 429 (rate-limited) but never 5xx. _ok = status 200 only.
    'chat_ok': ['rate>0.95'],
    // The arrival-rate executor must not silently drop iterations because the
    // VU pool is too small; otherwise the measured request rate is invalid.
    'dropped_iterations': ['count==0'],
  },
};

// ── One open-model iteration hits all three endpoints ──────────────────────
export function allEndpoints() {
  // ── healthz (lightweight, no auth) ──────────────────────────────────────
  const healthRes = http.get(`${BASE_URL}/healthz`, { tags: { endpoint: 'healthz' } });
  const healthIs200 = healthRes.status === 200;
  healthzOk.add(healthIs200);
  healthzDuration.add(healthRes.timings.duration);

  // ── /v1/models (auth + cache path) ──────────────────────────────────────
  const modelsRes = http.get(`${BASE_URL}/v1/models`, {
    headers: { Authorization: `Bearer ${API_KEY}` },
    tags: { endpoint: 'models' },
  });
  const modelsIs200 = modelsRes.status === 200;
  modelsOk.add(modelsIs200);
  modelsDuration.add(modelsRes.timings.duration);

  // ── /v1/chat/completions (full relay path through mock upstream) ────────
  const chatPayload = JSON.stringify({
    model: 'gpt-3.5-turbo',
    messages: [{ role: 'user', content: 'Hello!' }],
    max_tokens: 10,
    stream: false,
  });
  const chatRes = http.post(`${BASE_URL}/v1/chat/completions`, chatPayload, {
    headers: {
      Authorization: `Bearer ${API_KEY}`,
      'Content-Type': 'application/json',
    },
    tags: { endpoint: 'chat' },
  });
  // Success is 200 ONLY. 429 is rate-limited (recorded separately, not success).
  // 5xx is a hard failure and must never be counted as success.
  const chatIs200 = chatRes.status === 200;
  chatOk.add(chatIs200);
  chatDuration.add(chatRes.timings.duration);
  if (chatRes.status === 429) {
    chat429.add(1);
  }
}

// Keep the default entry point usable for ad-hoc k6 invocations that do not
// select the named scenario explicitly.
export default allEndpoints;

// ── Summary: print corrected throughput + per-endpoint breakdown ───────────
export function handleSummary(data) {
  const lines = [];
  lines.push('');
  lines.push('═══════════════════════════════════════════════════════════════');
  lines.push(' relay-gateway k6 baseline — summary');
  lines.push('═══════════════════════════════════════════════════════════════');
  lines.push(` base_url:          ${BASE_URL}`);
  lines.push(` iteration_target:  ${ITERATION_TARGET_RATE} iterations/s`);
  lines.push(` ramp_hold:         ${RAMP_HOLD}`);
  lines.push(` preallocated_vus:  ${PREALLOCATED_VUS}`);
  lines.push(` max_vus:           ${MAX_VUS}`);

  // ── Aggregate (all endpoints) ───────────────────────────────────────────
  const dur = data.metrics.http_req_duration?.values || {};
  const reqs = data.metrics.http_reqs?.values || {};
  const failed = data.metrics.http_req_failed?.values || {};
  lines.push('');
  lines.push(' ── Aggregate (all endpoints) ──');
  lines.push(`   throughput:      ${(reqs.rate || 0).toFixed(2)} req/s   (http_reqs.values.rate)`);
  lines.push(`   total_requests:  ${reqs.count || 0}`);
  lines.push(`   dropped_iters:   ${data.metrics.dropped_iterations?.values?.count || 0}`);
  lines.push(`   http_req_failed: ${((failed.rate || 0) * 100).toFixed(2)}%`);
  lines.push(`   p(50):           ${fmtMs(dur['p(50)'])}`);
  lines.push(`   p(90):           ${fmtMs(dur['p(90)'])}`);
  lines.push(`   p(95):           ${fmtMs(dur['p(95)'])}`);
  lines.push(`   p(99):           ${fmtMs(dur['p(99)'])}`);

  // ── Per-endpoint ────────────────────────────────────────────────────────
  lines.push('');
  lines.push(' ── /healthz ──');
  printEndpoint(lines, 'healthz', data);
  lines.push('');
  lines.push(' ── /v1/models ──');
  printEndpoint(lines, 'models', data);
  lines.push('');
  lines.push(' ── /v1/chat/completions ──');
  printEndpoint(lines, 'chat', data);

  const c429 = data.metrics.chat_rate_limited_total?.values?.count || 0;
  lines.push(`   chat 429 count:  ${c429}`);

  lines.push('');
  lines.push(' NOTE: gRPC service latency, billing commit, routing selection,');
  lines.push(' cache hit rate, and circuit-breaker state are collected from');
  lines.push(' Prometheus separately — see docs/design/BASELINE.md.');
  lines.push('═══════════════════════════════════════════════════════════════');
  lines.push('');

  const text = lines.join('\n');

  // Write JSON + text alongside k6's default files.
  const out = {
    stdout: text,
  };
  // Archive full data if RESULTS_FILE is set.
  if (__ENV.RESULTS_FILE) {
    out[__ENV.RESULTS_FILE] = JSON.stringify(data, null, 2);
  }
  return out;
}

function printEndpoint(lines, name, data) {
  const trendName = `${name}_duration_ms`;
  const okName = `${name}_ok`;
  const t = data.metrics[trendName]?.values || {};
  const ok = data.metrics[okName]?.values || {};
  lines.push(`   p(50):           ${fmtMs(t['p(50)'])}`);
  lines.push(`   p(90):           ${fmtMs(t['p(90)'])}`);
  lines.push(`   p(95):           ${fmtMs(t['p(95)'])}`);
  lines.push(`   p(99):           ${fmtMs(t['p(99)'])}`);
  lines.push(`   success_rate:    ${((ok.rate || 0) * 100).toFixed(2)}%  (status==200 only)`);
}

function fmtMs(ms) {
  if (ms === undefined || ms === null || isNaN(ms)) return 'N/A';
  if (ms < 1000) return `${ms.toFixed(2)} ms`;
  return `${(ms / 1000).toFixed(3)} s`;
}
