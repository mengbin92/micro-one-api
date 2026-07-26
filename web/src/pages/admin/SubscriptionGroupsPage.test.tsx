import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { MemoryRouter } from 'react-router';
import { describe, expect, it } from 'vitest';
import { AdminSubscriptionGroupsPage } from './SubscriptionGroupsPage';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';

const group = {
  id: 1,
  name: 'claude-pro',
  display_name: 'Claude Pro',
  platform: 'anthropic',
  subscription_type: 'standard',
  daily_limit_usd: 10,
  weekly_limit_usd: null,
  monthly_limit_usd: 100,
  rate_multiplier: 1,
  status: 1,
  price_quota: 10,
  duration_days: 30,
  created_at: 1700000000,
  updated_at: 1700000000,
};

describe('AdminSubscriptionGroupsPage', () => {
  it('lists subscription groups and renders limits (null => 不限)', async () => {
    server.use(
      http.get('/api/v1/admin/subscription-groups', () =>
        HttpResponse.json({ success: true, data: [group] }),
      ),
    );

    renderWithQuery(
      <MemoryRouter>
        <AdminSubscriptionGroupsPage />
      </MemoryRouter>,
    );

    const cell = await screen.findByText('Claude Pro');
    const row = cell.closest('tr');
    expect(row?.textContent).toContain('claude-pro');
    expect(row?.textContent).toContain('Anthropic / Claude');
    expect(row?.textContent).toContain('$10'); // daily limit
    expect(row?.textContent).toContain('不限'); // weekly limit is null
    expect(row?.textContent).toContain('$10.00 / 30天');
  });

  it('creates a group, sending null for empty limits', async () => {
    const captured = { body: null as Record<string, unknown> | null };
    server.use(
      http.get('/api/v1/admin/subscription-groups', () =>
        HttpResponse.json({ success: true, data: [] }),
      ),
      http.post('/api/v1/admin/subscription-groups', async ({ request }) => {
        captured.body = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json({ success: true, data: {} });
      }),
    );

    renderWithQuery(
      <MemoryRouter>
        <AdminSubscriptionGroupsPage />
      </MemoryRouter>,
    );

    await screen.findByText('暂无订阅分组');
    await userEvent.click(screen.getByRole('button', { name: /新建分组/ }));
    await userEvent.type(screen.getByLabelText('名称(唯一)'), 'team-plan');
    await userEvent.clear(screen.getByLabelText('购买价格(USD)'));
    await userEvent.type(screen.getByLabelText('购买价格(USD)'), '10');
    await userEvent.clear(screen.getByLabelText('有效期(天)'));
    await userEvent.type(screen.getByLabelText('有效期(天)'), '30');
    await userEvent.click(screen.getByRole('button', { name: /创建/ }));

    await waitFor(() => expect(captured.body).not.toBeNull());
    expect(captured.body?.name).toBe('team-plan');
    expect(captured.body?.daily_limit_usd).toBeNull();
    expect(captured.body?.monthly_limit_usd).toBeNull();
    expect(captured.body?.price_quota).toBe(10);
    expect(captured.body?.duration_days).toBe(30);
  });
});
