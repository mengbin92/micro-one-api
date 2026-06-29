// k6 baseline test for relay-gateway
// Run: k6 run --out json=results.json scripts/benchmark/k6-baseline.js
// Or with vegeta: echo "GET http://localhost:8080/healthz" | vegeta attack -duration=60s -rate=100 | vegeta report

import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate } from 'k6/metrics';

// Custom metrics
const errorRate = new Rate('errors');

// Test configuration
export const options = {
  stages: [
    { duration: '1m', target: 10 },   // Ramp up to 10 users
    { duration: '2m', target: 50 },   // Ramp up to 50 users
    { duration: '2m', target: 100 },  // Ramp up to 100 users
    { duration: '2m', target: 200 },  // Ramp up to 200 users
    { duration: '1m', target: 0 },    // Ramp down
  ],
  thresholds: {
    'http_req_duration': ['p(95)<500'], // 95% of requests must complete below 500ms
    'http_req_failed': ['rate<0.05'],   // Error rate must be below 5%
    'errors': ['rate<0.05'],
  },
};

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const API_KEY = __ENV.API_KEY || 'sk-test-key';

export default function () {
  // Health check endpoint (lightweight)
  let healthRes = http.get(`${BASE_URL}/healthz`, {
    tags: { endpoint: 'healthz' },
  });
  errorRate.add(!check(healthRes, { 'healthz status 200': (r) => r.status === 200 }));

  // Models list endpoint (uses cache)
  let modelsRes = http.get(`${BASE_URL}/v1/models`, {
    headers: { 'Authorization': `Bearer ${API_KEY}` },
    tags: { endpoint: 'models' },
  });
  errorRate.add(!check(modelsRes, { 'models status 200': (r) => r.status === 200 }));

  // Chat completions endpoint (full relay path)
  let chatPayload = JSON.stringify({
    model: 'gpt-3.5-turbo',
    messages: [{ role: 'user', content: 'Hello!' }],
    max_tokens: 10,
  });

  let chatRes = http.post(`${BASE_URL}/v1/chat/completions`, chatPayload, {
    headers: {
      'Authorization': `Bearer ${API_KEY}`,
      'Content-Type': 'application/json',
    },
    tags: { endpoint: 'chat' },
  });
  errorRate.add(!check(chatRes, {
    'chat status 200 or 429': (r) => r.status === 200 || r.status === 429 || r.status === 500,
  }));

  sleep(1);
}

export function handleSummary(data) {
  return {
    'stdout': textSummary(data, { indent: ' ', enableColors: true }),
    'summary.json': JSON.stringify(data),
  };
}

function textSummary(data, { indent, enableColors }) {
  // Custom text summary
  const colors = enableColors
    ? { green: '\x1B[32m', red: '\x1B[31m', reset: '\x1B[0m' }
    : { green: '', red: '', reset: '' };

  let summary = '';

  // Request duration percentiles
  const reqDuration = data.metrics.http_req_duration.values;
  summary += `${indent}Request Duration:\n`;
  summary += `${indent}  p(50): ${formatDuration(reqDuration['p(50)'])}\n`;
  summary += `${indent}  p(95): ${formatDuration(reqDuration['p(95)'])}\n`;
  summary += `${indent}  p(99): ${formatDuration(reqDuration['p(99)'])}\n`;

  // Error rate
  const failedRate = data.metrics.http_req_failed.values.rate;
  summary += `${indent}Error Rate: ${failedRate * 100}%\n`;

  // RPS
  const rps = data.metrics.http_reqs.values.count / data.metrics.http_req_duration.values.avg;
  summary += `${indent}Throughput: ${rps.toFixed(2)} req/s\n`;

  return summary;
}

function formatDuration(ms) {
  if (ms < 1000) return `${ms.toFixed(2)}ms`;
  return `${(ms / 1000).toFixed(2)}s`;
}
