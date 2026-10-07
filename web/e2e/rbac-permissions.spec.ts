import { test, expect, type Page } from '@playwright/test';
import { readFileSync } from 'node:fs';
const fixture = JSON.parse(readFileSync(process.env.RBAC_C_FIXTURE!, 'utf8')) as { tokens: Record<string, string>; users: Record<string, number> };
async function signIn(page: Page, name: string, path: string) {
 await page.addInitScript(({ token, id }) => { localStorage.setItem('token', token); localStorage.setItem('userId', String(id)); localStorage.setItem('userRole', '100'); localStorage.setItem('locale', 'zh-CN'); }, { token: fixture.tokens[name], id: fixture.users[name] });
 await page.goto(path);
}
function headers(name: string) { return { Authorization: `Bearer ${fixture.tokens[name]}`, 'x-authorization-reason': 'C browser negative acceptance' }; }

for (const viewport of [{ name: 'desktop', width: 1280, height: 800 }, { name: 'mobile', width: 390, height: 844 }]) {
 test(`permission details remain in the ${viewport.name} viewport after a long catalog`, async ({ page }) => {
  await page.setViewportSize({ width: viewport.width, height: viewport.height });
  await signIn(page, 'root', '/admin/iam/permissions');
  const details = page.getByRole('button', { name: '详情', exact: true });
  await expect(details.first()).toBeVisible();
  await details.first().click();
  const dialog = page.getByRole('dialog');
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole('textbox', { name: '说明', exact: true })).toBeVisible();
  const bounds = await dialog.boundingBox();
  expect(bounds).not.toBeNull();
  expect(bounds!.x).toBeGreaterThanOrEqual(0);
  expect(bounds!.y).toBeGreaterThanOrEqual(0);
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(viewport.width);
  expect(bounds!.y + bounds!.height).toBeLessThanOrEqual(viewport.height);
  const originalName = await dialog.getByRole('textbox', { name: '名称', exact: true }).inputValue();
  await page.screenshot({ path: `test-results/iam-permission-${viewport.name}.png` });
  await dialog.getByRole('button', { name: '关闭', exact: true }).click();
  await expect(dialog).toHaveCount(0);
  await details.nth(1).click();
  await expect(dialog.getByRole('textbox', { name: '名称', exact: true })).not.toHaveValue(originalName);
 });
}

test('alice: inherited group read, shared-group whole-write denial, secrets and ancillary polling', async ({ page, request }) => {
 const requests: string[] = []; page.on('request', req => requests.push(req.url()));
 await signIn(page, 'alice', '/admin');
 await expect(page).toHaveURL(/\/admin\/channels$/);
 await expect(page.getByText('channel-7', { exact: true })).toBeVisible();
 await expect(page.getByText('channel-9', { exact: true })).toBeVisible();
 await expect(page.getByText('channel-8', { exact: true })).toHaveCount(0);
 const shared = page.getByRole('row').filter({ hasText: 'channel-9' });
 await expect(shared.getByRole('button', { name: '禁用', exact: true })).toHaveCount(0);
 const east = page.getByRole('row').filter({ hasText: 'channel-7' });
 await expect(east.getByRole('button', { name: '禁用', exact: true })).toBeVisible();
 await expect(page.getByRole('button', { name: '编辑', exact: true })).toHaveCount(0);
 expect(requests.filter(url => /notifications|subscription-accounts/.test(url))).toEqual([]);
 expect(await (await request.get('/api/channel/8', { headers: headers('alice') })).status()).toBe(403);
 const detail = await request.get('/api/channel/9', { headers: headers('alice') }); expect(detail.status()).toBe(200); expect(await detail.text()).not.toContain('c-private-key');
 expect((await request.post('/api/channel/disable/9?expected_revision=1', { headers: headers('alice') })).status()).toBe(403);
 await page.screenshot({ path: 'test-results/rbac-alice.png', fullPage: true });
});

