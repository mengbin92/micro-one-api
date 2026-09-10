import { expect, test, type Locator, type Page } from '@playwright/test';
import { mockApi } from './fixtures';

test.use({ colorScheme: 'light', reducedMotion: 'reduce' });

async function seedLayoutPage(page: Page, language = 'en-US') {
  await page.route('**/api/**', (route) => route.fulfill({ json: { success: true, data: [] } }));
  await mockApi(page);
  await page.route('**/api/user/self', (route) => route.fulfill({ json: {
    success: true, data: { id: 1, display_name: 'Long Administrator Name', role: 10 },
  } }));
  await page.addInitScript((language) => {
    localStorage.setItem('token', 'layout-test-token');
    localStorage.setItem('userRole', '10');
    localStorage.setItem('web:language', JSON.stringify(language));
  }, language);
}

async function expectContainedText(locator: Locator) {
  await expect(locator).toBeVisible();
  expect(await locator.evaluate((element) => {
    const range = document.createRange();
    range.selectNodeContents(element);
    const text = range.getBoundingClientRect();
    const box = element.getBoundingClientRect();
    return text.width <= box.width + 1 && element.scrollWidth <= element.clientWidth + 1;
  }), 'text must be fully readable without wrapping numbers or clipping').toBe(true);
}

async function expectNoPageOverflow(page: Page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual((page.viewportSize()?.width ?? 0) + 1);
}

for (const language of ['zh-CN', 'en-US']) {
  test(`dashboard keeps long amounts and quick actions readable (${language})`, async ({ page }) => {
    await seedLayoutPage(page, language);
    await page.route('**/api/user/dashboard', (route) => route.fulfill({ json: {
      success: true, data: { balance: 12345678900, used_amount: 98765432100, usage: [] },
    } }));
    for (const width of [1440, 1280, 1024, 768, 390]) {
      await page.setViewportSize({ width, height: 900 });
      await page.goto('/dashboard');
      const amount = page.locator('main').getByText('$1234567.8900', { exact: true });
      await expect(amount).toBeVisible();
      await page.evaluate(() => document.fonts.ready);
      await expectContainedText(amount);
      const lineCount = await amount.evaluate((element) => {
        const range = document.createRange();
        range.selectNodeContents(element);
        return range.getClientRects().length;
      });
      expect(lineCount, `amount must stay on one line at ${width}px`).toBe(1);
      for (const label of await page.locator('main section[aria-label] a span.font-semibold').all()) {
        await expectContainedText(label);
      }
      await expectNoPageOverflow(page);
    }
  });

  test(`populated lists keep pagination inside the mobile viewport (${language})`, async ({ page }) => {
    await seedLayoutPage(page, language);
    await page.route('**/api/channel?**', (route) => route.fulfill({ json: { success: true, data: Array.from({ length: 20 }, (_, i) => ({
      id: i + 1, name: 'production-channel-with-a-long-name', type: 1, group: 'default', models: 'gpt-4o-mini', status: 1,
    })) } }));
    await page.route('**/api/log?**', (route) => route.fulfill({ json: { success: true, data: {
      logs: [{ id: '1', userId: '1', type: 'consume', amount: '-1000000', remark: 'long diagnostic detail '.repeat(10) }], total: 100,
    } } }));
    for (const width of [320, 390, 768, 1280]) {
      await page.setViewportSize({ width, height: 844 });
      for (const path of ['/admin/channels', '/admin/logs']) {
        await page.goto(path);
        const next = page.getByRole('button', { name: language === 'en-US' ? 'Next' : '下一页', exact: true });
        await expect(next).toBeEnabled();
        await next.scrollIntoViewIfNeeded();
        await expectNoPageOverflow(page);
        await next.click();
        await expect(page).toHaveURL(/page=2/);
      }
    }
  });
}

test('overview metrics stay readable at laptop widths', async ({ page }) => {
  await seedLayoutPage(page);
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto('/admin');
  const value = page.getByText('$5000.00', { exact: true });
  await expectContainedText(value);
  await page.evaluate(() => document.fonts.ready);
  const clipped = await page.locator('main [data-slot="card"]').evaluateAll((cards) => cards.filter((card) => card.scrollWidth > card.clientWidth + 1).map((card) => card.textContent));
  expect(clipped).toEqual([]);
});

test('ranking tabs support arrow keys and expose their panel', async ({ page }) => {
  await seedLayoutPage(page);
  await page.goto('/admin');
  const tabs = page.getByRole('tab');
  await tabs.first().focus();
  await page.keyboard.press('ArrowRight');
  await expect(tabs.nth(1)).toBeFocused();
  await expect(tabs.nth(1)).toHaveAttribute('aria-selected', 'true');
  await expect(page.getByRole('tabpanel', { name: 'Model', exact: true })).toBeVisible();
  await page.keyboard.press('End');
  await expect(tabs.last()).toBeFocused();
  await page.keyboard.press('Home');
  await expect(tabs.first()).toBeFocused();
});

