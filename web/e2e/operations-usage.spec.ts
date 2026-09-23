import { expect, test } from '@playwright/test';
import { mockApi } from './fixtures';

test.beforeEach(async ({ page }) => {
  await page.route('**/api/**', (route) => route.fulfill({ json: { success: true, data: [] } }));
  await mockApi(page);
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
