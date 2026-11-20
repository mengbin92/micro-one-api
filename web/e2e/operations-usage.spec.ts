import { expect, test } from '@playwright/test';
import { mockApi } from './fixtures';

test.beforeEach(async ({ page }) => {
  await page.route('**/api/**', (route) => route.fulfill({ json: { success: true, data: [] } }));
  await mockApi(page, { admin: true });
  await page.route('**/api/user/self', (route) => route.fulfill({ json: { success: true, data: { id: 42, display_name: 'Operator', role: 10 } } }));
  await page.addInitScript(() => {
    localStorage.setItem('token', 'test-user-token');
    localStorage.setItem('userRole', '10');
    localStorage.setItem('userId', '42');
    localStorage.setItem('web:language', JSON.stringify('zh-CN'));
  });
});

test('subscription settled, frozen and available stay visible', async ({ page }, testInfo) => {
  await page.route('**/api/v1/subscriptions/progress*', (route) => route.fulfill({ json: { success: true, data: {
    id: 7, status: 'active', starts_at: 1700000000, expires_at: 1900000000, remaining_seconds: 86400,
    rate_multiplier: 2, usage_source: 'billing',
    daily_used: { used: 5, settled: 5, frozen: 2, available: 3, limit: 10, remaining: 5 },
    weekly_used: { used: 2, settled: 2, frozen: 0, available: null, limit: null, remaining: 0, unlimited: true },
    monthly_used: { used: 1, settled: 1, frozen: 0, available: 0, limit: 0, remaining: 0, over_limit: true },
  } } }));
  await page.goto('/subscriptions');
  await expect(page.getByText('冻结 $2.00', { exact: true })).toBeVisible();
  await expect(page.getByText('可用 $3.00', { exact: true })).toBeVisible();
  await expect(page.getByText('已超限', { exact: true })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(page.viewportSize()!.width);
  await page.screenshot({ path: testInfo.outputPath('subscription-usage.png'), fullPage: true });
});

test('manual recovery filter queries the server and shows the reason', async ({ page }, testInfo) => {
  const policies: string[] = [];
  await page.route('**/api/subscription-accounts?**', (route) => {
    policies.push(new URL(route.request().url()).searchParams.get('recovery_policy') ?? '');
    return route.fulfill({ json: { accounts: [{ id: 1, name: 'Review account', platform: 'claude', status: 2, group: 'default', models: 'claude-sonnet-4-5', recoveryPolicy: 'manual', unschedulableReason: 'upstream 401', unschedulableSince: Math.floor(Date.now() / 1000) - 7200 }], total: 1 } });
  });
  await page.goto('/admin/subscription-accounts');
  await page.getByRole('combobox', { name: '按恢复策略筛选' }).selectOption('manual');
  await expect.poll(() => policies).toContain('manual');
  await expect(page.getByText('upstream 401', { exact: true })).toBeVisible();
  await expect(page.getByText('已等待 2h', { exact: true })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(page.viewportSize()!.width);
  await page.screenshot({ path: testInfo.outputPath('manual-recovery.png'), fullPage: true });
});

test('Kimi device binding retains pending authorization and the chosen group', async ({ page }) => {
  const polls: unknown[] = [];
  const authRequests: unknown[] = [];
  await page.context().route('https://auth.kimi.com/device-fixture', (route) => route.fulfill({ body: 'Device authorization fixture' }));
  await page.route('**/api/v1/admin/accounts/subscription/oauth/kimi/auth-url', (route) => {
    authRequests.push(route.request().postDataJSON());
    return route.fulfill({ json: {
      auth_url: 'https://auth.kimi.com/device-fixture', session_id: 'fixture-session', state: 'fixture-state', user_code: 'CODE-123',
      expires_at: Math.floor(Date.now() / 1000) + 300,
    } });
  });
  await page.route('**/api/v1/admin/accounts/subscription/oauth/kimi/exchange', (route) => {
    polls.push(route.request().postDataJSON());
    return route.fulfill(polls.length === 1
      ? { status: 202, json: { pending: true } }
      : { json: { success: true, account_id: 42, platform: 'kimi' } });
  });
  await page.goto('/admin/subscription-accounts');
  await page.getByRole('button', { name: 'OAuth 授权绑定' }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('平台', { exact: true }).selectOption('kimi');
  await dialog.getByLabel('路由分组').fill('coding');
  await dialog.getByRole('button', { name: '生成授权链接', exact: true }).click();
  await expect(dialog.getByLabel('设备码')).toHaveValue('CODE-123');
  await expect(dialog.getByRole('link', { name: '打开授权链接' })).toHaveAttribute('href', 'https://auth.kimi.com/device-fixture');
  await expect(dialog.getByLabel('路由分组')).toBeDisabled();
  await expect(dialog.getByLabel('授权码 / 回调 URL')).toHaveCount(0);
  await dialog.getByRole('button', { name: '完成绑定' }).click();
  await expect(page.getByText('设备授权尚未完成，请在授权页确认后重试。')).toBeVisible();
  await expect(dialog).toBeVisible();
  await dialog.getByRole('button', { name: '完成绑定' }).click();
  await expect(dialog).not.toBeVisible();
  expect(authRequests).toEqual([{ group: 'coding' }]);
  expect(polls).toHaveLength(2);
  expect(polls[1]).toMatchObject({ group: 'coding', code: '', session_id: 'fixture-session', state: 'fixture-state' });
});

test('closing the OAuth dialog discards the prior authorization response', async ({ page }) => {
  let completeRequest!: () => void;
  const deferred = new Promise<void>(resolve => { completeRequest = resolve; });
  await page.route('**/api/v1/admin/accounts/subscription/oauth/kimi/auth-url', async (route) => {
    await deferred;
    await route.fulfill({ json: {
      auth_url: 'https://auth.kimi.com/device-fixture', session_id: 'discarded', state: 'fixture-state', user_code: 'STALE-CODE',
      expires_at: Math.floor(Date.now() / 1000) + 300,
    } });
  });
  await page.goto('/admin/subscription-accounts');
  await page.getByRole('button', { name: 'OAuth 授权绑定' }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('平台', { exact: true }).selectOption('kimi');
  const requested = page.waitForRequest('**/api/v1/admin/accounts/subscription/oauth/kimi/auth-url');
  await dialog.getByRole('button', { name: '生成授权链接', exact: true }).click();
  await requested;
  await expect(dialog.getByLabel('平台', { exact: true })).toBeDisabled();
  await page.keyboard.press('Escape');
  await expect(dialog).not.toBeVisible();
  await page.getByRole('button', { name: 'OAuth 授权绑定' }).click();
  completeRequest();
  await expect(dialog.getByRole('button', { name: '生成授权链接', exact: true })).toBeEnabled();
  await expect(dialog.getByLabel('设备码')).toHaveCount(0);
});
