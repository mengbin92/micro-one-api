import { expect, test } from '@playwright/test';
import { mockApi } from './fixtures';

for (const language of ['zh-CN', 'en-US']) {
  test(`pricing shows busy and idle customer tariffs (${language})`, async ({ page }, testInfo) => {
    await page.route('**/api/**', route => route.fulfill({ json: { success: true, data: [] } }));
    await mockApi(page);
    await page.addInitScript(language => {
      localStorage.setItem('token', 'pricing-test-token');
      localStorage.setItem('userRole', '1');
      localStorage.setItem('web:language', JSON.stringify(language));
    }, language);
    const idle = { input_price: 0.2, output_price: 0.4, cache_read_price: 0.02 };
    const peak = { input_price: 0.4, output_price: 0.8, cache_read_price: 0.04 };
    await page.route('**/api/pricing', route => route.fulfill({ json: { success: true, data: {
      unit: '1M tokens', prices: [{ model: 'deepseek-flash', ...idle,
        service_tier_prices: { default: idle, priority: idle, flex: idle },
        time_pricing: { timezone: 'Asia/Shanghai', calendar_valid_through: '2026-12-31', holidays: ['2026-10-01'], peak_windows: [{ weekdays: [1, 2, 3, 4, 5], start_minute: 540, end_minute: 1080 }], peak_prices: { default: peak, priority: peak, flex: peak } },
      }, { model: 'ordinary-model', input_price: 1, output_price: 2 }],
    } } }));
    await page.goto('/pricing');
    const main = page.locator('main');
    await expect(main.getByText(language === 'zh-CN' ? '忙时' : 'Peak', { exact: true })).toBeVisible();
    await expect(main.getByText(language === 'zh-CN' ? '闲时' : 'Off-peak', { exact: true })).toBeVisible();
    await expect(main.getByText('ordinary-model', { exact: true })).toBeVisible();
    await expect(main.getByText('09:00–18:00', { exact: false })).toBeVisible();
    await expect(main.getByText('Asia/Shanghai', { exact: true })).toBeVisible();
    if (language === 'en-US') {
      await expect(main).not.toContainText(/[\u3400-\u9fff]/);
      await expect(main.getByText(/Mon, Tue, Wed, Thu, Fri/)).toBeVisible();
    }
    const outputPrice = main.getByText('$0.80 / 1M tokens');
    await outputPrice.scrollIntoViewIfNeeded();
    await expect(outputPrice).toBeInViewport();
    await page.screenshot({ path: testInfo.outputPath('pricing-time-prices.png'), fullPage: true });
    await main.locator('[data-slot="table-container"]').evaluate(element => { element.scrollLeft = 0; });
    await main.locator('summary').click();
    await expect(main.getByText('2026-10-01', { exact: true })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual((page.viewportSize()?.width ?? 0) + 1);
    await page.evaluate(() => window.scrollTo({ top: 0, behavior: 'instant' }));
    await page.screenshot({ path: testInfo.outputPath('pricing-time.png'), fullPage: true });
  });
}
