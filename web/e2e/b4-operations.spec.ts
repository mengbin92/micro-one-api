import { expect, test } from '@playwright/test';
import type { Page } from '@playwright/test';

async function session(page: Page) {
  await page.route('**/api/**', route => route.fulfill({ json: { success: true, data: [] } }));
  await page.route('**/api/user/authorization?**', route => route.fulfill({ json: { authorization_mode: 'legacy', legacy_admin: true } }));
  await page.route('**/api/user/self', route => route.fulfill({ json: { success: true, data: { id: 1, username: 'b4-fixture', role: 100 } } }));
  await page.addInitScript(() => { localStorage.setItem('token', 'b4-browser-fixture'); localStorage.setItem('userRole', '100'); localStorage.setItem('web:language', JSON.stringify('zh-CN')); });
}
test('user searches own models, reads plain announcements and handles failed refresh', async ({ page }, info) => {
  await session(page);
  let read = false;
  let unavailable = false;
  await page.route('**/api/user/model-availability', route => unavailable ? route.fulfill({ status: 503, json: { error: 'unavailable' } }) : route.fulfill({ json: { status: 'available', models: ['model-chat', 'model-code'], checked_at: 1793580000 } }));
  await page.route('**/api/user/announcements?**', route => route.fulfill({ json: { total: 1, items: [{ id: 'fixture-notice', revision: 2, title: '服务升级通知', body: '<script>plain text</script>\n服务升级期间请留意公告。', publish_at: 1793580000, read }] } }));
  await page.route('**/api/user/announcements/read', async route => { expect(route.request().postDataJSON()).toEqual({ id: 'fixture-notice', revision: 2 }); read = true; return route.fulfill({ json: { success: true } }); });
  await page.goto('/operations');
  await expect(page.getByRole('heading', { name: '模型与公告', exact: true })).toBeVisible();
  await expect(page.getByText('model-chat', { exact: true })).toBeVisible();
  await page.getByRole('textbox', { name: '搜索模型' }).fill('code');
  await expect(page.getByText('model-chat', { exact: true })).toHaveCount(0);
  await expect(page.getByText('model-code', { exact: true })).toBeVisible();
  await page.getByRole('textbox', { name: '搜索模型' }).fill('missing');
  await expect(page.getByText('没有匹配的可用模型')).toBeVisible();
  await page.getByRole('textbox', { name: '搜索模型' }).clear();
  await expect(page.getByText('<script>plain text</script>', { exact: false })).toBeVisible();
  await page.getByRole('button', { name: '标为已读', exact: true }).click();
  await expect(page.getByText('已读', { exact: true })).toBeVisible();
  expect(read).toBe(true);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.evaluate(() => scrollTo(0, 0));
  await page.screenshot({ path: info.outputPath('user-operations.png'), fullPage: true });
  unavailable = true;
  await page.getByRole('button', { name: '刷新', exact: true }).click();
  await expect(page.getByRole('alert').filter({ hasText: '模型状态暂不可用' })).toBeVisible();
  await expect(page.getByText('有可用路由', { exact: true })).toHaveCount(0);
});

test('admin targets, schedules, edits and archives with CAS; conflict preserves draft', async ({ page }, info) => {
  await session(page);
  type Row = { id: string; revision: number; title: string; body: string; audience: string; targets: string[]; status: string; publish_at: number; expires_at: number };
  let row: Row | undefined;
  let conflict = false;
  let releaseFirstSave = () => {};
  const firstSaveGate = new Promise<void>(resolve => { releaseFirstSave = resolve; });
  let firstSave = true;
  await page.route('**/api/admin/announcements**', async route => {
    if (route.request().method() === 'PUT') {
      const body = route.request().postDataJSON() as { announcement: Row; reason: string };
      expect(body.reason).toBe('例行服务通知');
      if (conflict) return route.fulfill({ status: 409, json: { error: 'conflict' } });
      expect(body.announcement.revision).toBe(row?.revision ?? 0);
      row = { ...body.announcement, id: 'fixture-notice', revision: (row?.revision ?? 0) + 1 };
      if (firstSave) { firstSave = false; await firstSaveGate; }
      return route.fulfill({ json: row });
    }
    return route.fulfill({ json: { items: row ? [row] : [], total: row ? 1 : 0 } });
  });
  await page.goto('/admin/announcements');
  await expect(page.getByText('暂无公告', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: '新建公告', exact: true }).click();
  await page.getByLabel('公告标题', { exact: true }).fill('计划维护通知');
  await page.getByLabel('公告正文', { exact: true }).fill('请提前保存工作。');
  await page.getByLabel('公告受众', { exact: true }).selectOption('groups');
  await page.getByLabel('用户默认路由组 ID（逗号分隔）').fill('2, 3');
  await page.getByLabel('发布状态', { exact: true }).selectOption('published');
  await page.getByLabel('发布时间（北京时间，留空立即）').fill('2030-01-02T09:30');
  await page.getByLabel('下架时间（北京时间，留空不限）').fill('2030-01-01T09:30');
  await expect(page.getByRole('alert').filter({ hasText: '下架时间必须晚于发布时间' })).toBeVisible();
  await page.getByLabel('下架时间（北京时间，留空不限）').fill('2030-01-03T09:30');
  await page.getByLabel('操作原因', { exact: true }).fill('例行服务通知');
  await page.getByRole('button', { name: '保存公告', exact: true }).click();
  try {
    await expect(page.getByRole('button', { name: '新建公告', exact: true })).toBeDisabled();
    await expect(page.getByLabel('公告标题', { exact: true })).toBeDisabled();
  } finally { releaseFirstSave(); }
  await expect(page.getByText('公告已保存', { exact: true })).toBeVisible();
  expect(row?.targets).toEqual(['2', '3']);
  expect(row?.publish_at).toBe(Date.parse('2030-01-02T09:30:00+08:00') / 1000);
  await expect(page.getByText(/待发布 · 指定用户分组/)).toBeVisible();
  await page.getByRole('button', { name: '编辑', exact: true }).click();
  await page.getByLabel('公告受众', { exact: true }).selectOption('users');
  await expect(page.getByLabel('用户 ID（逗号分隔）')).toHaveValue('');
  await page.getByLabel('用户 ID（逗号分隔）').fill('7');
  await page.getByLabel('发布状态', { exact: true }).selectOption('archived');
  await page.getByLabel('操作原因', { exact: true }).fill('例行服务通知');
  await page.evaluate(() => scrollTo(0, 0));
  await page.screenshot({ path: info.outputPath('announcement-editor.png'), fullPage: true });
  conflict = true;
  await page.getByRole('button', { name: '保存公告', exact: true }).click();
  await expect(page.getByRole('alert').filter({ hasText: '保存失败' })).toBeVisible();
  await expect(page.getByLabel('公告标题', { exact: true })).toHaveValue('计划维护通知');
  conflict = false;
  await page.getByRole('button', { name: '保存公告', exact: true }).click();
  await expect(page.getByText(/已下架 · 指定用户/)).toBeVisible();
  expect(row?.targets).toEqual(['7']);
  expect(row?.revision).toBe(2);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.evaluate(() => scrollTo(0, 0));
  await page.screenshot({ path: info.outputPath('announcement-admin.png'), fullPage: true });
});

