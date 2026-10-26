import { expect, test, type Page, type TestInfo } from '@playwright/test';

const defaultPolicy = {
  registration_enabled: true,
  registration_policy_available: true,
  email_verification: true,
  email_domain_restriction_enabled: true,
  email_domain_whitelist: ['example.com'],
  turnstile_check: false,
  turnstile_site_key: '',
  email_delivery_configured: true,
};

const csp = "default-src 'self'; base-uri 'self'; object-src 'none'; script-src 'self' https://challenges.cloudflare.com; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; font-src 'self' data:; connect-src 'self'; frame-src 'self' https://challenges.cloudflare.com; frame-ancestors 'none';";

async function prepare(page: Page, language: string, policy = defaultPolicy) {
  const pageErrors: string[] = [];
  const externalRequests: string[] = [];
  const unexpectedAPIs: string[] = [];
  const requests: { method: string; path: string }[] = [];
  page.on('pageerror', error => pageErrors.push(error.message));
  await page.addInitScript((language) => {
    localStorage.clear();
    localStorage.setItem('web:language', JSON.stringify(language));
    // An isolated browser fixture exercises the widget lifecycle without
    // fetching a real Cloudflare script or executing a remote challenge.
    const ids = new Map<string, HTMLElement>();
    (window as unknown as { turnstile: unknown }).turnstile = {
      ready: (callback: () => void) => callback(),
      render: (container: HTMLElement, options: { callback: (token: string) => void }) => {
        const button = document.createElement('button');
        button.type = 'button';
        button.textContent = 'Local security check';
        button.addEventListener('click', () => options.callback('a1-local-turnstile-fixture'));
        container.appendChild(button);
        const id = `local-widget-${ids.size}`;
        ids.set(id, button);
        return id;
      },
      remove: (id: string) => { ids.get(id)?.remove(); ids.delete(id); },
    };
  }, language);
  await page.route('**/*', async route => {
    const url = new URL(route.request().url());
    if (url.origin === 'http://127.0.0.1:4176') await route.continue();
    else { externalRequests.push(url.origin); await route.abort(); }
  });
  await page.route('**/api/**', route => {
    const request = route.request();
    const url = new URL(request.url());
    requests.push({ method: request.method(), path: url.pathname });
    if (url.pathname === '/api/status') return route.fulfill({ json: { success: true, data: policy } });
    unexpectedAPIs.push(`${request.method()} ${url.pathname}`);
    return route.fulfill({ status: 500, json: { success: false, message: 'unexpected fixture API' } });
  });
  await page.route(url => ['/login', '/register', '/forgot-password', '/reset-password'].includes(url.pathname), async route => {
    const response = await route.fetch();
    await route.fulfill({ response, headers: { ...response.headers(), 'content-security-policy': csp, 'referrer-policy': 'no-referrer' } });
  });
  return { pageErrors, externalRequests, unexpectedAPIs, requests };
}

async function evidence(page: Page, testInfo: TestInfo, state: Awaited<ReturnType<typeof prepare>>) {
  expect(state.pageErrors).toEqual([]);
  expect(state.externalRequests).toEqual([]);
  expect(state.unexpectedAPIs).toEqual([]);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual((page.viewportSize()?.width ?? 0) + 1);
  await page.screenshot({ path: testInfo.outputPath('auth.png'), fullPage: true });
  await testInfo.attach('browser-assertions', { body: JSON.stringify({ ...state, url: page.url(), viewport: page.viewportSize(), browser: page.context().browser()?.version() }, null, 2), contentType: 'application/json' });
}

