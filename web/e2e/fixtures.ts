import type { Page } from '@playwright/test';

export async function mockApi(page: Page) {
  await page.route('**/api/user/login', async (route) => {
    await route.fulfill({
      json: {
        success: true,
        data: 'test-user-token',
      },
    });
  });

  await page.route('**/api/user/self', async (route) => {
    await route.fulfill({
      json: {
        success: true,
        data: {
          id: 1,
          username: 'alice',
          display_name: 'Alice',
          quota: 5000000,
          used_quota: 1000000,
          role: 1,
        },
      },
    });
  });

  await page.route('**/api/user/dashboard', async (route) => {
    await route.fulfill({
      json: {
        success: true,
        data: [
          {
            date: '2026-05-20',
            count: 3,
            quota: 150000,
            prompt_tokens: 100,
            completion_tokens: 200,
          },
        ],
      },
    });
  });

  await page.route('**/api/option/', async (route) => {
    if (route.request().method() !== 'GET') {
      await route.fulfill({ json: { success: true } });
      return;
    }

    await route.fulfill({
      json: {
        success: true,
        data: [
          { key: 'RegisterEnabled', value: 'true' },
          { key: 'QuotaForNewUser', value: '500000' },
        ],
      },
    });
  });
}
