import { expect, test } from '@playwright/test';
import { mockApi } from './fixtures';

test('admin routing group details preserve resource and model grant distinctions', async ({ page }) => {
  await mockApi(page, { admin: true });
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

test.describe('short-term routing keys', () => {
  test.use({ timezoneId: 'Asia/Shanghai' });

  test('creates a restricted key after step-up and displays it once', async ({ page }) => {
    await mockApi(page);
    await page.addInitScript(() => {
      localStorage.setItem('token', 'test-user-token');
      localStorage.setItem('userId', '1');
      localStorage.setItem('userRole', '1');
    });
    const key = 'sk-short-term-fixture';
    const maskedKey = 'sk-s*************ture';
    const expiry = Date.UTC(2030, 0, 2, 4, 34) / 1000;
    const models = ['test-model'];
    const token = { id: 21, name: 'short test key', status: 1, routing_mode: 'fixed', routing_group_id: 2,
      routing_revision: 1, expired_time: expiry, remain_quota: 2048, unlimited_quota: false, models };
    const bodies: unknown[] = [];
    let verified = false;
    let created = false;
    await page.route(/\/api\/v1\/routing-groups\/available(?:\?|$)/, route => route.fulfill({ json: {
      success: true, data: { creation_enabled: true, default_available: true, facts: {}, next_page_token: '',
        groups: [{ id: 2, key: 'test-group', display_name: '测试分组', models, sources: [], price_ratio: 1 }],
      },
    } }));
    await page.route('**/api/token', route => route.fulfill({ json: {
      success: true, data: { items: created ? [{ ...token, masked_key: maskedKey }] : [] },
    } }));
    await page.route('**/api/user/security', route => route.fulfill({ json: { enabled: true } }));
    await page.route('**/api/user/security/verify', async route => {
      expect(route.request().postDataJSON()).toEqual({ code: '123456' });
      verified = true;
      await route.fulfill({ json: {} });
    });
    await page.route('**/api/v1/routing-tokens/restricted', async route => {
      bodies.push(route.request().postDataJSON());
      if (!verified) {
        await route.fulfill({ status: 403, json: { reason: 'STEP_UP_REQUIRED' } });
        return;
      }
      created = true;
      await route.fulfill({ json: { success: true, data: { ...token, key } } });
    });

    await page.goto('/tokens');
    await page.getByRole('button', { name: '创建 Token' }).click();
    const form = page.getByRole('dialog', { name: '创建新 Token' });
    await form.getByLabel('Token 名称').fill(token.name);
    await form.getByLabel('路由分组').selectOption('2');
    await form.getByRole('checkbox', { name: '短期 Key' }).check();
    const create = form.getByRole('button', { name: '创建', exact: true });
    await expect(create).toBeDisabled();
    await form.getByLabel('到期时间（本地时间）').fill('2030-01-02T12:34');
    await form.getByLabel('配额（Token）').fill('2048');
    await form.getByRole('checkbox', { name: 'test-model' }).check();
    await create.click();

    const stepUp = page.getByRole('dialog', { name: '安全验证' });
    await stepUp.getByLabel('验证码或恢复码').fill('123456');
    await stepUp.getByRole('button', { name: '验证并继续' }).click();
    const result = page.getByRole('dialog', { name: 'Token 已创建' });
    await expect(result.getByLabel('API 密钥')).toHaveValue(key);
    await expect(result.getByText('2048', { exact: true })).toBeVisible();
    const expected = { name: token.name, routing_mode: 'fixed', routing_group_id: 2,
      restrictions: { expire_at: expiry, remain_quota: 2048, models } };
    expect(bodies).toEqual([expected, expected]);
    await result.getByRole('button', { name: '完成' }).click();
    await expect(result).toHaveCount(0);
    await expect(page.getByText(maskedKey, { exact: true })).toBeVisible();
  });
});
