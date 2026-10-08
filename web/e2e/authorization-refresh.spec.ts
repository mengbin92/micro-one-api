import { expect, test } from '@playwright/test';
import { mockApi } from './fixtures';

for (const fixture of [
  { page: 'channels', endpoint: 'channel', permission: 'channel.channel.list', action: 'channel.channel.update', export: 'channel.channel.export', label: '编辑', row: { id: '1', name: 'stable channel', type: 1, status: 1, group: 'default', models: 'gpt-4o' } },
  { page: 'users', endpoint: 'user', permission: 'identity.user.list', action: 'identity.user.disable', export: 'identity.user.export', label: '禁用', row: { id: '1', username: 'stable user', status: 1, group: 'default' } },
  { page: 'logs', endpoint: 'log', permission: 'log.request.list', action: 'log.request.delete', export: 'log.request.export', label: '清理', row: { id: '1', userId: '1', type: 'consume', amount: 0, createdAt: '1760000000' } },
]) {
  test(`${fixture.page} retains actions, export and owner cache across 90 seconds of authorization polling`, async ({ page }) => {
    await page.clock.install();
    await mockApi(page, { admin: true });
    let authCalls = 0;
    let ownerCalls = 0;
    let revoked = false;
    let release: (() => void) | undefined;
    await page.route('**/api/user/authorization?**', async route => {
      if (++authCalls > 1 && !revoked) await new Promise<void>(resolve => { release = resolve; });
      await route.fulfill({ json: {
        authorization_mode: 'iam', session: { activation_state: 'active', revision: '1' },
        versions: { policy_revision: '1' }, valid_until: new Date(Date.now() + 600_000).toISOString(),
        permitted_operations: revoked ? ['admin.console.enter'] : ['admin.console.enter', fixture.permission, fixture.action, fixture.export],
      } });
    });
    await page.route(`**/api/${fixture.endpoint}?**`, route => {
      ownerCalls++;
      return route.fulfill({ json: { success: true, data: [fixture.row] } });
    });
    await page.addInitScript(() => {
      localStorage.setItem('token', 'stable-management-fixture');
      localStorage.setItem('web:language', JSON.stringify('zh-CN'));
    });
    try {
      await page.goto(`/admin/${fixture.page}`);
      const action = page.getByRole('button', { name: fixture.label, exact: true });
      const download = page.getByRole('button', { name: 'Export CSV', exact: true });
      await expect(action).toBeEnabled();
      await expect(download).toBeEnabled();
      // Mark real DOM instances: replacing them can lose focus or local state
      // even when identical text comes back after the network request.
      await action.evaluate(button => button.setAttribute('data-refresh-instance', 'action'));
      await download.evaluate(button => button.setAttribute('data-refresh-instance', 'export'));
      for (let cycle = 0; cycle < 3; cycle++) {
        await page.clock.fastForward(31_000);
        await expect.poll(() => authCalls).toBe(cycle + 2);
        await expect(action).toBeVisible();
        await expect(action).toBeDisabled();
        await expect(action).toHaveAttribute('data-refresh-instance', 'action');
        await expect(download).toBeVisible();
        await expect(download).toBeDisabled();
        await expect(download).toHaveAttribute('data-refresh-instance', 'export');
        expect(ownerCalls).toBe(1);
        release?.();
        await expect(action).toBeEnabled();
        await expect(download).toBeEnabled();
        expect(ownerCalls).toBe(1);
      }
      if (fixture.page === 'channels') {
        await action.click();
        const dialog = page.getByRole('dialog', { name: '模型配置', exact: true });
        const name = dialog.getByLabel('名称', { exact: true });
        await name.fill('unsaved channel draft');
        const save = dialog.getByRole('button', { name: '保存配置', exact: true });
        await expect(save).toBeEnabled();
        await page.clock.fastForward(31_000);
        await expect.poll(() => authCalls).toBe(5);
        await expect(dialog).toBeVisible();
        await expect(name).toHaveValue('unsaved channel draft');
        await expect(save).toBeVisible();
        await expect(save).toBeDisabled();
        release?.();
        await expect(save).toBeEnabled();
        await expect(name).toHaveValue('unsaved channel draft');
      }
      revoked = true;
      await page.clock.fastForward(31_000);
      await expect(page.getByText('需要管理员权限')).toBeVisible();
      await expect(action).toHaveCount(0);
      await expect(download).toHaveCount(0);
      await expect(page.getByRole('dialog')).toHaveCount(0);
      expect(ownerCalls).toBe(1);
    } finally { release?.(); }
  });
}

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