test('overview never reports healthy when the summary request fails', async ({ page }) => {
  await seedLayoutPage(page);
  await page.route('**/api/admin/summary', (route) => route.fulfill({ status: 500, json: { message: 'Summary unavailable' } }));
  await page.goto('/admin');
  await expect(page.locator('main').getByRole('alert')).toBeVisible();
  await expect(page.getByText(/运行正常，暂无告警|Running normally, no alerts/i)).toBeHidden();
  await page.route('**/api/admin/summary', (route) => route.fulfill({ json: { success: true, data: { totals: {} } } }));
  await page.getByRole('button', { name: 'Retry', exact: true }).click();
  await expect(page.getByText('Running normally, no alerts')).toBeVisible();
  await expect(page.locator('main').getByRole('alert')).toBeHidden();
});

test('mobile navigation keeps preferences reachable within the drawer', async ({ page }) => {
  await seedLayoutPage(page);
  await page.setViewportSize({ width: 390, height: 700 });
  await page.goto('/dashboard');
  await page.getByRole('button', { name: /打开导航|Open navigation/i }).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole('button', { name: /Switch to dark mode/i })).toBeInViewport();
  await dialog.getByRole('button', { name: /Switch to dark mode/i }).click();
  await expect(page.locator('html')).toHaveClass(/dark/);
  await dialog.getByRole('link', { name: /Redemption Codes/i }).scrollIntoViewIfNeeded();
  await expect(dialog.getByRole('button', { name: /Switch to light mode/i })).toBeInViewport();
  await dialog.getByRole('link', { name: /Redemption Codes/i }).click();
  await expect(page).toHaveURL(/redeem/);
  await expect(dialog).toBeHidden();
});

test('mobile log filters keep list and export requests aligned', async ({ page }) => {
  await seedLayoutPage(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.route('**/api/log?**', (route) => route.fulfill({ json: { success: true, data: { logs: [], total: 0 } } }));
  await page.goto('/admin/logs');
  await page.getByRole('button', { name: /Filter/i, exact: true }).click();
  const type = page.locator('select').filter({ has: page.locator('option[value="consume"]') });
  await type.selectOption('consume');
  await expect(page).toHaveURL(/type=consume/);
  const requested = page.waitForRequest((request) => new URL(request.url()).pathname === '/api/log' && new URL(request.url()).searchParams.get('user_id') === '42');
  await page.getByPlaceholder('User ID', { exact: true }).fill('42');
  expect(new URL((await requested).url()).searchParams.get('type')).toBe('consume');
  await expect(page.getByPlaceholder('Subscription Account ID', { exact: true })).toHaveCount(0);
  await page.route('**/api/log/export?**', (route) => route.fulfill({ contentType: 'text/csv', body: 'id\n1\n' }));
  const exportRequest = page.waitForRequest('**/api/log/export?**');
  const download = page.waitForEvent('download');
  await page.getByRole('button', { name: 'Export CSV' }).click();
  const exportedParams = new URL((await exportRequest).url()).searchParams;
  expect(exportedParams.get('user_id')).toBe('42');
  expect(exportedParams.get('type')).toBe('consume');
  await download;
  await page.getByRole('button', { name: 'Clear', exact: true }).click();
  await expect(page).not.toHaveURL(/user_id=|type=/);
  await expectNoPageOverflow(page);
});

test('dark overview keeps populated alerts and tables within the viewport', async ({ page }, testInfo) => {
  await seedLayoutPage(page);
  await page.addInitScript(() => localStorage.setItem('web:theme', JSON.stringify('dark')));
  await page.route('**/api/admin/summary', (route) => route.fulfill({ json: { success: true, data: {
    totals: { users: 120, channels: 8 },
    alerts: [{ message: `Upstream unavailable: ${'long-upstream-id-'.repeat(12)}`, channel_id: 1 }],
    channels: [{ id: 1, name: 'long-channel-name-'.repeat(8), type: 1, models: 'gpt-4o-mini,gpt-4o', status: 1, balance: 12345 }],
    subscription_accounts: [{ id: 1, name: 'long-account-name-'.repeat(8), platform: 'claude', group: 'default', status: 1 }],
    top_users: [{ user_id: '1', quota: 10000, count: 10 }],
  } } }));
  for (const width of [390, 1280]) {
    await page.setViewportSize({ width, height: 900 });
    await page.goto('/admin');
    const alert = page.getByText(/Upstream unavailable:/);
    await expect(alert).toBeInViewport();
    await expectContainedText(alert);
    await expect(page.locator('html')).toHaveClass(/dark/);
    await expectNoPageOverflow(page);
    await page.screenshot({ path: testInfo.outputPath(`overview-dark-${width}.png`), fullPage: true });
  }
});
