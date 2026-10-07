import { expect, test } from '@playwright/test';
import { mockApi } from './fixtures';

test('background authorization polling preserves the page, details draft and owner cache, while revocation removes them', async ({ page }) => {
  await page.clock.install();
  await mockApi(page, { admin: true });
  let authorizationCalls = 0;
  let permissionCalls = 0;
  let release: (() => void) | undefined;
  let revoke = false;
  const validUntil = new Date(Date.now() + 10 * 60_000).toISOString();
  await page.route('**/api/user/authorization?**', async route => {
    authorizationCalls++;
    if (authorizationCalls > 1 && !revoke) await new Promise<void>(resolve => { release = resolve; });
    await route.fulfill({ json: {
      authorization_mode: 'iam', session: { activation_state: 'active', revision: '1' },
      permitted_operations: revoke ? ['admin.console.enter'] : ['admin.console.enter', 'iam.permission.list', 'iam.permission.metadata.update'],
      versions: { policy_revision: revoke ? '2' : '1' }, valid_until: validUntil,
    } });
  });
  await page.route('**/api/v1/admin/iam/permissions?**', route => {
    permissionCalls++;
    return route.fulfill({ json: { permissions: [{ id: '1', resource_id: '1', name: 'Stable permission', code: 'channel.channel.list', description: 'original', status: 'enabled', revision: '1' }], total: '1' } });
  });
  await page.addInitScript(() => {
    localStorage.setItem('token', 'authorization-refresh-fixture');
    localStorage.setItem('web:language', JSON.stringify('zh-CN'));
  });
  try {
    await page.goto('/admin/iam/permissions');
    await expect(page.getByText('Stable permission', { exact: true })).toBeVisible();
    await page.getByRole('button', { name: '详情', exact: true }).click();
    const dialog = page.getByRole('dialog', { name: '详情', exact: true });
    await dialog.getByLabel('说明', { exact: true }).fill('unsaved description');
    const save = dialog.getByRole('button', { name: '保存目录资料' });
    await expect(save).toBeEnabled();
    await page.clock.fastForward(31_000);
    await expect.poll(() => authorizationCalls).toBe(2);
    await expect(dialog).toBeVisible();
    await expect(dialog.getByLabel('说明', { exact: true })).toHaveValue('unsaved description');
    // Background refresh retains the action's layout but blocks execution.
    await expect(save).toBeVisible();
    await expect(save).toBeDisabled();
    expect(permissionCalls).toBe(1);
    release?.();
    await expect(save).toBeEnabled();
    await expect(dialog.getByLabel('说明', { exact: true })).toHaveValue('unsaved description');
    expect(permissionCalls).toBe(1);

    revoke = true;
    await page.clock.fastForward(31_000);
    await expect.poll(() => authorizationCalls).toBe(3);
    await expect(page.getByText('需要管理员权限')).toBeVisible();
    await expect(dialog).toHaveCount(0);
    await expect(page.getByText('Stable permission', { exact: true })).toHaveCount(0);
    expect(permissionCalls).toBe(1);
  } finally { release?.(); }
});
