import { useQueryClient, type QueryClient } from '@tanstack/react-query';
import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import { MemoryRouter } from 'react-router';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { DashboardPage } from './DashboardPage';
import { SubscriptionsPage } from './SubscriptionsPage';
import type { QuotaDimension } from '@/components/SubscriptionProgress';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';
import { apiClient } from '@/lib/api';
import { suspendProtectedQueries } from '@/lib/authorization';

const windowStart = new Date(2026, 9, 10, 10, 0, 0);
const nextRefresh = new Date(2026, 9, 11, 10, 0, 0);
const daily: QuotaDimension = {
  used: 38.46,
  settled: 38.46,
  frozen: 0,
  available: 461.54,
  limit: 500,
  remaining: 461.54,
  window_start: windowStart.getTime() / 1000,
  next_refresh: nextRefresh.getTime() / 1000,
};
let progressUserIds: Array<string | null>;

function mockProgress(dimension: QuotaDimension = daily) {
  server.use(http.get('/api/v1/subscriptions/progress', ({ request }) => {
    progressUserIds.push(new URL(request.url).searchParams.get('user_id'));
    return HttpResponse.json({
      success: true,
      data: {
        id: 7,
        status: 'active',
        starts_at: daily.window_start,
        expires_at: new Date(2026, 10, 10, 10).getTime() / 1000,
        remaining_seconds: 30 * 86400,
        daily_used: dimension,
        weekly_used: null,
        monthly_used: null,
        usage_source: 'billing',
        rate_multiplier: 1,
      },
    });
  }));
}

function renderDashboard(withSubscriptionPage = false) {
  return renderWithQuery(
    <MemoryRouter>
      <DashboardPage />
      {withSubscriptionPage && <SubscriptionsPage />}
    </MemoryRouter>,
  );
}

async function todayConsumptionCard() {
  const title = await screen.findByText('今日消耗');
  const card = title.closest('[data-slot="card"]');
  expect(card).not.toBeNull();
  return card as HTMLElement;
}

function SuspendProtectedCache({ afterSuspend }: { afterSuspend: (client: QueryClient) => void }) {
  const client = useQueryClient();
  return <button onClick={() => { suspendProtectedQueries(client); afterSuspend(client); }}>撤销缓存</button>;
}

