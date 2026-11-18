import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { MemoryRouter } from 'react-router';
import { describe, expect, it, vi } from 'vitest';
import { AdminSubscriptionsPage } from './SubscriptionsAdminPage';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';
import { useAuthorizationLifecycle } from '@/lib/authorization';

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

function ManagedSubscriptions() {
  useAuthorizationLifecycle();
  return <AdminSubscriptionsPage />;
}

describe('AdminSubscriptionsPage', () => {
  it('assigns and revokes under IAM through the real write interceptor with colliding contract ids', async () => {
    mockBaseEndpoints();
    const writes: { action: string; body: Record<string, unknown>; revision: string | null }[] = [];
    server.use(
      http.get('/api/user/authorization', () => HttpResponse.json({
        authorization_mode: 'iam', session: { activation_state: 'active' },
        versions: { policy_revision: '1' },
        permitted_operations: ['subscription.user_subscription.list', 'subscription.quota_policy.list', 'subscription.user_subscription.assign', 'subscription.user_subscription.revoke'],
      })),
      http.get('/api/v1/admin/subscriptions', () => HttpResponse.json({ success: true, data: [
        { ...subscription, entitlement_revision: 4, contract: {
          version: 2, digest: 'digest', coverage_mode: 'selected_groups', coverage: [],
          quota_policy: { id: 7, version: '9f2c7b1ad0e4', rate_multiplier: 1, daily_limit_usd: null, weekly_limit_usd: null, monthly_limit_usd: null },
        } },
        { ...subscription, id: 7, subscription_name: 'bob-pro', entitlement_revision: 6 },
      ] })),
      http.get('/api/v1/admin/routing-groups', () => HttpResponse.json({ success: true, data: { groups: [], next_page_token: '' } })),
      http.post('/api/v1/admin/subscriptions/assign', async ({ request }) => {
        writes.push({ action: 'assign', body: await request.json() as Record<string, unknown>, revision: new URL(request.url).searchParams.get('expected_revision') });
        return HttpResponse.json({ success: true });
      }),
      http.post('/api/v1/admin/subscriptions/7/revoke', async ({ request }) => {
        writes.push({ action: 'revoke', body: await request.json() as Record<string, unknown>, revision: new URL(request.url).searchParams.get('expected_revision') });
        return HttpResponse.json({ success: true });
      }),
    );
    vi.spyOn(window, 'prompt').mockReturnValue('运营复核');
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    renderWithQuery(<MemoryRouter><ManagedSubscriptions /></MemoryRouter>);
    await screen.findByText('bob-pro');
    await userEvent.click(screen.getByRole('button', { name: '分配订阅' }));
    await userEvent.type(screen.getByLabelText('用户 ID'), '3');
    await userEvent.selectOptions(screen.getByLabelText('订阅额度策略'), '1');
    fireEvent.change(screen.getByLabelText('到期时间'), { target: { value: '2030-01-02T12:34' } });
    await userEvent.click(screen.getByRole('button', { name: '分配' }));
    await waitFor(() => expect(writes).toHaveLength(1));
    expect(writes[0]).toMatchObject({ action: 'assign', body: { user_id: 3, reason: '运营复核' }, revision: null });
    expect(writes[0].body).not.toHaveProperty('expected_revision');
    const row = screen.getByText('bob-pro').closest('tr')!;
    await userEvent.click(within(row).getByRole('button', { name: '撤销' }));
    await waitFor(() => expect(writes).toHaveLength(2));
    expect(writes[1]).toMatchObject({ action: 'revoke', body: { expected_revision: 6, reason: '运营复核' }, revision: '6' });
  });

  it('prefills expiry from the selected policy and preserves a manual override on assignment', async () => {
    mockBaseEndpoints();
    const now = Math.floor(Date.now() / 60_000) * 60_000;
    vi.spyOn(Date, 'now').mockReturnValue(now);
    const captured = { groupId: 0, expiresAt: 0 };
    server.use(
      http.get('/api/v1/admin/subscription-groups', () => HttpResponse.json({ success: true, data: [
        { id: 1, name: 'monthly', display_name: 'Monthly', duration_days: 30, daily_limit_usd: 10, weekly_limit_usd: null, monthly_limit_usd: 100 },
        { id: 2, name: 'weekly', display_name: 'Weekly', duration_days: 7 },
      ] })),
      http.get('/api/v1/admin/routing-groups', () => HttpResponse.json({ success: true, data: { groups: [], next_page_token: '' } })),
      http.post('/api/v1/admin/subscriptions/assign', async ({ request }) => {
        const body = await request.json() as { group_id: number; expires_at: number };
        captured.groupId = body.group_id;
        captured.expiresAt = body.expires_at;
        return HttpResponse.json({ success: true });
      }),
    );
    renderWithQuery(<MemoryRouter><AdminSubscriptionsPage /></MemoryRouter>);
    await screen.findByText('alice-pro');
    await userEvent.click(screen.getByRole('button', { name: '分配订阅' }));
    await userEvent.type(screen.getByLabelText('用户 ID'), '42');
    await userEvent.selectOptions(screen.getByLabelText('订阅额度策略'), '1');
    const expiry = screen.getByLabelText('到期时间') as HTMLInputElement;
    expect(new Date(expiry.value).getTime()).toBe(now + 30 * 86400_000);
    expect(screen.getByText('额度: 10 / 不限 / 100 (日/周/月 USD) · 有效期 30 天')).toBeInTheDocument();
    await userEvent.selectOptions(screen.getByLabelText('订阅额度策略'), '2');
    expect(new Date(expiry.value).getTime()).toBe(now + 7 * 86400_000);
    fireEvent.change(expiry, { target: { value: '2030-01-02T12:34' } });
    await userEvent.click(screen.getByRole('button', { name: '分配' }));
    await waitFor(() => expect(captured).toEqual({ groupId: 2, expiresAt: Math.floor(new Date('2030-01-02T12:34').getTime() / 1000) }));
  });

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
