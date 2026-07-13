// k6 pre-prod stress test for relay-gateway subscription-account paths.
//
// Covers the docs/design/subscription-follow-up-roadmap.md §阶段 3 (Relay 稳定性)
// scenarios that need a real multi-replica deployment + real Redis:
//   - session_hash sticky (prompt-cache reuse) under sustained load
//   - previous_response_id route sticky under sustained load
//   - 429/5xx/529 failover under load
//   - RPM / concurrency / session-window failover under load
//
// This script is the PRE-PROD full load test. CI runs only the Go hermetic
// smoke (go test -run TestStress ./internal/relay/...); see
// docs/runbooks/relay-stress-runbook.md for when to run this.
//
// Usage (against a pre-prod relay-gateway with >=2 replicas + Redis):
//   k6 run \
//     -e BASE_URL=https://relay-gateway.preprod.internal \
//     -e API_KEY=sk-... \
//     -e SESSION_HASH=stress-session-1 \
//     -e SCENARIO=session_sticky \
//     scripts/benchmark/k6-relay-subscription-stress.js
//
// Scenarios (-e SCENARIO=...):
//   session_sticky       — one session_hash, many turns (sticky hit rate)
//   response_sticky      — previous_response_id chaining (route sticky)
//   failover_429         — forces 429 failover (needs an account that 429s)
//   mixed_failover       — 429/5xx/529 mix
//   concurrency_rpm      — high concurrency to exercise concurrency/RPM caps
//
// Metrics emitted (roadmap §阶段 3 fixed metric set):
//   - relay_success_rate          (k6 custom Rate)
//   - relay_latency_p50/p95/p99   (k6 Trend on http_req_duration)
//   - relay_failover_total        (from Prometheus scrape, see below)
//   - relay_redis_fallback_total  (from Prometheus scrape)
//   - relay_concurrency_peak      (from Prometheus scrape)
//
// Prometheus scrape (run separately or in handleSummary):
//   curl -s $PROM_URL/api/v1/query?query=micro_one_api_relay_subscription_failover_total
//   curl -s $PROM_URL/api/v1/query?query=micro_one_api_relay_account_concurrency_fallback_total
//
// k6 itself provides http_req_duration (p50/p95/p99) and http_req_failed; the
// failover/fallback/concurrency-peak metrics live in Prometheus and are
// scraped after the run for the final report.

import http from 'k6/http';
import { check, sleep, group } from 'k6';
import { Rate, Trend, Counter } from 'k6/metrics';

// ---- Custom metrics (roadmap fixed set) -------------------------------------
const relaySuccessRate = new Rate('relay_success_rate');
const relayLatency = new Trend('relay_latency_ms', true); // ms
const relayFailoverObserved = new Counter('relay_failover_observed');
const relayErrors = new Counter('relay_errors_total');

// ---- Configuration ----------------------------------------------------------
const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const API_KEY = __ENV.API_KEY || 'sk-test-key';
const SCENARIO = __ENV.SCENARIO || 'session_sticky';
const MODEL = __ENV.MODEL || 'gpt-5';

// Per-scenario load profiles. Pre-prod full = sustained; CI smoke is the Go
// test suite, NOT this script.
const scenarios = {
  session_sticky: {
    executor: 'ramping-arrival-rate',
    startRate: 5,
    timeUnit: '1s',
    preAllocatedVUs: 50,
    maxVUs: 200,
    stages: [
      { duration: '1m', target: 20 },   // ramp
      { duration: '3m', target: 50 },   // sustained
      { duration: '1m', target: 0 },    // ramp down
    ],
    exec: 'sessionSticky',
  },
  response_sticky: {
    executor: 'ramping-arrival-rate',
    startRate: 5,
    timeUnit: '1s',
    preAllocatedVUs: 50,
    maxVUs: 200,
    stages: [
      { duration: '1m', target: 20 },
      { duration: '3m', target: 50 },
      { duration: '1m', target: 0 },
    ],
    exec: 'responseSticky',
  },
  failover_429: {
    executor: 'constant-arrival-rate',
    rate: 30,
    timeUnit: '1s',
    preAllocatedVUs: 100,
    maxVUs: 300,
    duration: '3m',
    exec: 'failover429',
  },
  mixed_failover: {
    executor: 'constant-arrival-rate',
    rate: 30,
    timeUnit: '1s',
    preAllocatedVUs: 100,
    maxVUs: 300,
    duration: '3m',
    exec: 'mixedFailover',
  },
  concurrency_rpm: {
    executor: 'ramping-vus',
    startVUs: 0,
    stages: [
      { duration: '30s', target: 50 },
      { duration: '2m', target: 150 },
      { duration: '30s', target: 0 },
    ],
    exec: 'concurrencyRpm',
  },
};