test('finance reader: first accessible page and actual scoped orders, independent refunds and export', async ({ page, request }) => {
 await signIn(page, 'finance', '/admin'); await expect(page).toHaveURL(/\/admin\/payment-orders$/);
 await expect(page.getByText(`c-order-${fixture.users.member}`, { exact: true })).toBeVisible();
 await expect(page.getByText(`c-order-${fixture.users.bob}`, { exact: true })).toHaveCount(0);
 expect((await request.get('/api/v1/admin/reports/cost:export', { headers: headers('finance') })).status()).toBe(403);
 const refund = await request.post('/api/v1/admin/payments/refund', { headers: headers('finance'), data: { trade_no: `c-order-${fixture.users.member}`, reason: 'denied refund', expected_revision: '1' } });
 const denied = await refund.json(); expect(denied.success).toBe(false); expect(denied.message).toMatch(/authorization|permission|denied/i);
});

test('auditor: audit page and no management or export authority', async ({ page, request }) => {
 await signIn(page, 'auditor', '/admin'); await expect(page).toHaveURL(/\/admin\/iam\/audits$/);
 await expect(page.getByRole('heading', { name: '授权审计', exact: true })).toBeVisible();
 await expect(page.getByRole('button', { name: '导出授权审计' })).toHaveCount(0);
 expect((await request.get('/api/v1/admin/iam/audit-events:export', { headers: headers('auditor') })).status()).toBe(403);
 expect((await request.post('/api/v1/admin/iam/roles', { headers: headers('auditor'), data: { role: { code: 'forbidden', name: 'forbidden' }, expected_policy_revision: (await (await request.get('/api/user/authorization', { headers: headers('auditor') })).json()).versions.policy_revision, reason: 'denied', request_id: 'denied' } })).status()).toBe(403);
});

test('member: self roles remain accessible; numeric JWT and local role cannot enter console', async ({ page, request }) => {
 await signIn(page, 'member', '/session-roles'); await expect(page.getByRole('heading', { name: '我的会话角色', exact: true })).toBeVisible();
 await expect(page.getByRole('link', { name: '进入管理' })).toHaveCount(0);
 expect((await request.get('/api/user', { headers: headers('member') })).status()).toBe(403);
 await page.goto('/admin/users'); await expect(page.getByRole('alert').filter({ hasText: '需要管理员权限' })).toBeVisible();
});

test('bob: delegated assignment preview and commit; hidden targets and self assignment rejected', async ({ page, request }) => {
 await signIn(page, 'bob', `/admin/iam/assignments?user_id=${fixture.users.member}`);
 await page.getByLabel('角色', { exact: true }).selectOption('53');
 await page.getByRole('button', { name: '预检分配', exact: true }).click();
 await page.getByLabel('变更原因', { exact: true }).fill('C bob delegated membership');
 await page.getByRole('button', { name: '预检变更', exact: true }).click();
 await expect(page.getByRole('button', { name: '确认提交', exact: true })).toBeEnabled();
 await page.getByRole('button', { name: '确认提交', exact: true }).click();
 await expect(page.getByRole('dialog')).toHaveCount(0);
 const auth = await (await request.get('/api/user/authorization', { headers: headers('bob') })).json();
 const policy = auth.versions.policy_revision;
 const body = (uid: number) => ({ user_id: String(uid), assignment: { user_id: String(uid), role_id: '53', boundary: { clauses: [{ all: true }] }, validity: { starts_at: new Date().toISOString() } }, expected_policy_revision: policy, reason: 'C negative delegation', request_id: 'c-negative' });
 expect((await request.post(`/api/v1/admin/iam/users/${fixture.users.bob}/roles`, { headers: headers('bob'), data: body(fixture.users.bob) })).status()).toBe(403);
 expect((await request.post(`/api/v1/admin/iam/users/${fixture.users.alice}/roles`, { headers: headers('bob'), data: body(fixture.users.alice) })).status()).toBe(403);
});

