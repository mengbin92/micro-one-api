import { expect, test } from '@playwright/test';
import { mockApi } from './fixtures';

test.use({ trace: 'off', video: 'off', screenshot: 'off' });

for (const language of ['zh-CN', 'en-US'] as const) {
  const labels = language === 'zh-CN'
    ? { google: '已绑定 Google', setup: '设置双重认证', qr: '验证器绑定二维码', open: '在验证器应用中打开', cancel: '取消' }
    : { google: 'Linked to Google', setup: 'Set up two-factor authentication', qr: 'Authenticator setup QR code', open: 'Open in authenticator app', cancel: 'Cancel' };

  test(`MFA setup and linked providers work in ${language}`, async ({ page }, testInfo) => {
    if (testInfo.project.name === 'mobile-chrome') await page.setViewportSize({ width: 320, height: 720 });
    const totpSeed = 'GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ';
    const uri = `otpauth://totp/Synthetic%3Aqr-fixture?secret=${totpSeed}&issuer=Synthetic&algorithm=SHA1&digits=6&period=30`;
    const externalRequests: string[] = [];
    page.on('request', request => {
      const url = new URL(request.url());
      if (['http:', 'https:'].includes(url.protocol) && url.origin !== 'http://127.0.0.1:5173') externalRequests.push(url.origin);
    });
    await page.route('**/api/**', route => route.fulfill({ json: { success: true, data: [] } }));
    await mockApi(page);
    await page.route('**/api/user/self', route => route.fulfill({ json: { success: true, data: { id: 1, username: 'alice', display_name: 'Alice', email: 'alice@example.test', group: 'default', status: 1, role: 1, oauth_providers: ['github', 'google'] } } }));
    await page.route('**/api/user/security', route => route.fulfill({ json: { configured: true, enabled: false, password_available: true } }));
    await page.route('**/api/user/security/setup', route => route.fulfill({ json: { secret: totpSeed, otpauth_uri: uri } }));
    await page.addInitScript(language => {
      localStorage.setItem('token', 'synthetic-qr-test-session');
      localStorage.setItem('web:language', JSON.stringify(language));
      localStorage.setItem('web:theme', JSON.stringify('dark'));
    }, language);

    await page.goto('/profile?oauth_bind=success&provider=github&keep=1#anchor');
    const linkedGoogle = page.getByRole('button', { name: labels.google, exact: true });
    await expect(linkedGoogle).toBeDisabled();
    expect(await linkedGoogle.evaluate(element => element.scrollWidth <= element.clientWidth)).toBe(true);
    await expect(page).toHaveURL(/\/profile\?keep=1#anchor$/);
    const security = page.locator('#security-center');
    const qr = security.getByRole('img', { name: labels.qr });
    await expect(qr).toHaveCount(0);
    await security.getByRole('button', { name: labels.setup, exact: true }).click();
    await expect(qr).toBeVisible();
    await expect(qr.locator('path[fill="#ffffff"]')).toBeAttached();
    await expect(qr.locator('path[fill="#000000"]')).toBeAttached();
    await expect(qr.locator('..')).toHaveCSS('background-color', 'rgb(255, 255, 255)');
    await expect(security.getByRole('link', { name: labels.open })).toHaveAttribute('href', uri);
    await expect(security.locator('code')).toHaveText(totpSeed);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

    const decoded = await qr.evaluate(async svg => {
      const Barcode = (window as typeof window & {
        BarcodeDetector?: {
          new(options: { formats: string[] }): { detect(source: ImageBitmap): Promise<{ rawValue: string }[]> };
          getSupportedFormats(): Promise<string[]>;
        };
      }).BarcodeDetector;
      if (!Barcode || !(await Barcode.getSupportedFormats()).includes('qr_code')) return null;
      const image = new Image();
      image.src = `data:image/svg+xml;charset=utf-8,${encodeURIComponent(new XMLSerializer().serializeToString(svg))}`;
      await image.decode();
      const bitmap = await createImageBitmap(image);
      try { return (await new Barcode({ formats: ['qr_code'] }).detect(bitmap)).map(result => result.rawValue); }
      finally { bitmap.close(); }
    });
    if (decoded === null) {
      testInfo.annotations.push({ type: 'limitation', description: 'Native BarcodeDetector cannot decode QR codes on this browser.' });
    } else {
      expect(decoded).toEqual([uri]);
      testInfo.annotations.push({ type: 'qr-decoding', description: 'Native BarcodeDetector matched the synthetic otpauth URI.' });
    }

    await security.getByRole('button', { name: labels.cancel, exact: true }).click();
    await expect(qr).toHaveCount(0);
    await expect(security.locator('code')).toHaveCount(0);
    await expect(security.getByRole('link', { name: labels.open })).toHaveCount(0);
    expect(externalRequests).toEqual([]);
  });
}