export const options = {
  scenarios: { [SCENARIO]: scenarios[SCENARIO] },
  thresholds: {
    // Pre-prod SLO gates. Adjust per environment.
    'relay_success_rate': ['rate>0.99'],
    'relay_latency_ms': ['p(95)<2000', 'p(99)<5000'],
    'http_req_failed': ['rate<0.02'],
  },
  noConnectionReuse: false,
};

// ---- Helpers ----------------------------------------------------------------
function makeHeaders(extra) {
  const h = {
    'Authorization': `Bearer ${API_KEY}`,
    'Content-Type': 'application/json',
  };
  return Object.assign(h, extra || {});
}

function recordResult(res, sessionHash) {
  const ok = res.status === 200;
  relaySuccessRate.add(ok);
  relayLatency.add(res.timings.duration);
  if (!ok) {
    relayErrors.add(1);
  }
  // A 503 from the gateway (all accounts busy) or a passthrough 429/5xx means
  // a failover was attempted (or exhausted). We count it so the run report
  // surfaces failover pressure; the authoritative count is the Prometheus
  // counter micro_one_api_relay_subscription_failover_total.
  if (res.status === 429 || res.status === 503 || res.status >= 500) {
    relayFailoverObserved.add(1);
  }
  return ok;
}

// ---- Scenario: session_hash sticky -----------------------------------------
// One session_hash across many turns: proves prompt-cache reuse (sticky hits).
export function sessionSticky() {
  const sessionHash = __ENV.SESSION_HASH || `k6-session-${__VU}`;
  const body = JSON.stringify({
    model: MODEL,
    messages: [{ role: 'user', content: 'Tell me a short fact.' }],
    max_tokens: 16,
    stream: false,
    session_hash: sessionHash,
  });
  const res = http.post(`${BASE_URL}/v1/chat/completions`, body, {
    headers: makeHeaders({ 'X-Session-Hash': sessionHash }),
  });
  recordResult(res, sessionHash);
  sleep(0.5);
}

// ---- Scenario: previous_response_id route sticky ---------------------------
// Chains responses by previous_response_id to prove cross-turn route sticky.
export function responseSticky() {
  const session = `k6-resp-${__VU}-${__ITER}`;
  // Turn 1: create a response.
  let body = JSON.stringify({
    model: MODEL,
    input: 'Hello!',
    stream: false,
    session_hash: session,
  });
  let res = http.post(`${BASE_URL}/v1/responses`, body, {
    headers: makeHeaders({ 'X-Session-Hash': session }),
  });
  let ok = recordResult(res, session);
  if (!ok || res.status !== 200) return;

  // Extract the response id for the next turn (best-effort; body shape varies).
  let responseID = '';
  try {
    const parsed = res.json();
    responseID = parsed.id || '';
  } catch (e) { /* ignore */ }

  sleep(0.5);

  // Turn 2: follow up with previous_response_id (route sticky lookup).
  if (responseID) {
    body = JSON.stringify({
      model: MODEL,
      input: 'Follow up.',
      previous_response_id: responseID,
      stream: false,
      session_hash: session,
    });
    res = http.post(`${BASE_URL}/v1/responses`, body, {
      headers: makeHeaders({ 'X-Session-Hash': session }),
    });
    recordResult(res, session);
  }
  sleep(0.5);
}

