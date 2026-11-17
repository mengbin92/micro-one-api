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
