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

  await page.route('**/api/user/export**', async (route) => {
    await route.fulfill({
      contentType: 'text/csv',
      body: 'id,username\n1,alice\n',
    });
  });

  await page.route('**/api/user?**', async (route) => {
    if (route.request().method() !== 'GET') {
      await route.fulfill({ json: { success: true } });
      return;
    }

    await route.fulfill({
      json: {
        success: true,
        data: [
          {
            id: '1',
            username: 'alice',
            displayName: 'Alice',
            email: 'alice@example.com',
            group: 'default',
            status: 1,
            quota: '5000000',
            usedQuota: '1000000',
            createdAt: '1710000000',
          },
        ],
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

  await page.route('**/api/admin/access', async (route) => {
    await route.fulfill({
      json: {
        success: true,
        data: { admin: true },
      },
    });
  });

  await page.route('**/api/admin/summary', async (route) => {
    await route.fulfill({
      json: {
        success: true,
        data: {
          totals: {
            users: 12,
            active_users: 10,
            channels: 3,
            active_channels: 2,
            configured_models: 5,
            request_count: 42,
            quota_used: 750000,
            prompt_tokens: 300,
            completion_tokens: 500,
            channel_balance: 88.5,
            stale_balance_channels: 1,
            log_count: 4,
          },
          recent_users: [
            { id: 1, username: 'alice', display_name: 'Alice', email: 'alice@example.com', group: 'default', status: 1 },
          ],
          channels: [
            { id: 1, name: 'openai-main', type: 1, group: 'default', status: 1, models: 'gpt-4o-mini,gpt-4o', balance: 88.5 },
          ],
          recent_logs: [
            { id: 1, user_id: '1', type: 'consume', amount: -150000, model_name: 'gpt-4o-mini', endpoint: '/v1/chat/completions', created_at: 1779200000 },
            { id: 2, user_id: '1', type: 'recharge', amount: 500000, created_at: 1779200100 },
          ],
          model_catalog: [{ id: 'gpt-4o-mini', owned_by: 'openai' }],
          pricing_options: {
            ModelRatio: '{"gpt-4o-mini":0.15}',
            CompletionRatio: '{"gpt-4o-mini":1}',
            GroupRatio: '{"default":1}',
            QuotaPerUnit: '500000',
          },
          payment_summary: { recent_order_count: 1, recent_amount: 500000 },
        },
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
