import { expect, test } from '@playwright/test';
import { mockApi } from './fixtures';

for (const colorScheme of ['light', 'dark'] as const) {
  test.describe(colorScheme, () => {
    test.use({ colorScheme });
    test('safe Markdown, local fonts, and copy remain usable at this viewport', async ({ page }, testInfo) => {
      const requests: unknown[] = [];
      const imageRequests: string[] = [];
      const errors: string[] = [];
      page.on('pageerror', error => errors.push(error.message));
      page.on('request', request => { if (request.url().includes('evil.test')) imageRequests.push(request.url()); });
      await page.addInitScript(() => {
        localStorage.setItem('token', 'd7-user-fixture');
        localStorage.setItem('web:language', JSON.stringify('zh-CN'));
        localStorage.setItem('web:theme', JSON.stringify(window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'));
        document.addEventListener('securitypolicyviolation', event => {
          (window as unknown as { violations: string[] }).violations ??= [];
          (window as unknown as { violations: string[] }).violations.push(event.effectiveDirective);
        });
      });
      await page.route('**/api/**', route => route.fulfill({ json: { success: true, data: [] } }));
      await mockApi(page);
      await page.route('**/api/status', route => route.fulfill({ json: { success: true, data: { server_address: `${new URL(route.request().url()).origin}/relay` } } }));
      await page.route('**/relay/v1/models', route => route.fulfill({ json: { data: [{ id: 'd7-model' }] } }));
      const code = `const 中文 = "${'long_value_'.repeat(30)}";\n`;
      const answer = '# 安全回答\n\n**清晰中文**和 English 123.45\n\n' +
        '[文档](https://example.test) [危险](javascript:alert%281%29)\n\n' +
        '<img src="https://evil.test/track" onerror="alert(1)">\n\n' +
        '![示例](https://evil.test/track)\n\n```js\n' + code + '```\n\n' +
        '| 金额 | 用量 |\n| --- | --- |\n| $0.0001 | 1234567890 |';
      await page.route('**/relay/v1/chat/completions', route => {
        requests.push(route.request().postDataJSON());
        return route.fulfill({ json: { choices: [{ message: { content: answer }, finish_reason: 'stop' }] } });
      });
      await page.route('**/playground', async route => {
        const response = await route.fetch();
        await route.fulfill({ response, headers: { ...response.headers(), 'content-security-policy': "default-src 'self'; base-uri 'self'; object-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; font-src 'self' data:; connect-src 'self'; frame-ancestors 'none'" } });
      });
      await page.goto('/playground');
      await expect(page.locator('html')).toHaveClass(colorScheme === 'dark' ? /dark/ : /^(?!.*dark).*$/);
      await page.locator('main').getByLabel('API 密钥').fill('sk-d7-local-fixture');
      await page.getByRole('button', { name: '验证并加载模型' }).click();
      await expect(page.locator('#playground-model')).toHaveValue('d7-model');
      expect(await page.evaluate(() => performance.getEntriesByType('resource').some(entry => entry.name.includes('AssistantMarkdown')))).toBe(false);
      await page.getByLabel('输入消息').fill('**用户原文**');
      await page.getByRole('button', { name: '发送', exact: true }).click();
      const assistant = page.getByRole('article', { name: '助手消息' });
      await expect(assistant.getByRole('heading', { name: '安全回答' })).toBeVisible();
      expect(await page.evaluate(() => performance.getEntriesByType('resource').some(entry => entry.name.includes('AssistantMarkdown')))).toBe(true);
      await expect(page.getByRole('article', { name: '用户消息' })).toContainText('**用户原文**');
      await expect(assistant.locator('img, script, iframe, svg')).toHaveCount(0);
      await expect(assistant.getByRole('link', { name: '危险' })).toHaveCount(0);
      await expect(assistant.getByRole('link', { name: '文档' })).toHaveAttribute('rel', 'noopener noreferrer nofollow');
      await expect(assistant.getByRole('table')).toContainText('$0.0001');
      // Observe the real copy payload without relying on OS clipboard permission.
      await page.evaluate(() => Object.defineProperty(navigator.clipboard, 'writeText', { configurable: true, value: async (text: string) => { (window as unknown as { copied: string }).copied = text; } }));
      await assistant.getByRole('button', { name: '复制代码' }).click();
      await expect(assistant.getByRole('status')).toHaveText('代码已复制');
      expect(await page.evaluate(() => (window as unknown as { copied: string }).copied)).toBe(code);
      await page.getByRole('button', { name: '重用消息' }).click();
      await expect(page.getByLabel('输入消息')).toBeFocused();
      expect(requests).toHaveLength(1);
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual((page.viewportSize()?.width ?? 0) + 1);
      const typography = await page.evaluate(() => ({
        family: getComputedStyle(document.body).fontFamily,
        fontResources: performance.getEntriesByType('resource').filter(entry => /\.(woff2?|ttf|otf)(\?|$)/.test(entry.name)).length,
        violations: (window as unknown as { violations?: string[] }).violations ?? [],
        storedKey: [...Object.values(localStorage), ...Object.values(sessionStorage)].some(value => value.includes('sk-d7-local-fixture')),
      }));
      expect(typography.family).toContain('PingFang SC');
      expect(typography.fontResources).toBe(0);
      expect(typography.violations).toEqual([]);
      expect(typography.storedKey).toBe(false);
      expect(imageRequests).toEqual([]);
      expect(errors).toEqual([]);
      await testInfo.attach('typography', { body: JSON.stringify(typography), contentType: 'application/json' });
      await page.screenshot({ path: testInfo.outputPath('playground.png'), fullPage: true });
    });
  });
}
