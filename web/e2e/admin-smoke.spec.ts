import { expect, test, type Page } from '@playwright/test';
import { mockApi } from './fixtures';

test.beforeEach(async ({ page }) => {
  await mockApi(page);
});

async function seedAdminSession(page: Page) {
  await page.goto('/login');
  await page.evaluate(() => {
    localStorage.setItem('token', 'test-user-token');
    localStorage.setItem('userRole', '10');
    localStorage.setItem('adminToken', 'test-admin-token');
  });
}

async function openMobileNavIfVisible(page: Page) {
  const openNavigation = page.getByRole('button', { name: /open navigation/i });
  if (await openNavigation.isVisible()) {
    await openNavigation.click();
  }
}

test('unauthenticated dashboard redirects to login', async ({ page }) => {
  await page.goto('/dashboard');

  await expect(page).toHaveURL(/\/login$/);
  await expect(page.getByLabel('Username')).toBeVisible();
});

test('login stores token and shows dashboard', async ({ page }) => {
  await page.goto('/login');
  await page.getByLabel('Username').fill('alice');
  await page.getByLabel('Password').fill('secret');
  await page.getByRole('button', { name: 'Sign in' }).click();

  await expect(page).toHaveURL(/\/dashboard$/);
  await expect(page.getByRole('heading', { name: /Alice/ })).toBeVisible();
  await expect(page.getByText('剩余额度')).toBeVisible();
  await expect(page.evaluate(() => localStorage.getItem('token'))).resolves.toBe('test-user-token');
});

test('register creates account and signs in', async ({ page }) => {
  const requests: string[] = [];
  await page.route('**/api/user/register', async (route) => {
    requests.push(route.request().postData() || '');
    await route.fulfill({
      json: {
        success: true,
        data: { user_id: 1 },
      },
    });
  });

  await page.goto('/register');
  await expect(page.getByText('Register with a username and password')).toBeVisible();
  await page.getByLabel('Username').fill('bob');
  await page.getByLabel('Password', { exact: true }).fill('password123');
  await page.getByLabel('Confirm password').fill('password123');
  await page.getByRole('button', { name: 'Create account' }).click();

  await expect(page).toHaveURL(/\/dashboard$/);
  await expect(page.evaluate(() => localStorage.getItem('token'))).resolves.toBe('test-user-token');
  await expect.poll(() => requests.some((body) => body.includes('"username":"bob"'))).toBe(true);
});

