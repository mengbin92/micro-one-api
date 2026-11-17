import { expect, test } from '@playwright/test';
import { mockApi } from './fixtures';

const quotaPolicy = {
  id: 7, revision: 3, name: 'team-quota', display_name: '团队额度',
  platform: 'gemini', subscription_type: 'standard',
  daily_limit_usd: 10, weekly_limit_usd: null, monthly_limit_usd: 100,
  rate_multiplier: 1, status: 1, price_quota: 10, duration_days: 30,
  created_at: 1700000000, updated_at: 1700000000,
};

test.beforeEach(async ({ page }) => {
  await mockApi(page, { admin: true });
  await page.route('**/api/v1/admin/subscription-groups', route => route.fulfill({
    json: { success: true, data: [quotaPolicy] },
  }));
  await page.route('**/api/v1/admin/routing-groups?**', route => route.fulfill({
    json: { success: true, data: { groups: [{ id: 1, key: 'team', display_name: '团队路由' }], next_page_token: '' } },
  }));
  await page.addInitScript(() => {
    localStorage.setItem('token', 'subscription-admin-fixture');
    localStorage.setItem('web:language', JSON.stringify('zh-CN'));
  });
});

test('quota policies stay under user products and preserve their hidden platform on save', async ({ page }) => {
  const writes: Record<string, unknown>[] = [];
  await page.route('**/api/v1/admin/subscription-groups/7', async route => {
    writes.push(route.request().postDataJSON());
    await route.fulfill({ json: { success: true } });
  });
  await page.goto('/admin/subscription-groups');
  await expect(page.getByRole('cell', { name: '团队额度 team-quota' })).toBeVisible();
  await expect(page.getByRole('columnheader', { name: '平台', exact: true })).toHaveCount(0);
  if ((page.viewportSize()?.width ?? 1280) < 1024) {
    await page.getByRole('button', { name: '打开导航' }).click();
  }
  await expect(page.getByRole('button', { name: '用户与产品', exact: true })).toHaveAttribute('aria-expanded', 'true');
  await expect(page.getByRole('link', { name: '订阅额度策略', exact: true })).toBeVisible();
  await page.getByRole('link', { name: '订阅额度策略', exact: true }).click();
  await page.getByRole('button', { name: '编辑', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: '编辑订阅额度策略', exact: true });
  await expect(dialog.getByText(/平台/)).toHaveCount(0);
  await dialog.getByLabel('显示名称', { exact: true }).fill('团队额度更新');
  await dialog.getByRole('button', { name: '保存配置', exact: true }).click();
  await expect.poll(() => writes[0]).toMatchObject({ display_name: '团队额度更新', platform: 'gemini', expected_revision: 3 });
  await expect(dialog).toHaveCount(0);
});

test('plans display policy names and save the policy selected from the dropdown', async ({ page }) => {
  const writes: Record<string, unknown>[] = [];
  await page.route('**/api/v1/admin/subscription-plans**', async route => {
    if (route.request().method() === 'POST') {
      writes.push(route.request().postDataJSON());
      await route.fulfill({ json: { success: true } });
      return;
    }
    await route.fulfill({ json: { success: true, data: [{
      id: 2, revision: 1, name: '团队月套餐', product_name: '团队产品', group_id: 7,
      price_quota: 10, validity_days: 30, validity_unit: 'day', for_sale: false,
      sort_order: 0, created_at: 1700000000, updated_at: 1700000000,
    }] } });
  });
  await page.goto('/admin/subscription-plans');
  await expect(page.getByRole('cell', { name: '团队额度', exact: true })).toBeVisible();
  await page.getByRole('button', { name: '新建套餐', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: '新建套餐', exact: true });
  await dialog.getByLabel('套餐名称', { exact: true }).fill('新团队套餐');
  await dialog.getByRole('checkbox', { name: '团队路由 (team)', exact: true }).check();
  await dialog.getByLabel('额度策略', { exact: true }).selectOption('7');
  await expect(dialog.getByText('10 / 不限 / 100 (日/周/月 USD)', { exact: false })).toBeVisible();
  await dialog.getByRole('button', { name: '保存', exact: true }).click();
  await expect.poll(() => writes[0]).toMatchObject({ name: '新团队套餐', group_id: 7 });
  await expect(dialog).toHaveCount(0);
});

test('assigning a subscription fills the policy term and accepts a custom expiry', async ({ page }) => {
  const writes: Record<string, unknown>[] = [];
  await page.route('**/api/v1/admin/subscriptions**', async route => {
    if (route.request().method() === 'POST') {
      writes.push(route.request().postDataJSON());
      await route.fulfill({ json: { success: true } });
      return;
    }
    await route.fulfill({ json: { success: true, data: [] } });
  });
  await page.goto('/admin/subscriptions');
  await page.getByRole('button', { name: '分配订阅', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: '分配订阅', exact: true });
  await dialog.getByRole('checkbox', { name: '团队路由 (team)', exact: true }).check();
  await dialog.getByLabel('用户 ID', { exact: true }).fill('42');
  await dialog.getByLabel('订阅额度策略', { exact: true }).selectOption('7');
  const expires = dialog.getByLabel('到期时间', { exact: true });
  const remainingDays = await expires.evaluate(input => (
    (new Date((input as HTMLInputElement).value).getTime() - Date.now()) / 86_400_000
  ));
  expect(remainingDays).toBeGreaterThan(29.99);
  expect(remainingDays).toBeLessThanOrEqual(30);
  await expect(dialog.getByText('10 / 不限 / 100 (日/周/月 USD)', { exact: false })).toBeVisible();
  await expires.fill('2030-01-02T12:00');
  const expectedExpiry = await expires.evaluate(input => Math.floor(new Date((input as HTMLInputElement).value).getTime() / 1000));
  await dialog.getByRole('button', { name: '分配', exact: true }).click();
  await expect.poll(() => writes[0]).toMatchObject({ user_id: 42, group_id: 7, expires_at: expectedExpiry });
  await expect(dialog).toHaveCount(0);
});
