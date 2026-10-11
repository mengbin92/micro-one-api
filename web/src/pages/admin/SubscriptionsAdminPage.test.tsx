import { fireEvent, screen, waitFor } from '@testing-library/react';
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
    http.get('/api/v1/admin/subscriptions/progress', () =>
      HttpResponse.json({ success: false, message: 'subscription not found' }),
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
      http.get('/api/v1/admin/subscriptions/progress', () =>
        HttpResponse.json({ success: false, message: 'subscription not found' }),
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

  it('reads another user progress through the managed endpoint with the read permission', async () => {
    mockBaseEndpoints();
    const captured = { userId: null as string | null, selfCalls: 0 };
    server.use(
      http.get('/api/user/authorization', () => HttpResponse.json({
        authorization_mode: 'iam',
        session: { user_id: '7', session_id: 'admin-session', activation_state: 'active', revision: '1' },
        versions: { policy_revision: '1' },
        permitted_operations: ['subscription.user_subscription.list', 'subscription.user_subscription.read', 'subscription.quota_policy.list'],
        valid_until: new Date(Date.now() + 60_000).toISOString(),
      })),
      http.get('/api/v1/subscriptions/progress', () => {
        captured.selfCalls++;
        return HttpResponse.json({ success: false, message: 'foreign subscription progress denied' }, { status: 403 });
      }),
      http.get('/api/v1/admin/subscriptions/progress', ({ request }) => {
        captured.userId = new URL(request.url).searchParams.get('user_id');
        return HttpResponse.json({ success: true, data: {
          ...subscription,
          daily_used: { used: 1.5, settled: 1.5, frozen: 0.5, available: 8, limit: 10, remaining: 8 },
          weekly_used: null, monthly_used: null, remaining_seconds: 3600,
        } });
      }),
    );
    renderWithQuery(<MemoryRouter><AdminSubscriptionsPage /></MemoryRouter>);
    await screen.findByText('alice-pro');
    await userEvent.type(screen.getByPlaceholderText(/按用户 ID 筛选/), '42');
    await userEvent.click(screen.getByRole('button', { name: /筛选/ }));

    expect(await screen.findByText('用户 42 当前活跃订阅配额')).toBeVisible();
    expect(screen.getByText('可用 $8.00')).toBeVisible();
    expect(captured.userId).toBe('42');
    expect(captured.selfCalls).toBe(0);
  });

  it.each([200, 503])('shows a progress failure at HTTP %s instead of hiding it as no subscription', async (status) => {
    mockBaseEndpoints();
    server.use(http.get('/api/v1/admin/subscriptions/progress', () =>
      HttpResponse.json({ success: false, message: 'billing unavailable' }, { status }),
    ));
    renderWithQuery(<MemoryRouter><AdminSubscriptionsPage /></MemoryRouter>);
    await screen.findByText('alice-pro');
    await userEvent.type(screen.getByPlaceholderText(/按用户 ID 筛选/), '42');
    await userEvent.click(screen.getByRole('button', { name: /筛选/ }));

    expect(await screen.findByRole('alert')).toHaveTextContent('订阅用量暂不可用');
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

  it('opens a date-time picker when extending a subscription and submits Unix seconds', async () => {
    mockBaseEndpoints();
    const captured = { expiresAt: 0 };
    server.use(
      http.post('/api/v1/admin/subscriptions/3/extend', async ({ request }) => {
        const body = (await request.json()) as { expires_at: number };
        captured.expiresAt = body.expires_at;
        return HttpResponse.json({ success: true });
      }),
    );

    renderWithQuery(
      <MemoryRouter>
        <AdminSubscriptionsPage />
      </MemoryRouter>,
    );

    await screen.findByText('alice-pro');
    await userEvent.click(screen.getByRole('button', { name: /延长/ }));

    const picker = screen.getByLabelText('新的到期时间');
    expect(picker).toHaveAttribute('type', 'datetime-local');
    fireEvent.change(picker, { target: { value: '2030-01-02T12:34' } });
    await userEvent.click(screen.getByRole('button', { name: '保存到期时间' }));

    await waitFor(() => expect(captured.expiresAt).toBe(Math.floor(new Date('2030-01-02T12:34').getTime() / 1000)));
  });
});