test('admin token enables Options nav', async ({ page }) => {
  await seedAdminSession(page);
  await page.goto('/dashboard');
  await openMobileNavIfVisible(page);

  await expect(page.getByRole('link', { name: 'Admin Overview' })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Options' })).toBeVisible();
});

test('regular user shell does not show admin login control', async ({ page }) => {
  await page.addInitScript(() => {
    localStorage.setItem('token', 'test-user-token');
    localStorage.removeItem('adminToken');
  });
  await page.goto('/dashboard');

  await expect(page.getByRole('button', { name: 'Admin' })).toBeHidden();
  await expect(page.getByRole('link', { name: 'Admin Overview' })).toBeHidden();
});

test('admin overview renders operational status', async ({ page }) => {
  await seedAdminSession(page);
  await page.goto('/admin');

  await expect(page.getByRole('heading', { name: '管理总览' })).toBeVisible();
  await expect(page.getByRole('heading', { name: '上游供应商' })).toBeVisible();
  await expect(page.getByText('openai-main')).toBeVisible();
  await expect(page.getByRole('heading', { name: '订阅账号' })).toBeVisible();
  await expect(page.getByText('claude-pro-1')).toBeVisible();
  await expect(page.getByRole('heading', { name: '最近调用与订单动态' })).toBeVisible();
});

test('admin options renders core settings', async ({ page }) => {
  await seedAdminSession(page);
  await page.goto('/admin/options');

  await expect(page.getByRole('heading', { name: 'System Options' })).toBeVisible();
  await expect(page.getByText('Core Settings')).toBeVisible();
  await expect(page.getByText('Registration enabled')).toBeVisible();
});

test('admin users sends sort and filter params', async ({ page }) => {
  const requests: string[] = [];
  await page.route('**/api/user**', async (route) => {
    if (route.request().method() === 'GET') {
      requests.push(route.request().url());
      await route.fulfill({ json: { success: true, data: [{ id: '1', username: 'alice', status: 1, group: 'default' }] } });
      return;
    }
    await route.continue();
  });

  await seedAdminSession(page);
  await page.goto('/admin/users');
  await page.getByLabel('Filter users by status').selectOption('1');
  await page.getByRole('button', { name: /sort by username/i }).click();

  await expect
    .poll(() => requests.some((url) => url.includes('status=1') && url.includes('sort=username') && url.includes('order=asc')))
    .toBe(true);
});

test('admin channels creates a channel from the web page', async ({ page }) => {
  const channelRequests: unknown[] = [];
  await page.route('**/api/channel**', async (route) => {
    if (route.request().method() === 'POST') {
      channelRequests.push(route.request().postDataJSON());
      await route.fulfill({
        json: {
          success: true,
          data: { success: true, message: 'created', channel_id: 101 },
        },
      });
      return;
    }
    await route.fulfill({
      json: {
        success: true,
        data: [
          {
            id: '101',
            name: 'openai-main',
            type: 1,
            group: 'default',
            models: 'gpt-4o-mini',
            status: 1,
            priority: '1',
            balance: 12.5,
          },
        ],
      },
    });
  });

  await seedAdminSession(page);
  await page.goto('/admin/channels');
  await page.getByRole('button', { name: 'Create Channel' }).click();
  const dialog = page.getByRole('dialog', { name: 'Create Channel' });
  await dialog.getByLabel('Name').fill('openai-main');
  await dialog.getByLabel('Base URL').fill('https://api.example.com/v1');
  await dialog.getByLabel('API Key').fill('sk-test');
  await dialog.getByLabel('Models').fill('gpt-4o-mini');
  await dialog.getByLabel('Group').fill('default');
  await dialog.getByRole('button', { name: 'Create', exact: true }).click();

  await expect.poll(() => channelRequests.length).toBe(1);
  expect(channelRequests[0]).toMatchObject({
    name: 'openai-main',
    type: 1,
    base_url: 'https://api.example.com/v1',
    key: 'sk-test',
    models: 'gpt-4o-mini',
    group: 'default',
  });
});

test('admin redemptions shows generated code values after creation', async ({ page }) => {
  await page.route('**/api/redemption**', async (route) => {
    if (route.request().method() === 'POST') {
      await route.fulfill({
        json: {
          success: true,
          data: {
            success: true,
            codes: ['redeem-a', 'redeem-b'],
          },
        },
      });
      return;
    }
    await route.fulfill({
      json: {
        success: true,
        data: [],
      },
    });
  });

  await seedAdminSession(page);
  await page.goto('/admin/redemptions');
  await page.getByRole('button', { name: 'Create Code' }).click();
  const dialog = page.getByRole('dialog', { name: 'Create Redemption Code' });
  await dialog.getByLabel('Name').fill('Campaign');
  await dialog.getByLabel('Amount').fill('10');
  await dialog.getByLabel('Count').fill('2');
  await dialog.getByRole('button', { name: 'Create', exact: true }).click();

  await expect(page.getByText('redeem-a')).toBeVisible();
  await expect(page.getByText('redeem-b')).toBeVisible();
});

test('admin payment orders sends filters to backend', async ({ page }) => {
  const requests: string[] = [];
  await page.route('**/api/payment/orders**', async (route) => {
    requests.push(route.request().url());
    await route.fulfill({
      json: {
        success: true,
        data: {
          orders: [
            {
              id: 1,
              user_id: '42',
              trade_no: 'PAY-1',
              channel: 'alipay',
              asset_amount: 500000,
              money_cents: 1000,
              currency: 'CNY',
              status: 'paid',
              provider_trade_no: 'ALI-1',
              asset_issue_status: 'issued',
              created_at: { seconds: 1779200000 },
            },
          ],
          total: 1,
        },
      },
    });
  });

  await seedAdminSession(page);
  await page.goto('/admin/payment-orders');
  await expect(page.getByRole('heading', { name: /支付订单/ })).toBeVisible();
  await page.getByLabel('Filter payment orders by status').selectOption('paid');
  await page.getByLabel('Filter payment orders by channel').selectOption('alipay');
  await page.getByLabel('Filter payment orders by user id').fill('42');

  await expect
    .poll(() => requests.some((url) => url.includes('status=paid') && url.includes('channel=alipay') && url.includes('user_id=42')))
    .toBe(true);
  await expect(page.getByText('PAY-1')).toBeVisible();
});

test('recharge page can be opened and creates alipay order', async ({ page }) => {
  const paymentRequests: unknown[] = [];
  await page.route('**/api/user/pay', async (route) => {
    paymentRequests.push(route.request().postDataJSON());
    await route.fulfill({
      json: {
        success: true,
        data: {
          trade_no: 'PAY-TEST',
          pay_url: 'mock://payment/PAY-TEST',
        },
      },
    });
  });

  await page.addInitScript(() => {
    localStorage.setItem('token', 'test-user-token');
    localStorage.removeItem('adminToken');
  });
  await page.goto('/dashboard');
  await openMobileNavIfVisible(page);
  await page.getByText('充值 / 订阅').click();

  await expect(page).toHaveURL(/\/recharge$/);
  await expect(page.getByRole('heading', { name: '快捷金额' })).toBeVisible();
  await page.getByRole('button', { name: /¥50/ }).click();
  await expect(page.getByRole('button', { name: /确认支付 ¥ 50.00/ })).toBeEnabled();
  const pagePromise = page.context().waitForEvent('page');
  await page.getByRole('button', { name: /确认支付 ¥ 50.00/ }).click();
  const paymentPage = await pagePromise;

  await expect.poll(() => paymentRequests.length).toBe(1);
  expect(paymentRequests[0]).toMatchObject({ amount: 50, payment_method: 'alipay' });
  expect(paymentPage.url()).toBe('about:blank');
  await expect(page.getByText(/测试订单已创建/)).toBeVisible();
  await paymentPage.close();
});

test('regular user can open and redeem a code', async ({ page }) => {
  const redeemRequests: unknown[] = [];
  await page.route('**/api/user/topup', async (route) => {
    redeemRequests.push(route.request().postDataJSON());
    await route.fulfill({
      json: {
        success: true,
        data: 500000,
      },
    });
  });

  await page.addInitScript(() => {
    localStorage.setItem('token', 'test-user-token');
    localStorage.setItem('userRole', '1');
    localStorage.removeItem('adminToken');
  });
  await page.goto('/dashboard');
  await openMobileNavIfVisible(page);
  await page.getByRole('link', { name: '兑换码' }).click();

  await expect(page).toHaveURL(/\/redeem$/);
  await expect(page.getByRole('heading', { name: '兑换码充值' })).toBeVisible();
  await page.getByLabel('兑换码').fill('CODE-1000');
  await page.getByRole('button', { name: '立即兑换' }).click();

  await expect.poll(() => redeemRequests.length).toBe(1);
  expect(redeemRequests[0]).toMatchObject({ key: 'CODE-1000' });
  await expect(page.getByText(/已到账/)).toBeVisible();
});

test('orders page shows admin payment orders', async ({ page }) => {
  await seedAdminSession(page);
  await page.goto('/orders');

  await expect(page.getByRole('heading', { name: '我的订单' })).toBeVisible();
  await expect(page.getByText('PAY-1')).toBeVisible();
  await expect(page.getByText('支付充值')).toBeVisible();
  await page.getByRole('button', { name: '刷新' }).first().click();
  await expect(page.getByText(/状态：已支付/)).toBeVisible();
});

test('mobile navigation exposes admin links and closes after navigation', async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== 'mobile-chrome', 'mobile-only coverage');

  await seedAdminSession(page);
  await page.goto('/dashboard');
  await page.getByRole('button', { name: /open navigation/i }).click();
  await expect(page.getByRole('link', { name: 'Options' })).toBeVisible();
  await page.getByRole('link', { name: 'Options' }).click();

  await expect(page).toHaveURL(/\/admin\/options$/);
  await expect(page.getByRole('dialog')).toBeHidden();
});

