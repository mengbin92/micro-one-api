import { expect, test, type Page } from '@playwright/test';
import { mockApi } from './fixtures';

async function openRoleEditor(page: Page) {
  await mockApi(page);
  await page.route('**/api/user/authorization?**', route => route.fulfill({ json: {
    authorization_mode: 'iam', session: { activation_state: 'active' },
    permitted_operations: ['admin.console.enter', 'iam.role.list', 'iam.permission.list', 'iam.role.permissions.read', 'iam.role.permissions.update', 'iam.role.hierarchy.update'],
    versions: { policy_revision: '7' }, valid_until: new Date(Date.now() + 60_000).toISOString(),
  } }));
  const role = { id: '1', code: 'layout_editor', name: 'Layout role', revision: '1', status: 'enabled', grants: [], inherits: ['2'] };
  await page.route('**/api/v1/admin/iam/roles?**', route => route.fulfill({ json: {
    roles: [role, { id: '2', code: 'junior', name: `junior_${'x'.repeat(110)}`, status: 'enabled' }], total: '2',
  } }));
  await page.route('**/api/v1/admin/iam/roles/1/permissions?**', route => route.fulfill({ json: { roles: [role] } }));
  await page.route('**/api/v1/admin/iam/permissions?**', route => route.fulfill({ json: {
    permissions: Array.from({ length: 24 }, (_, resource) => ['create', 'read', 'update', 'delete', 'enable', 'disable'].map(action => ({
      code: `channel.resource_${resource}.${action}`, name: `Resource ${resource} ${action}`, binding: 'bound', status: 'enabled',
    }))).flat(), total: '144',
  } }));
  await page.addInitScript(() => { localStorage.setItem('token', 'iam-layout-fixture'); localStorage.setItem('locale', 'zh-CN'); });
  await page.goto('/admin/iam/roles');
  await page.getByRole('row').filter({ hasText: 'Layout role' }).getByRole('button', { name: '详情', exact: true }).click();
  await expect(page.getByRole('textbox', { name: '名称', exact: true })).toHaveValue('Layout role');
  await expect(page.getByRole('button', { name: '允许当前子项' })).toBeVisible();
}

test('IAM matrix keeps both scroll axes in the visible viewport', async ({ page }) => {
  await openRoleEditor(page);
  await page.getByRole('button', { name: '资源 × 操作矩阵' }).click();
  const dimensions = await page.getByRole('table').last().evaluate(table => {
    const scrollport = table.parentElement!.parentElement!;
    scrollport.scrollLeft = scrollport.scrollWidth;
    return {
      horizontal: scrollport.scrollWidth > scrollport.clientWidth,
      vertical: scrollport.scrollHeight > scrollport.clientHeight,
      height: scrollport.getBoundingClientRect().height,
      scrolled: scrollport.scrollLeft > 0,
    };
  });
  expect(dimensions.horizontal).toBe(true);
  expect(dimensions.vertical).toBe(true);
  expect(dimensions.height).toBeLessThanOrEqual(514);
  expect(dimensions.scrolled).toBe(true);
});

test('IAM inheritance with long role names fits inside its card', async ({ page }) => {
  await openRoleEditor(page);
  const bounds = await page.getByRole('group', { name: '角色继承（senior → junior）' }).evaluate(group => {
    const card = group.closest('[data-slot="card"]')!;
    const cardBounds = card.getBoundingClientRect();
    return {
      cardRight: cardBounds.right,
      groupRight: group.getBoundingClientRect().right,
      graphRight: group.querySelector('[role="img"]')!.getBoundingClientRect().right,
      overflowing: card.scrollWidth > card.clientWidth,
    };
  });
  expect(bounds.groupRight).toBeLessThanOrEqual(bounds.cardRight);
  expect(bounds.graphRight).toBeLessThanOrEqual(bounds.cardRight);
  expect(bounds.overflowing).toBe(false);
});