// ---- Scenario: failover 429 -------------------------------------------------
// Drives load that should trigger 429 failover. Requires at least one account
// configured to 429 (or rate-limited upstream) and >=1 healthy sibling.
export function failover429() {
  const sessionHash = `k6-429-${__VU}-${__ITER}`;
  const body = JSON.stringify({
    model: MODEL,
    messages: [{ role: 'user', content: 'ping' }],
    max_tokens: 4,
    stream: false,
    session_hash: sessionHash,
  });
  const res = http.post(`${BASE_URL}/v1/chat/completions`, body, {
    headers: makeHeaders({ 'X-Session-Hash': sessionHash }),
  });
  recordResult(res, sessionHash);
  // No sleep: constant-arrival-rate controls the pace.
}

// ---- Scenario: mixed failover (429/5xx/529) --------------------------------
export function mixedFailover() {
  const sessionHash = `k6-mix-${__VU}-${__ITER}`;
  const body = JSON.stringify({
    model: MODEL,
    messages: [{ role: 'user', content: 'ping' }],
    max_tokens: 4,
    stream: false,
    session_hash: sessionHash,
  });
  const res = http.post(`${BASE_URL}/v1/chat/completions`, body, {
    headers: makeHeaders({ 'X-Session-Hash': sessionHash }),
  });
  recordResult(res, sessionHash);
}

// ---- Scenario: concurrency / RPM pressure ----------------------------------
export function concurrencyRpm() {
  const sessionHash = `k6-rpm-${__VU}`;
  const body = JSON.stringify({
    model: MODEL,
    messages: [{ role: 'user', content: 'hi' }],
    max_tokens: 4,
    stream: false,
    session_hash: sessionHash,
  });
  const res = http.post(`${BASE_URL}/v1/chat/completions`, body, {
    headers: makeHeaders({ 'X-Session-Hash': sessionHash }),
  });
  recordResult(res, sessionHash);
  sleep(0.1);
}

// ---- Summary ----------------------------------------------------------------
export function handleSummary(data) {
  const pad = (s, n) => String(s).padEnd(n, ' ');
  const lines = [];
  lines.push('=== Relay Subscription Stress Summary ===');
  lines.push(`scenario:        ${SCENARIO}`);
  lines.push(`base_url:        ${BASE_URL}`);

  const reqs = data.metrics.http_reqs?.values?.count || 0;
  const failedReqs = data.metrics.http_req_failed?.values?.rate || 0;
  lines.push(`requests:        ${reqs}`);
  lines.push(`http_req_failed: ${(failedReqs * 100).toFixed(2)}%`);

  const dur = data.metrics.relay_latency_ms?.values || {};
  lines.push(`latency_ms_p50:  ${(dur['p(50)'] || 0).toFixed(3)}`);
  lines.push(`latency_ms_p95:  ${(dur['p(95)'] || 0).toFixed(3)}`);
  lines.push(`latency_ms_p99:  ${(dur['p(99)'] || 0).toFixed(3)}`);

  const sr = data.metrics.relay_success_rate?.values?.rate || 0;
  lines.push(`success_rate:    ${sr.toFixed(4)}`);

  const fo = data.metrics.relay_failover_observed?.values?.count || 0;
  lines.push(`failover_observed(client-side): ${fo}`);
  lines.push('');
  lines.push('NOTE: authoritative failover / redis-fallback / concurrency-peak');
  lines.push('metrics are in Prometheus. Scrape after the run:');
  lines.push('  micro_one_api_relay_subscription_failover_total');
  lines.push('  micro_one_api_relay_account_concurrency_fallback_total');
  lines.push('  micro_one_api_relay_account_rpm_fallback_total');
  lines.push('  micro_one_api_relay_runtime_block_active');

  const text = lines.join('\n');
  return {
    stdout: text + '\n',
    'relay-stress-summary.json': JSON.stringify(data, null, 2),
  };
}