for (const language of ['zh-CN', 'en-US']) {
  const english = language === 'en-US';
  const label = (zh: string, en: string) => english ? en : zh;

  test(`registration retries email delivery and submits proof with invitation (${language})`, async ({ page }, testInfo) => {
    // Invitation rewards still require email ownership even when ordinary
    // registration permits an unverified address.
    const state = await prepare(page, language, { ...defaultPolicy, email_verification: false, turnstile_check: true, turnstile_site_key: 'local-site-key' });
    let sendCount = 0;
    let registration: Record<string, unknown> | undefined;
    await page.route('**/api/verification?**', route => {
      state.requests.push({ method: route.request().method(), path: '/api/verification' });
      expect(new URL(route.request().url()).searchParams.get('email')).toBe('alice@example.com');
      sendCount++;
      return route.fulfill(sendCount === 1 ? { status: 503, json: { success: false, message: 'email service unavailable' } } : { json: { success: true } });
    });
    await page.route('**/api/user/register', route => {
      state.requests.push({ method: route.request().method(), path: '/api/user/register' });
      registration = route.request().postDataJSON();
      return route.fulfill({ json: { success: true, data: { user_id: 8 } } });
    });
    await page.goto('/register?aff=inviter-fixture');
    await expect(page.getByText(label('允许的邮箱域名：example.com', 'Allowed email domains: example.com'))).toBeVisible();
    await page.getByLabel(label('用户名', 'Username'), { exact: true }).fill('alice');
    await page.getByLabel(label('密码', 'Password'), { exact: true }).fill('a1-browser-password');
    await page.getByLabel(label('确认密码', 'Confirm password'), { exact: true }).fill('a1-browser-password');
    await page.getByLabel(label('邮箱', 'Email'), { exact: true }).fill('alice@example.com');
    await expect(page.getByLabel(label('邀请码（可选）', 'Invitation code (optional)'))).toHaveValue('inviter-fixture');
    await page.getByRole('button', { name: label('发送验证码', 'Send verification code'), exact: true }).click();
    await expect(page.locator('#auth-error')).toHaveText(label('邮件服务暂不可用，请联系管理员。', 'Email delivery is temporarily unavailable. Please contact an administrator.'));
    await expect(page.getByText(label('验证码已发送，请查收邮件。', 'Verification code sent. Please check your email.'))).toHaveCount(0);
    await page.getByRole('button', { name: label('发送验证码', 'Send verification code'), exact: true }).click();
    await expect(page.getByText(label('验证码已发送，请查收邮件。', 'Verification code sent. Please check your email.'))).toBeVisible();
    await page.getByLabel(label('邮箱验证码', 'Email verification code')).fill('a1-mail-fixture-code');
    await page.getByRole('button', { name: 'Local security check' }).click();
    await page.getByRole('checkbox').check();
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual((page.viewportSize()?.width ?? 0) + 1);
    await page.screenshot({ path: testInfo.outputPath('registration-fields.png'), fullPage: true });
    await page.getByRole('button', { name: label('注册账号', 'Register an account'), exact: true }).click();
    await expect(page).toHaveURL(/\/login$/);
    expect(sendCount).toBe(2);
    expect(registration).toEqual({ username: 'alice', password: 'a1-browser-password', email: 'alice@example.com', aff_code: 'inviter-fixture', verification_code: 'a1-mail-fixture-code', turnstile_token: 'a1-local-turnstile-fixture' });
    await expect(page.getByLabel(label('密码', 'Password'), { exact: true })).toHaveValue('');
    await expect(page.getByRole('link', { name: label('忘记密码？', 'Forgot password?') })).toHaveAttribute('href', '/forgot-password');
    await evidence(page, testInfo, state);
  });

  test(`recovery acknowledgement is neutral and fragment reset clears URL and session (${language})`, async ({ page }, testInfo) => {
    const state = await prepare(page, language);
    let requestedEmail = '';
    let reset: Record<string, unknown> | undefined;
    await page.route('**/api/reset_password?**', route => {
      state.requests.push({ method: route.request().method(), path: '/api/reset_password' });
      requestedEmail = new URL(route.request().url()).searchParams.get('email') ?? '';
      return route.fulfill({ json: { success: true, message: 'reset token sent' } });
    });
    await page.route('**/api/user/reset', route => {
      state.requests.push({ method: route.request().method(), path: '/api/user/reset' });
      reset = route.request().postDataJSON();
      return route.fulfill({ json: { success: true, message: 'password reset' } });
    });
    await page.goto('/login');
    await page.getByRole('link', { name: label('忘记密码？', 'Forgot password?') }).click();
    await page.getByLabel(label('邮箱', 'Email'), { exact: true }).fill('unknown@example.com');
    await page.getByRole('button', { name: label('发送重置邮件', 'Send reset email') }).click();
    await expect(page.getByRole('status')).toHaveText(label('如果该邮箱关联了账号，您将收到密码重置邮件。', 'If this email is associated with an account, you will receive a password reset email.'));
    expect(requestedEmail).toBe('unknown@example.com');
    await expect(page.getByText('reset token sent')).toHaveCount(0);
    await page.screenshot({ path: testInfo.outputPath('forgot-password.png'), fullPage: true });
    await page.goto('/reset-password?email=alice%40example.com#token=a1-reset-fixture');
    await expect(page).toHaveURL(/\/reset-password$/);
    await expect(page.getByLabel(label('邮箱', 'Email'), { exact: true })).toHaveValue('alice@example.com');
    await expect(page.getByLabel(label('重置令牌', 'Reset token'))).toHaveValue('a1-reset-fixture');
    await page.evaluate(() => { localStorage.setItem('token', 'a1-old-browser-session'); localStorage.setItem('userId', '7'); });
    await page.getByLabel(label('新密码', 'New password'), { exact: true }).fill('a1-new-browser-password');
    await page.getByLabel(label('确认新密码', 'Confirm new password'), { exact: true }).fill('a1-new-browser-password');
    // Locale changes remount the router. The recovery flow must keep its
    // scrubbed token and form draft only in memory across those remounts.
    await page.getByRole('button', { name: english ? 'Switch to Chinese' : '切换至英文' }).click();
    await expect(page.locator('html')).toHaveAttribute('lang', english ? 'zh-CN' : 'en-US');
    await expect(page.locator('#reset-token')).toHaveValue('a1-reset-fixture');
    await expect(page.locator('#recovery-email')).toHaveValue('alice@example.com');
    await expect(page.locator('#new-password')).toHaveValue('a1-new-browser-password');
    await page.getByRole('button', { name: english ? '切换至英文' : 'Switch to Chinese' }).click();
    await expect(page.locator('html')).toHaveAttribute('lang', language);
    await expect(page.locator('#reset-token')).toHaveValue('a1-reset-fixture');
    await expect(page.locator('#confirm-new-password')).toHaveValue('a1-new-browser-password');
    await expect(page).toHaveURL(/\/reset-password$/);
    expect(await page.evaluate(() => [...Object.values(localStorage), ...Object.values(sessionStorage)].some(value => value.includes('a1-reset-fixture') || value.includes('a1-new-browser-password')))).toBe(false);
    await page.getByRole('button', { name: label('保存新密码', 'Save new password') }).click();
    await expect(page.getByRole('status')).toHaveText(label('密码已重置，原有会话已失效，请重新登录。', 'Password reset. Previous sessions have expired. Please sign in again.'));
    expect(reset).toEqual({ email: 'alice@example.com', token: 'a1-reset-fixture', password: 'a1-new-browser-password' });
    expect(await page.evaluate(() => [localStorage.getItem('token'), localStorage.getItem('userId')])).toEqual([null, null]);
    await expect(page.getByLabel(label('重置令牌', 'Reset token'))).toHaveCount(0);
    await evidence(page, testInfo, state);
  });

  test(`unavailable policy and unconfigured delivery fail closed (${language})`, async ({ page }, testInfo) => {
    const state = await prepare(page, language);
    await page.route('**/api/status', route => route.fulfill({ json: { success: true, data: { ...defaultPolicy, registration_policy_available: false } } }));
    await page.goto('/register');
    await expect(page.getByRole('alert')).toHaveText(label('注册策略暂不可用，请重试。', 'Registration policy is temporarily unavailable. Please retry.'));
    await expect(page.getByRole('button', { name: label('注册账号', 'Register an account'), exact: true })).toBeDisabled();
    await page.route('**/api/status', route => route.fulfill({ json: { success: true, data: { ...defaultPolicy, email_delivery_configured: false } } }));
    await page.getByRole('button', { name: label('重试', 'Retry'), exact: true }).click();
    await expect(page.getByRole('alert')).toHaveText(label('邮件服务暂不可用，请联系管理员。', 'Email delivery is temporarily unavailable. Please contact an administrator.'));
    await expect(page.getByRole('button', { name: label('发送验证码', 'Send verification code'), exact: true })).toBeDisabled();
    await expect(page.getByRole('button', { name: label('注册账号', 'Register an account'), exact: true })).toBeDisabled();
    await page.goto('/forgot-password');
    await expect(page.getByRole('alert')).toHaveText(label('邮件服务暂不可用，请联系管理员。', 'Email delivery is temporarily unavailable. Please contact an administrator.'));
    await expect(page.getByRole('button', { name: label('发送重置邮件', 'Send reset email'), exact: true })).toBeDisabled();
    await evidence(page, testInfo, state);
  });

  test(`invitation requires verified email and can be removed when delivery is unavailable (${language})`, async ({ page }, testInfo) => {
    const policy = { ...defaultPolicy, email_verification: false, email_domain_restriction_enabled: false };
    const state = await prepare(page, language, policy);
    await page.goto('/register?aff=inviter-fixture');
    await expect(page.getByLabel(label('邮箱', 'Email'), { exact: true })).toHaveAttribute('required', '');
    await expect(page.getByLabel(label('邮箱验证码', 'Email verification code'))).toHaveAttribute('required', '');
    await page.route('**/api/status', route => route.fulfill({ json: { success: true, data: { ...policy, email_delivery_configured: false } } }));
    await page.reload();
    await expect(page.getByRole('alert')).toHaveText(label('邀请奖励需要验证邮箱；邮件服务不可用，请移除邀请码后注册。', 'Invitation rewards require a verified email. Email delivery is unavailable; remove the invitation code to register.'));
    await expect(page.getByRole('button', { name: label('注册账号', 'Register an account'), exact: true })).toBeDisabled();
    await page.getByLabel(label('邀请码（可选）', 'Invitation code (optional)')).clear();
    await expect(page.getByRole('button', { name: label('注册账号', 'Register an account'), exact: true })).toBeEnabled();
    await expect(page.getByLabel(label('邮箱验证码', 'Email verification code'))).toHaveCount(0);
    await expect(page.getByLabel(label('邮箱（可选）', 'Email (optional)'))).not.toHaveAttribute('required', '');
    await evidence(page, testInfo, state);
  });
}
