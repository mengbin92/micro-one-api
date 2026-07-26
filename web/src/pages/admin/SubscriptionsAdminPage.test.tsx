import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { MemoryRouter } from 'react-router';
import { describe, expect, it, vi } from 'vitest';
import { AdminSubscriptionsPage } from './SubscriptionsAdminPage';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';

const subscription = {
  id: 3,
  user_id: 42,
  group_id: 1,
  subscription_name: 'alice-pro',
  status: 'active',
  starts_at: 1700000000,
  expires_at: 1800000000,
  daily_usage_usd: 1.5,
  weekly_usage_usd: 4,
  monthly_usage_usd: 12,
  metadata: '',
  created_at: 1700000000,
  updated_at: 1700000000,
};

function mockBaseEndpoints() {
  server.use(
    http.get('/api/v1/admin/subscription-groups', () =>
      HttpResponse.json({ success: true, data: [{ id: 1, name: 'claude-pro', display_name: 'Claude Pro' }] }),
    ),
    http.get('/api/v1/admin/subscriptions', () =>
      HttpResponse.json({ success: true, data: [subscription] }),
    ),
    http.get('/api/v1/subscriptions/progress', () =>
      HttpResponse.json({ success: false, message: 'no active' }),
    ),
  );
}

describe('AdminSubscriptionsPage', () => {
  it('lists all subscriptions by default (no user_id required)', async () => {
    const captured = { url: null as string | null };
    server.use(
      http.get('/api/v1/admin/subscription-groups', () =>
        HttpResponse.json({ success: true, data: [{ id: 1, name: 'claude-pro', display_name: 'Claude Pro' }] }),
      ),
      http.get('/api/v1/admin/subscriptions', ({ request }) => {
        captured.url = request.url;
        return HttpResponse.json({ success: true, data: [subscription] });
      }),
    );

    renderWithQuery(
      <MemoryRouter>
        <AdminSubscriptionsPage />
      </MemoryRouter>,
    );

    const cell = await screen.findByText('alice-pro');
    expect(cell.closest('tr')?.textContent).toContain('active');
    // Default list request carries no user_id filter.
    expect(captured.url).not.toContain('user_id');
  });

  it('narrows to a single user when filtering by user id', async () => {
    const captured = { url: null as string | null };
    server.use(
      http.get('/api/v1/admin/subscription-groups', () =>
        HttpResponse.json({ success: true, data: [] }),
      ),
      http.get('/api/v1/admin/subscriptions', ({ request }) => {
        captured.url = request.url;
        return HttpResponse.json({ success: true, data: [subscription] });
      }),
      http.get('/api/v1/subscriptions/progress', () =>
        HttpResponse.json({ success: false, message: 'no active' }),
      ),
    );

    renderWithQuery(
      <MemoryRouter>
        <AdminSubscriptionsPage />
      </MemoryRouter>,
    );

    await screen.findByText('alice-pro');
    await userEvent.type(screen.getByPlaceholderText(/按用户 ID 筛选/), '42');
    await userEvent.click(screen.getByRole('button', { name: /筛选/ }));

    await waitFor(() => expect(captured.url).toContain('user_id=42'));
  });

  it('revokes a subscription via the :id/revoke endpoint', async () => {
    mockBaseEndpoints();
    const captured = { hit: false };
    server.use(
      http.post('/api/v1/admin/subscriptions/3/revoke', () => {
        captured.hit = true;
        return HttpResponse.json({ success: true });
      }),
    );
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    vi.spyOn(window, 'prompt').mockReturnValue('policy violation');

    renderWithQuery(
      <MemoryRouter>
        <AdminSubscriptionsPage />
      </MemoryRouter>,
    );

    await screen.findByText('alice-pro');
    await userEvent.click(screen.getByRole('button', { name: /撤销/ }));

    await waitFor(() => expect(captured.hit).toBe(true));
  });
});
