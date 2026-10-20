import { readFile } from 'node:fs/promises';
import { expect, test } from '@playwright/test';
import { mockApi } from './fixtures';
import { vendorPayload } from '../src/test/fixtures/vendor-report';

for (const language of ['zh-CN', 'en-US']) {
  for (const theme of ['light', 'dark'] as const) {
    test.describe(`${language} ${theme}`, () => {
      test.use({ colorScheme: theme });
      test('supplier evidence remains readable and exports the complete owner response', async ({ page }, testInfo) => {
        await page.route('**/api/**', route => route.fulfill({ json: { success: true, data: [] } }));
        await mockApi(page, { admin: true });
        await page.route('**/api/admin/vendor-reports?**', route => {
          expect(route.request().method()).toBe('GET');
          return route.fulfill({ json: vendorPayload });
        });
        await page.addInitScript(({ language, theme }) => {
          localStorage.setItem('token', 'vendor-evidence-test-token');
          localStorage.setItem('userRole', '10');
          localStorage.setItem('web:language', JSON.stringify(language));
          localStorage.setItem('web:theme', JSON.stringify(theme));
        }, { language, theme });
        await page.goto('/admin/vendor-reports');
        await page.getByRole('button', { name: language === 'en-US' ? 'View evidence' : '查看证据', exact: true }).click();
        const evidence = page.getByRole('region', { name: language === 'en-US' ? 'Report evidence' : '报告证据', exact: true });
        await expect(evidence).toBeVisible();
        await expect(evidence.getByText('coverage-proof', { exact: true })).toBeVisible();
        await expect(evidence.getByText('previous-report', { exact: true })).toBeVisible();
        await expect(evidence.getByText('-0.000000000000000001 USD', { exact: true }).first()).toBeVisible();
        if (language === 'en-US') expect(await evidence.textContent()).not.toMatch(/[\u3400-\u9fff]/);
        await expect(page.locator('html')).toHaveClass(theme === 'dark' ? /dark/ : /^(?!.*dark).*$/);
        expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(page.viewportSize()!.width);
        expect(await evidence.evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true);
        const downloadEvent = page.waitForEvent('download');
        await page.getByRole('button', { name: language === 'en-US' ? 'Export review evidence' : '导出复盘材料', exact: true }).click();
        const download = await downloadEvent;
        expect(JSON.parse(await readFile((await download.path())!, 'utf8'))).toEqual(vendorPayload);
        await page.screenshot({ path: testInfo.outputPath('vendor-evidence.png'), fullPage: true });
      });
    });
  }
}