test('admin users page size persists after reload', async ({ page }) => {
  await seedAdminSession(page);
  await page.goto('/admin/users');
  await page.getByLabel('Rows per page').selectOption('50');
  await expect(page).toHaveURL(/page_size=50/);

  await page.reload();

  await expect(page.getByLabel('Rows per page')).toHaveValue('50');
});

test('admin users export sends current filters to backend export route', async ({ page }) => {
  const requests: string[] = [];
  await page.route('**/api/user/export**', async (route) => {
    requests.push(route.request().url());
    await route.fulfill({
      contentType: 'text/csv',
      body: 'id,username\n1,alice\n',
    });
  });

  await seedAdminSession(page);
  await page.goto('/admin/users');
  await page.getByLabel('Filter users by status').selectOption('1');
  await page.getByRole('button', { name: /export csv/i }).click();

  await expect.poll(() => requests.length).toBeGreaterThan(0);
  expect(requests.some((url) => url.includes('status=1') && url.includes('format=csv'))).toBe(true);
});


test('admin subscription accounts page lists and creates accounts', async ({ page }) => {
  const createRequests: unknown[] = [];
  await page.route('**/api/subscription-accounts**', async (route) => {
    const method = route.request().method();
    const pathname = new URL(route.request().url()).pathname;
    if (method === 'POST') {
      createRequests.push(route.request().postDataJSON());
      await route.fulfill({ json: { success: true, message: '', account_id: 2 } });
      return;
    }
    if (/\/api\/subscription-accounts\/?$/.test(pathname)) {
      await route.fulfill({
        json: {
          accounts: [
            {
              id: 1,
              name: 'claude-pro-1',
              platform: 'claude',
              accountType: 'oauth',
              status: 1,
              group: 'default',
              models: 'claude-sonnet-4-5',
              priority: 0,
              accountId: 'acct-123',
              expiresAt: 1800000000,
              updatedAt: 1700000000,
            },
          ],
          total: 1,
        },
      });
      return;
    }
    await route.fulfill({
      json: { success: true, message: '' },
    });
  });

  await seedAdminSession(page);
  await page.goto('/admin/subscription-accounts');

  await expect(page.getByRole('heading', { name: '订阅账号管理' })).toBeVisible();
  await expect(page.getByText('claude-pro-1')).toBeVisible();

  await page.getByRole('button', { name: /新建订阅账号/ }).click();
  const dialog = page.getByRole('dialog', { name: '新建订阅账号' });
  await dialog.getByLabel('名称').fill('codex-team');
  await dialog.getByLabel('Access Token').fill('sk-test-access');
  await dialog.getByLabel('Refresh Token').fill('rt-test-refresh');
  await dialog.getByRole('button', { name: '创建', exact: true }).click();

  await expect.poll(() => createRequests.length).toBe(1);
  expect(createRequests[0]).toMatchObject({
    name: 'codex-team',
    access_token: 'sk-test-access',
    refresh_token: 'rt-test-refresh',
  });
});
