import { expect, test } from '@playwright/test';
// UI-only fixtures; actual HTTP/RPC owners and empty restore run in Go tests.
test('backup policy CAS, uncertain submission and receipts fit desktop/mobile', async ({ page }, info) => {
  let policy = { revision: 1, enabled: false, interval_hours: 24, retain: 7, rpo_hours: 24, rto_minutes: 60 };
  const ids: string[] = [];
  await page.route('**/api/**', route => route.fulfill({ json: { success: true, data: [] } }));
  await page.route('**/api/user/authorization?**', route => route.fulfill({ json: { authorization_mode: 'legacy', legacy_admin: true } }));
  await page.route('**/api/user/self', route => route.fulfill({ json: { success: true, data: { id: 1, username: 'backup-fixture', role: 100 } } }));
  await page.route('**/api/admin/backup-policy', async route => {
    if (route.request().method() === 'PUT') { const body = route.request().postDataJSON(); expect(body.reason).toBe('隔离恢复演练'); expect(body.policy.revision).toBe(1); policy = { ...body.policy, revision: 2 }; }
    return route.fulfill({ json: policy });
  });
  await page.route('**/api/admin/backups', async route => {
    if (route.request().method() === 'POST') { const body = route.request().postDataJSON(); ids.push(body.request_id); if (ids.length === 1) return route.fulfill({ status: 503, json: { message: 'fixture outcome unknown' } }); return route.fulfill({ json: { id: body.request_id, status: 'queued' } }); }
    return route.fulfill({ json: { jobs: [{ id: 'backup-fixture-uncertain', status: 'uncertain', created_at: 1793300000, error_code: 'worker_interrupted' }, { id: 'backup-fixture-success', status: 'succeeded', created_at: 1793290000, bytes: 1048576 }], events: [{ id: '1', action: 'queued', actor: '1', reason: '隔离恢复演练', at: 1793300000 }], worker_last_seen: Math.floor(Date.now() / 1000), worker_phase: 'ready' } });
  });
  await page.addInitScript(() => { localStorage.setItem('token', 'backup-browser-fixture'); localStorage.setItem('userRole', '100'); localStorage.setItem('web:language', JSON.stringify('zh-CN')); });
  await page.goto('/admin/options');
  await expect(page.getByText('管理化备份', { exact: true })).toBeVisible();
  await page.getByLabel('保留成功备份数').fill('4'); await page.getByLabel('操作原因', { exact: true }).fill('隔离恢复演练');
  await page.getByRole('button', { name: '保存备份策略', exact: true }).click(); await expect(page.getByText('备份策略已保存', { exact: true })).toBeVisible(); expect(policy.retain).toBe(4);
  await page.getByRole('button', { name: '立即备份', exact: true }).click(); await expect(page.getByRole('alert')).toContainText('fixture outcome unknown');
  await page.getByRole('button', { name: '立即备份', exact: true }).click(); await expect(page.getByText('任务已排队，备份完成后可查看结果。')).toBeVisible(); expect(ids).toHaveLength(2); expect(ids[0]).toBe(ids[1]);
  await expect(page.getByRole('cell').filter({ hasText: '结果不确定' })).toBeVisible(); await expect(page.getByText('备份成功', { exact: true })).toBeVisible();
  await page.getByText('备份审计', { exact: true }).click();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.getByText('管理化备份', { exact: true }).scrollIntoViewIfNeeded(); await page.screenshot({ path: info.outputPath('managed-backup.png'), fullPage: true });
});
