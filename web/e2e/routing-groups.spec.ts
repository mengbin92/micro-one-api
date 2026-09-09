import { expect, test } from '@playwright/test';
import { mockApi } from './fixtures';

test('admin routing group details preserve resource and model grant distinctions', async ({ page }) => {
  await mockApi(page);
  await page.addInitScript(() => {
    localStorage.setItem('token', 'test-user-token');
    localStorage.setItem('userId', '1');
    localStorage.setItem('userRole', '10');
  });
  await page.route('**/api/user/self', (route) => route.fulfill({ json: { success: true, data: { id: 1, username: 'admin', display_name: 'Admin', role: 10 } } }));
  const group = { id: 2, key: 'vip', display_name: '开发者服务', description: '混合资源与模型授权', status: 'enabled', access_mode: 'restricted', model_access_mode: 'all_authorized', revision: 1 };
  await page.route('**/api/v1/admin/routing-groups?*', (route) => route.fulfill({ json: { success: true, data: { groups: [group], next_page_token: '' } } }));
  await page.route('**/api/v1/admin/routing-groups/2', (route) => route.fulfill({ json: { success: true, data: { group,
    resources: [{ source_kind: 'channel', source_id: 1, priority: 7, weight: 5 }, { source_kind: 'subscription', source_id: 1, priority: 9, weight: 4 }],
    model_grants: [{ mapping_id: 3, account_id: 2, model: 'managed-model', upstream_model_id: 'upstream-model', priority: 13, enabled: true, extra_authorization: true }],
  } } }));
  await page.goto('/admin/routing-groups');
  await page.getByRole('button', { name: '查看分组 vip' }).click();
  await expect(page.getByText('模型级额外授权', { exact: true })).toBeVisible();
  await expect(page.getByText('API 渠道', { exact: true })).toBeVisible();
  await expect(page.getByText('上游订阅账号', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: '下一页' })).toBeDisabled();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.screenshot({ path: `/tmp/group-v2-b-${test.info().project.name}.png`, fullPage: true });
});