describe('DashboardPage subscription daily consumption', () => {
  beforeEach(() => {
    window.localStorage.setItem('userId', '42');
    progressUserIds = [];
    server.use(
      http.get('/api/user/self', () => HttpResponse.json({ success: true, data: { id: 42, username: 'admin', display_name: 'admin' } })),
      http.get('/api/user/dashboard', () => HttpResponse.json({ success: true, data: { today_amount: 10249, usage: [] } })),
      http.get('/api/token', () => HttpResponse.json({ success: true, data: [] })),
      http.get('/api/user/limits', () => HttpResponse.json({ success: true, data: { user_rpm_limit: 3 } })),
    );
    mockProgress();
  });

  it('uses the same settled USD and daily window as the subscription page', async () => {
    renderDashboard(true);
    const card = await todayConsumptionCard();

    expect(await within(card).findByText('$38.46')).toBeInTheDocument();
    expect(await screen.findByText('$38.46 / $500.00')).toBeInTheDocument();
    expect(screen.getByText('可用 $461.54')).toBeInTheDocument();
    expect(card).toHaveTextContent(/订阅.*窗口/);
    expect(card).toHaveTextContent(windowStart.toLocaleString('zh-CN'));
    expect(card).toHaveTextContent(nextRefresh.toLocaleString('zh-CN'));
    expect(progressUserIds.length).toBeGreaterThan(0);
    expect(progressUserIds.every(id => id === '42')).toBe(true);
  });

  it('keeps calendar-day consumption for a user without an active subscription', async () => {
    server.use(http.get('/api/v1/subscriptions/progress', () => HttpResponse.json({ success: false, message: 'subscription not found' })));
    renderDashboard();
    const card = await todayConsumptionCard();

    expect(await within(card).findByText('$1.0249')).toBeInTheDocument();
    expect(card).toHaveTextContent('UTC 自然日');
    expect(card).not.toHaveTextContent(/订阅.*窗口/);
  });

  it('shows lifetime request counts instead of replacing them with seven-day totals', async () => {
    server.use(http.get('/api/user/dashboard', () => HttpResponse.json({ success: true, data: {
      request_count: 1200, today_amount: 0,
      usage: [{ date: '2026-10-11', count: 2, amount: 0 }],
    } })));
    renderDashboard();
    const title = await screen.findByText('调用次数');
    const card = title.closest('[data-slot="card"]') as HTMLElement;

    expect(within(card).getByText('1,200')).toBeInTheDocument();
    expect(card).toHaveTextContent('今日 2');
  });

  it.each([
    { status: 503, body: {} },
    { status: 200, body: { success: false, message: 'failed to aggregate usage' } },
  ])('does not display failed dashboard statistics as zero ($status)', async ({ status, body }) => {
    server.use(
      http.get('/api/user/dashboard', () => HttpResponse.json(body, { status })),
      http.get('/api/v1/subscriptions/progress', () => HttpResponse.json({ success: false, message: 'subscription not found' })),
    );
    renderDashboard();
    const card = await todayConsumptionCard();

    expect(within(card).getByText('—')).toBeInTheDocument();
    expect(card).toHaveTextContent('用量统计暂不可用');
    expect(within(card).queryByText('$0.0000')).not.toBeInTheDocument();
    for (const title of ['钱包余额', '已用金额', '调用次数']) {
      const metric = screen.getByText(title).closest('[data-slot="card"]') as HTMLElement;
      expect(within(metric).getByText('—')).toBeInTheDocument();
    }
  });

  it.each([503, 200])('does not display failed API key statistics as zero (%s)', async (status) => {
    server.use(http.get('/api/token', () => HttpResponse.json({ success: false, message: 'token store unavailable' }, { status })));
    renderDashboard();
    const title = await screen.findByText('API 密钥');
    const card = title.closest('[data-slot="card"]') as HTMLElement;

    expect(within(card).getByText('—')).toBeInTheDocument();
    expect(card).toHaveTextContent('密钥统计暂不可用');
    expect(within(card).queryByText('0')).not.toBeInTheDocument();
  });

  it('clears API key counts with the protected account cache', async () => {
    server.use(http.get('/api/token', () => HttpResponse.json({ success: true, data: [{ id: 1, name: 'First user key', status: 1 }] })));
    let tokensAfterSuspend: unknown = 'not suspended';
    renderWithQuery(<MemoryRouter><DashboardPage /><SuspendProtectedCache afterSuspend={(client) => { tokensAfterSuspend = client.getQueryData(['tokens']); }} /></MemoryRouter>);
    const title = await screen.findByText('API 密钥');
    const card = title.closest('[data-slot="card"]') as HTMLElement;
    expect(within(card).getByText('1')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '撤销缓存' }));

    expect(tokensAfterSuspend).toBeUndefined();
  });

  it('passes the query cancellation signal to the API key request', async () => {
    const get = vi.spyOn(apiClient, 'get');
    renderDashboard();
    await todayConsumptionCard();

    expect(get).toHaveBeenCalledWith('/token', { signal: expect.any(AbortSignal) });
  });

  it.each([
    { status: 503, body: { error: 'billing unavailable' } },
    { status: 200, body: { success: false, message: 'failed to read subscription usage' } },
  ])('shows unavailable consumption when the subscription query fails ($status)', async ({ status, body }) => {
    server.use(http.get('/api/v1/subscriptions/progress', () => HttpResponse.json(body, { status })));
    renderDashboard();
    const card = await todayConsumptionCard();

    expect(await within(card).findByText('—')).toBeInTheDocument();
    expect(card).toHaveTextContent(/订阅.*(失败|不可用)/);
    expect(within(card).queryByText('$1.0249')).not.toBeInTheDocument();
  });

  it('does not fall back to calendar-day consumption when the user query fails', async () => {
    server.use(http.get('/api/user/self', () => HttpResponse.json({}, { status: 503 })));
    renderDashboard();
    const card = await todayConsumptionCard();

    expect(await within(card).findByText('—')).toBeInTheDocument();
    expect(card).toHaveTextContent(/订阅.*(失败|不可用)/);
    expect(progressUserIds).toEqual([]);
  });

  it('preserves tiny nonzero subscription consumption', async () => {
    mockProgress({ ...daily, used: 0.000002, settled: 0.000002, available: 499.999998 });
    renderDashboard(true);
    const card = await todayConsumptionCard();

    expect(await within(card).findByText('$0.000002')).toBeInTheDocument();
    expect(screen.getByText('$0.000002 / $500.00')).toBeInTheDocument();
  });

  it('keeps a settled value of zero instead of using another amount', async () => {
    mockProgress({ ...daily, settled: 0, available: 500 });
    renderDashboard();
    const card = await todayConsumptionCard();

    expect(await within(card).findByText('$0.00')).toBeInTheDocument();
    await waitFor(() => expect(progressUserIds).toEqual(['42']));
  });

  it('shows settled consumption while explaining that frozen usage is separate', async () => {
    mockProgress({ ...daily, frozen: 2, available: 459.54 });
    renderDashboard();
    const card = await todayConsumptionCard();

    expect(await within(card).findByText('$38.46')).toBeInTheDocument();
    expect(card).toHaveTextContent('已结算');
    expect(card).toHaveTextContent('冻结');
    expect(within(card).queryByText('$40.46')).not.toBeInTheDocument();
  });
});
