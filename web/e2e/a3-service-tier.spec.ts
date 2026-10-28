import { expect, test } from '@playwright/test';

test('atomically saves tier prices and explains an unknown actual tier', async ({ page }, testInfo) => {
  let revision = '8';
  let config = '{"enabled":false}';
  let writes = 0;
  await page.route('**/api/**', route => route.fulfill({ json: { success: true, data: [] } }));
  await page.route('**/api/user/authorization?**', route => route.fulfill({ json: { authorization_mode: 'legacy', legacy_admin: true } }));
  await page.route('**/api/user/self', route => route.fulfill({ json: { success: true, data: { id: 1, username: 'fixture-admin', role: 10 } } }));
  await page.route('**/api/admin/models?**', route => route.fulfill({ json: { models: [], total: 0 } }));
  await page.route('**/api/option/', async route => {
    if (route.request().method() === 'PUT') {
      const body = route.request().postDataJSON();
      expect(Object.keys(body.values)).toEqual(['ServiceTierPricing']);
      expect(body.expected_revisions).toEqual({ ServiceTierPricing: revision });
      config = body.values.ServiceTierPricing;
      revision = '9';
      writes++;
      return route.fulfill({ json: { success: true } });
    }
    return route.fulfill({ json: { success: true, data: [{ key: 'ServiceTierPricing', value: config, revision }, { key: 'ModelPrice', value: '{}', revision: '1' }] } });
  });
  const log = {
    id: '71', type: 'consume', userId: '1', model_name: 'gpt-5.1', amount: '-10000',
    requestedServiceTier: 'priority', actualServiceTier: '', serviceTierStatus: 'missing',
    billedServiceTier: 'max', serviceTierPolicy: 'actual_or_max_v1', serviceTierConfigHash: 'frozen-tier-fixture',
    usageParseStatus: 'verified', uncachedInputTokens: 10, completionTokens: 2, billableTotalTokens: 12,
    requestSnapshot: JSON.stringify({ Version: 2, ServiceTier: { max_cost: 20000 } }),
  };
  await page.route('**/api/log?**', route => route.fulfill({ json: { success: true, data: { logs: [log], total: 1 } } }));
  await page.route('**/api/log/71', route => route.fulfill({ json: { success: true, data: log } }));
  await page.route('**/api/user/logs?**', route => route.fulfill({ json: { success: true, data: { logs: [{
    id: '71', type: 'consume', model_name: 'gpt-5.1', amount: -10000, created_at: 1,
    requested_service_tier: 'priority', actual_service_tier: '', service_tier_status: 'missing',
    billed_service_tier: 'max', service_tier_policy: 'actual_or_max_v1', service_tier_config_hash: 'frozen-tier-fixture',
  }], total: 1 } } }));
  await page.addInitScript(() => {
    localStorage.setItem('token', 'a3-fixture-token');
    localStorage.setItem('userRole', '10');
    localStorage.setItem('web:language', JSON.stringify('zh-CN'));
  });
  await page.goto('/admin/pricing');
  await expect(page.getByLabel('档位价格 JSON')).toHaveValue(/"enabled": false/);
  await page.getByRole('button', { name: '填入关闭状态示例', exact: true }).click();
  const example = await page.getByLabel('档位价格 JSON').inputValue();
  expect(JSON.parse(example).enabled).toBe(false);
  const fixtureConfig = JSON.parse(example);
  fixtureConfig.enabled = true;
  fixtureConfig.source_id = 71;
  for (const tier of ['default', 'priority', 'flex']) {
    fixtureConfig.prices[tier] = { input_price: 0.000001, output_price: 0.000002, cache_read_price: 0 };
  }
  // Enabling exists only inside the route fixture; this suite has no owner
  // connection or upstream credentials.
  await page.getByLabel('档位价格 JSON').fill(JSON.stringify(fixtureConfig, null, 2));
  await page.getByRole('button', { name: '保存档位价格', exact: true }).click();
  await expect(page.getByText('档位价格已保存', { exact: true })).toBeVisible();
  expect(writes).toBe(1);
  await expect(page.getByRole('button', { name: '保存档位价格', exact: true })).toBeEnabled();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.getByLabel('档位价格 JSON').evaluate(element => { element.scrollTop = 0; });
  await page.evaluate(() => scrollTo(0, 0));
  await page.screenshot({ path: testInfo.outputPath('tier-pricing.png'), fullPage: true });

  await page.goto('/admin/logs');
  await page.getByRole('button', { name: '查看日志 71', exact: true }).click();
  await page.getByRole('tab', { name: '用量计费', exact: true }).click();
  await expect(page.getByText('服务档位与计价依据', { exact: true })).toBeVisible();
  await expect(page.getByText('未知', { exact: true })).toBeVisible();
  await expect(page.getByText('逐桶最高价', { exact: true })).toBeVisible();
  await expect(page.getByText('frozen-tier-fixture', { exact: true })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('tier-ledger.png'), fullPage: true });
  await page.goto('/usage');
  await page.getByText('服务档位与计价依据', { exact: true }).click();
  await expect(page.getByText('上游实际档位：未知 · missing', { exact: true })).toBeVisible();
  await expect(page.getByText('结算档位：逐桶最高价', { exact: true })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('tier-user-bill.png'), fullPage: true });
});
