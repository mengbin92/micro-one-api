import { expect, test, type Page } from '@playwright/test';
import { mockApi } from './fixtures';

test.beforeEach(async ({ page }) => {
  await mockApi(page);
});

async function seedAdminSession(page: Page) {
  await page.goto('/login');
  await page.evaluate(() => {
    localStorage.setItem('token', 'test-user-token');
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

test('admin token enables Options nav', async ({ page }) => {
  await seedAdminSession(page);
  await page.goto('/dashboard');
  await openMobileNavIfVisible(page);

  await expect(page.getByRole('link', { name: 'Admin Overview' })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Options' })).toBeVisible();
});

test('regular user shell does not show admin login control', async ({ page }) => {
  await page.goto('/login');
  await page.evaluate(() => {
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
