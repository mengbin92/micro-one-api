import { expect, test } from '@playwright/test';

for (const fixture of [
  { name: 'A3 base', button: '填入关闭状态示例', longContext: false, timePricing: false },
  { name: 'B1 long context', button: '填入长上下文关闭示例', longContext: true, timePricing: false },
  { name: 'B1 time pricing without long context', button: '填入忙闲分时关闭示例', longContext: false, timePricing: true },
  { name: 'B1 combined time and long context', button: '填入忙闲分时关闭示例', longContext: true, timePricing: true },
]) test(`${fixture.name}: atomically saves prices and explains the frozen bill`, async ({ page }, testInfo) => {
  const model = fixture.longContext || fixture.timePricing ? 'deepseek-flash' : 'gpt-5.1';
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
    id: '71', type: 'consume', userId: '1', model_name: model, amount: '-10000',
    requestedServiceTier: 'priority', actualServiceTier: '', serviceTierStatus: 'missing',
    billedServiceTier: 'max', serviceTierPolicy: 'actual_or_max_v1', serviceTierConfigHash: 'frozen-tier-fixture',
    usageParseStatus: 'verified', uncachedInputTokens: 10, completionTokens: 2, billableTotalTokens: 12,
    requestSnapshot: JSON.stringify({ Version: 2, ServiceTier: { max_cost: 20000 } }),
  };
  await page.route('**/api/log?**', route => route.fulfill({ json: { success: true, data: { logs: [log], total: 1 } } }));
  await page.route('**/api/log/71', route => route.fulfill({ json: { success: true, data: log } }));
  await page.route('**/api/user/logs?**', route => route.fulfill({ json: { success: true, data: { logs: [{
    id: '71', type: 'consume', model_name: model, amount: -10000, created_at: 1793250000,
    requested_service_tier: 'priority', actual_service_tier: '', service_tier_status: 'missing',
    billed_service_tier: 'max', service_tier_policy: 'actual_or_max_v1', service_tier_config_hash: 'frozen-tier-fixture',
    ...(fixture.longContext ? {
      long_context_policy: 'whole_request_input_gt_v1', long_context_threshold: 128000,
      long_context_tier: 'long', long_context_input_tokens: 128001,
    } : {}),
    ...(fixture.timePricing ? {
      pricing_time_band: 'peak', pricing_timezone: 'Asia/Shanghai', pricing_time_unix: 1793235600,
    } : {}),
  }], total: 1 } } }));
  await page.addInitScript(() => {
    localStorage.setItem('token', 'a3-fixture-token');
    localStorage.setItem('userRole', '10');
    localStorage.setItem('web:language', JSON.stringify('zh-CN'));
  });
  await page.goto('/admin/pricing');
  await expect(page.getByLabel('档位价格 JSON')).toHaveValue(/"enabled": false/);
  await page.getByRole('button', { name: fixture.button, exact: true }).click();
  const example = await page.getByLabel('档位价格 JSON').inputValue();
  expect(JSON.parse(example).enabled).toBe(false);
  const fixtureConfig = JSON.parse(example);
  if (fixture.longContext || fixture.timePricing) expect(fixtureConfig).toMatchObject({ source_id: 11, model: 'deepseek-flash' });
  fixtureConfig.enabled = true;
  fixtureConfig.source_id = 71;
  const price = { input_price: 0.000001, output_price: 0.000002, cache_read_price: 0, cache_creation_5m_price: 0, cache_creation_1h_price: 0 };
  const longPrice = { ...price, input_price: 0.000002, output_price: 0.000004 };
  const tariffs = (value: typeof price) => Object.fromEntries(['default', 'priority', 'flex'].map(tier => [tier, value]));
  for (const tier of ['default', 'priority', 'flex']) {
    fixtureConfig.prices[tier] = price;
  }
  if (fixture.longContext) fixtureConfig.long_context = { policy: 'whole_request_input_gt_v1', threshold_tokens: 128000, prices: tariffs(longPrice) };
  if (fixture.timePricing) {
    fixtureConfig.time_pricing.peak_prices = tariffs(price);
    if (fixture.longContext) fixtureConfig.time_pricing.peak_long_context_prices = tariffs(longPrice);
  }
  expect(fixtureConfig.version).toBe(fixture.timePricing ? 3 : fixture.longContext ? 2 : 1);
  // Enabling exists only inside the route fixture; this suite has no owner
  // connection or upstream credentials.
  await page.getByLabel('档位价格 JSON').fill(JSON.stringify(fixtureConfig, null, 2));
  await page.getByRole('button', { name: '保存档位价格', exact: true }).click();
  await expect(page.getByText('档位价格已保存', { exact: true })).toBeVisible();
  expect(writes).toBe(1);
  await expect(page.getByLabel('档位价格 JSON')).toHaveValue(JSON.stringify(fixtureConfig, null, 2));
  expect(JSON.parse(config)).toEqual(fixtureConfig);
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
  if (fixture.longContext) {
    await expect(page.getByText('上下文计价档位：长上下文', { exact: true })).toBeVisible();
    await expect(page.getByText('实际输入总量（含缓存）：128,001', { exact: true })).toBeVisible();
    await expect(page.getByText('长上下文阈值：128,000', { exact: true })).toBeVisible();
  } else await expect(page.getByText(/上下文计价档位：/)).toHaveCount(0);
  if (fixture.timePricing) {
    await expect(page.getByText('冻结计价时段：忙时', { exact: true })).toBeVisible();
    await expect(page.getByText('计价时区：Asia/Shanghai', { exact: true })).toBeVisible();
    await expect(page.getByText('预扣计价时刻：2026/10/29 09:00:00', { exact: true })).toBeVisible();
    await expect(page.getByText('按预扣时刻冻结时段，跨时段完成仍使用冻结价格。', { exact: true })).toBeVisible();
  } else await expect(page.getByText(/冻结计价时段：/)).toHaveCount(0);
  await page.screenshot({ path: testInfo.outputPath('tier-user-bill.png'), fullPage: true });
});