test('root: role creation uses server preview and same-revision digest; permission tree and matrix share edits', async ({ page, request }) => {
 await signIn(page, 'root', '/admin/iam/roles');
 const inherited = await request.get('/api/v1/admin/iam/roles/52/permissions', { headers: headers('root') });
 expect(inherited.status()).toBe(200);
 expect((await inherited.json()).sources).toEqual(expect.arrayContaining([expect.objectContaining({ operation: 'channel.channel.list', inheritance_path: ['52', '51'] })]));
 await page.getByLabel('角色代码', { exact: true }).fill('c-browser-created'); await page.getByLabel('名称', { exact: true }).fill('C browser role');
 await page.getByRole('button', { name: '保存角色资料', exact: true }).click();
 await page.getByLabel('变更原因', { exact: true }).fill('C browser role lifecycle');
 await page.getByRole('button', { name: '预检变更', exact: true }).click();
 await expect(page.getByRole('button', { name: '确认提交', exact: true })).toBeEnabled();
 await page.getByRole('button', { name: '确认提交', exact: true }).click(); await expect(page.getByRole('dialog')).toHaveCount(0);
 const row = page.getByRole('row').filter({ hasText: 'C browser role' }); await expect(row).toBeVisible(); await row.getByRole('button', { name: '详情', exact: true }).click();
 await expect(page.getByRole('button', { name: '预检并保存授权', exact: true })).toBeEnabled();
 await page.getByLabel('admin.console.enter 直接授权', { exact: true }).selectOption('allow');
 await page.getByRole('button', { name: '资源 × 操作矩阵', exact: true }).click();
 await expect(page.getByLabel('admin.console.enter 直接授权', { exact: true })).toHaveValue('allow');
 await expect(page.getByRole('columnheader', { name: '资源', exact: true })).toBeVisible();
 await page.screenshot({ path: 'test-results/rbac-role-matrix.png', fullPage: true });
 await page.getByRole('button', { name: '预检并保存授权', exact: true }).click();
 await page.getByLabel('变更原因', { exact: true }).fill('C explicit tree/matrix grant');
 await page.getByRole('button', { name: '预检变更', exact: true }).click();
 await expect(page.getByRole('button', { name: '确认提交', exact: true })).toBeEnabled();
 await page.getByRole('button', { name: '确认提交', exact: true }).click();
 await expect(page.getByRole('dialog')).toHaveCount(0);
 const roles = await (await request.get('/api/v1/admin/iam/roles', { headers: headers('root') })).json();
 const created = roles.roles.find((role: { code: string }) => role.code === 'c-browser-created');
 expect(created.status).toBe('draft');
 const saved = await (await request.get(`/api/v1/admin/iam/roles/${created.id}/permissions`, { headers: headers('root') })).json();
 expect(saved.roles[0].grants).toEqual([expect.objectContaining({ operation: 'admin.console.enter', effect: 'allow' })]);
});

test('DSD: select only assigned roles before business access, reject conflicting activation', async ({ page, request }) => {
 await signIn(page, 'selection', '/admin'); await expect(page).toHaveURL(/\/session-roles$/);
 await expect(page.getByText('selection_required', { exact: false })).toBeVisible();
 const before = await (await request.get('/api/user/authorization', { headers: headers('selection') })).json();
 expect(before.permitted_operations ?? []).toEqual([]);
 await page.getByRole('checkbox').nth(0).uncheck();
 const finance = page.locator('label').filter({ hasText: 'finance_reader' });
 const auditor = page.locator('label').filter({ hasText: 'auditor' });
 await finance.getByRole('checkbox').check(); await auditor.getByRole('checkbox').check();
 await page.locator('label').filter({ hasText: '变更原因' }).getByRole('textbox').fill('C DSD conflict');
 await page.getByRole('button', { name: '激活所选角色', exact: true }).click();
 await expect(page.getByRole('alert').filter({ hasText: /constraint|职责|分离|授权/i })).toBeVisible();
 await auditor.getByRole('checkbox').uncheck();
 await page.getByRole('button', { name: '激活所选角色', exact: true }).click();
 await expect(page.getByRole('main').getByRole('link', { name: '进入管理', exact: true })).toBeVisible();
 await page.getByRole('main').getByRole('link', { name: '进入管理', exact: true }).click(); await expect(page).toHaveURL(/\/admin\/payment-orders$/);
});