test('empty availability, announcement paging and failed read receipts stay explicit', async ({ page }) => {
  await session(page);
  let empty = true;
  let secondPageExpired = false;
  await page.route('**/api/user/model-availability', route => route.fulfill({ json: { status: 'no_available_route', models: [], checked_at: 1793580000 } }));
  await page.route('**/api/user/announcements?**', route => {
    const number = Number(new URL(route.request().url()).searchParams.get('page'));
    return route.fulfill({ json: empty ? { items: [], total: 0 } : secondPageExpired && number === 2 ? { items: [], total: 1 } : { total: 21, items: [{ id: `notice-${number}`, revision: 1, title: `第${number}页通知`, body: '请确认已读', publish_at: 1793580000, read: false }] } });
  });
  await page.route('**/api/user/announcements/read', route => route.fulfill({ status: 404, json: { error: 'announcement changed' } }));
  await page.goto('/operations');
  await expect(page.getByText('暂无可用路由', { exact: true })).toBeVisible();
  await expect(page.getByText('暂无公告', { exact: true })).toBeVisible();
  empty = false;
  await page.getByRole('button', { name: '刷新', exact: true }).click();
  await expect(page.getByRole('heading', { name: '第1页通知' })).toBeVisible();
  await page.getByRole('button', { name: '下一页', exact: true }).click();
  await expect(page.getByRole('heading', { name: '第2页通知' })).toBeVisible();
  await expect(page.getByRole('button', { name: '下一页', exact: true })).toBeDisabled();
  secondPageExpired = true;
  await page.getByRole('button', { name: '刷新', exact: true }).click();
  await expect(page.getByText('本页暂无公告，请返回上一页。', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: '上一页', exact: true }).click();
  await page.getByRole('button', { name: '标为已读', exact: true }).click();
  await expect(page.getByRole('alert').filter({ hasText: '已读保存失败' })).toBeVisible();
  await expect(page.getByText('未读', { exact: true })).toBeVisible();
});

test('admin cancel keeps saved content and all-user drafts have no targets', async ({ page }) => {
  await session(page);
  let saved = false;
  await page.route('**/api/admin/announcements**', async route => {
    if (route.request().method() === 'PUT') {
      const body = route.request().postDataJSON();
      expect(body.announcement.audience).toBe('all');
      expect(body.announcement.targets).toEqual([]);
      expect(body.announcement.status).toBe('draft');
      saved = true;
      return route.fulfill({ json: { ...body.announcement, id: 'draft', revision: 1 } });
    }
    return route.fulfill({ json: { total: saved ? 1 : 0, items: saved ? [{ id: 'draft', revision: 1, title: '全体草稿', body: '草稿内容', audience: 'all', status: 'draft', publish_at: 0 }] : [] } });
  });
  await page.goto('/admin/announcements');
  await page.getByRole('button', { name: '新建公告', exact: true }).click();
  await page.getByLabel('公告标题', { exact: true }).fill('放弃的草稿');
  await page.getByRole('button', { name: '取消', exact: true }).click();
  await expect(page.getByLabel('公告标题', { exact: true })).toHaveCount(0);
  expect(saved).toBe(false);
  await page.getByRole('button', { name: '新建公告', exact: true }).click();
  await page.getByLabel('公告标题', { exact: true }).fill('全体草稿');
  await page.getByLabel('公告正文', { exact: true }).fill('草稿内容');
  await page.getByLabel('操作原因', { exact: true }).fill('全体草稿测试');
  await page.getByRole('button', { name: '保存公告', exact: true }).click();
  await expect(page.getByRole('heading', { name: '全体草稿' })).toBeVisible();
});
