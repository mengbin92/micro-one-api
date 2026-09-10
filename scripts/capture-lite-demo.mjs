// Record only a --keep environment created by test-lite-smoke.py.
// npm ci in web/ and a local Chrome installation are required.
import { execFileSync } from 'node:child_process';
import { createRequire } from 'node:module';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
const require = createRequire(new URL('../web/package.json', import.meta.url));
const { chromium, expect } = require('@playwright/test');
const project = process.env.LITE_DEMO_PROJECT;
const dir = process.env.LITE_DEMO_COMPOSE_DIR;
if (!project?.startsWith('lite-smoke-') || !dir?.includes('micro-one-api-lite-')) {
  throw new Error('Set LITE_DEMO_PROJECT and LITE_DEMO_COMPOSE_DIR to an isolated --keep smoke project');
}
const args = ['compose', '-p', project, '--env-file', `${dir}/.env`, '-f', `${dir}/docker-compose.lite.yml`, '-f', `${dir}/smoke.json`];
const compose = (...rest) => execFileSync('docker', [...args, ...rest], { encoding: 'utf8' }).trim();
const admin = 'http://' + compose('port', 'admin-api', '8000');
let env = fs.readFileSync(`${dir}/.env`, 'utf8');
env = env.replace(/^CORS_ALLOWED_ORIGINS=.*$/m, `CORS_ALLOWED_ORIGINS=${admin}`);
fs.writeFileSync(`${dir}/.env`, env);
compose('up', '-d', '--no-deps', 'relay-gateway');
const relay = 'http://' + compose('port', 'relay-gateway', '8080');
const password = compose('exec', '-T', 'identity-service', 'cat', '/data/initial-admin-password.txt');
const root = path.resolve(new URL('..', import.meta.url).pathname);
const shots = path.join(root, 'docs/assets/screenshots');
const demos = path.join(root, 'docs/assets/demos');
fs.mkdirSync(demos, { recursive: true });
const videoDir = fs.mkdtempSync(path.join(os.tmpdir(), 'lite-video-'));
const browser = await chromium.launch({ channel: 'chrome', headless: true });
const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, locale: 'zh-CN', recordVideo: { dir: videoDir, size: { width: 1440, height: 1000 } } });
// Install the mask before any credential is rendered, including SPA navigation.
await context.addInitScript(() => {
  const install = () => {
    if (!document.head || document.getElementById('demo-privacy')) return;
    const style = document.createElement('style');
    style.id = 'demo-privacy';
    style.textContent = '#created-token-key, #playground-key, #channel-key { color: transparent !important; text-shadow: none !important; caret-color: transparent !important; background: #cbd5e1 !important; } .text-emerald-600 { font-size: 0 !important; } .text-emerald-600::after { content: "已验证：••••••••"; font-size: 12px; }';
    document.head.appendChild(style);
  };
  new MutationObserver(install).observe(document, { childList: true, subtree: true });
  install();
});
const page = await context.newPage();
const demoName = `quickstart-demo-${Date.now()}`;
const pause = () => page.waitForTimeout(1800); // readable pacing for the published demonstration
const shot = async (name) => { await pause(); await page.screenshot({ path: path.join(shots, name + '.png') }); };
try {
  await page.goto(admin + '/login');
  await page.getByLabel('用户名', { exact: true }).fill('admin');
  await page.getByLabel('密码', { exact: true }).fill(password);
  await pause();
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
  await shot('user-dashboard');
  const session = await page.evaluate(() => localStorage.getItem('token'));
  const option = await fetch(admin + '/api/option', { method: 'PUT', headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + session }, body: JSON.stringify({ key: 'ServerAddress', value: relay }) });
  if (!option.ok || (await option.json()).success === false) throw new Error('Cannot configure demo Relay address');
  await page.goto(admin + '/admin/channels');
  await page.getByRole('button', { name: '创建渠道', exact: true }).click();
  await page.locator('#channel-name').fill(demoName);
  await page.locator('#channel-base-url').fill('http://mock-upstream:9999');
  await page.locator('#channel-key').fill('sk-mock-key');
  await page.getByPlaceholder('搜索模型...').fill('gpt-3.5-turbo');
  await pause();
  const checkbox = page.getByRole('checkbox').filter({ hasText: 'gpt-3.5-turbo' });
  if (await checkbox.count()) await checkbox.first().click();
  else await page.getByRole('button', { name: '添加 gpt-3.5-turbo', exact: true }).click();
  await pause();
  await page.getByRole('button', { name: '创建', exact: true }).click();
  await expect(page.getByRole('cell', { name: demoName, exact: true })).toBeVisible();
  await pause();
  await page.goto(admin + '/tokens');
  await page.getByRole('button', { name: '创建 Token', exact: true }).click();
  await page.getByLabel('Token 名称').fill(demoName);
  await pause();
  await page.getByRole('button', { name: '创建', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Token 已创建', exact: true })).toBeVisible();
  await pause();
  await page.getByRole('button', { name: '在在线调试中使用', exact: true }).click();
  await expect(page.locator('#playground-model')).toBeEnabled({ timeout: 20000 });
  await page.locator('#playground-model').selectOption('gpt-3.5-turbo');
  await page.getByLabel('流式输出').uncheck();
  await page.getByLabel('最大 Token', { exact: true }).fill('16');
  await page.getByRole('textbox', { name: '输入消息', exact: true }).fill('Hello lite');
  await pause();
  await page.getByRole('button', { name: '发送', exact: true }).click();
  await expect(page.getByText('Mock response for: Hello lite', { exact: true })).toBeVisible({ timeout: 20000 });
  await shot('lite-playground');
  for (const [route, name] of [
    ['/tokens', 'token-management'], ['/usage', 'usage-records'], ['/recharge', 'subscription-plans'],
    ['/admin/channel-health', 'channel-health'], ['/admin/cost-analysis', 'cost-analysis'], ['/admin/logs', 'billing-logs'],
  ]) {
    await page.goto(admin + route);
    await page.waitForLoadState('networkidle');
    if (route === '/tokens') {
      await page.locator('table tbody tr td:nth-child(2)').evaluateAll((cells) => {
        for (const cell of cells) cell.textContent = '••••••••';
      });
    }
    await shot(name);
  }
  await context.close();
  const video = await page.video().path();
  execFileSync('ffmpeg', ['-y', '-i', video, '-c:v', 'libx264', '-pix_fmt', 'yuv420p', '-crf', '28', '-movflags', '+faststart', path.join(demos, 'lite-quickstart.mp4')], { stdio: 'ignore' });
  console.log('PASS: live UI login, channel creation, Token handoff, models and chat; screenshots and MP4 saved');
} finally {
  await browser.close();
}
