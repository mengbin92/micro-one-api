import assert from 'node:assert/strict';
import { chromium, expect } from '@playwright/test';

const baseURL = process.argv[2] || 'http://127.0.0.1:4174';
const relayURL = process.argv[3] || 'https://relay.test';
const liveHeader = process.argv.includes('--live-header');
const browser = await chromium.launch({ channel: 'chrome', headless: true });
const page = await browser.newPage();
const pageErrors = [];
const policy = "default-src 'self'; base-uri 'self'; object-src 'none'; " +
  "script-src 'self'; style-src 'self' 'unsafe-inline'; " +
  "img-src 'self' data: https:; font-src 'self' data:; " +
  `connect-src 'self' ${relayURL}; frame-ancestors 'none';`;

try {
  page.on('pageerror', error => pageErrors.push(error.message));
  await page.addInitScript(() => {
    localStorage.setItem('token', 'test-user-token');
    localStorage.setItem('userRole', '1');
    localStorage.setItem('userId', '42');
    localStorage.setItem('web:language', JSON.stringify('zh-CN'));
    document.addEventListener('securitypolicyviolation', event => {
      window.__cspViolations ??= [];
      window.__cspViolations.push({ directive: event.effectiveDirective, blocked: event.blockedURI });
    });
  });

  await page.route('**/api/**', route => route.fulfill({ json: { success: true, data: [] } }));
  await page.route('**/api/user/self', route => route.fulfill({
    json: { success: true, data: { id: 42, role: 1, display_name: 'Tester' } },
  }));
  await page.route('**/api/status', route => route.fulfill({
    json: { success: true, data: { server_address: relayURL } },
  }));
  await page.route(`${relayURL}/**`, route => {
    const headers = {
      'Access-Control-Allow-Origin': '*',
      'Access-Control-Allow-Headers': 'Authorization, Content-Type, X-Request-ID',
      'Access-Control-Expose-Headers': 'X-Request-ID, X-Trace-ID, X-OTel-Trace-ID',
    };
    if (route.request().method() === 'OPTIONS') return route.fulfill({ status: 204, headers });
    if (route.request().url().endsWith('/v1/models')) {
      return route.fulfill({ json: { data: [{ id: 'demo-model' }] }, headers });
    }
    return route.fulfill({
      json: {
        choices: [{ message: { content: 'ok' }, finish_reason: 'stop' }],
        usage: { prompt_tokens: 1, completion_tokens: 1 },
      },
      headers: { ...headers, 'X-Request-ID': 'root-browser-1', 'X-Trace-ID': 'trace-browser-1' },
    });
  });

  if (!liveHeader) {
    // Vite preview has no server headers; inject the admin-api policy on its document.
    await page.route('**/playground', async route => {
      const response = await route.fetch();
      await route.fulfill({ response, headers: { ...response.headers(), 'content-security-policy': policy } });
    });
  }

  const response = await page.goto(`${baseURL}/playground`);
  if (liveHeader) {
    const header = response.headers()['content-security-policy'] || '';
    assert.ok(header.includes("script-src 'self'"));
    assert.ok(header.includes(`connect-src 'self' ${relayURL}`));
  }
  await page.locator('#playground-key').fill('sk-browser-test');
  await page.getByRole('button', { name: '验证并加载模型' }).click();
  await expect(page.locator('#playground-model')).toHaveValue('demo-model');
  await page.locator('textarea[aria-label="输入消息"]').fill('hello');
  await page.getByRole('button', { name: '发送' }).click();
  await expect(page.getByText('ok', { exact: true })).toBeVisible();
  await expect(page.getByText('root-browser-1')).toBeVisible();

  const state = await page.evaluate(() => ({
    violations: window.__cspViolations ?? [],
    secretPersisted: [...Object.values(localStorage), ...Object.values(sessionStorage)]
      .some(value => value.includes('sk-browser-test')),
  }));
  assert.deepEqual(state.violations, []);
  assert.deepEqual(pageErrors, []);
  assert.equal(state.secretPersisted, false);
  assert.equal(page.url().includes('sk-browser-test'), false);
  console.log('Playground CSP, cross-origin Relay, request ID and key checks passed');
} finally {
  await browser.close();
}
