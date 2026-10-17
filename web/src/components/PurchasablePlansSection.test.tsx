import { useQueryClient } from '@tanstack/react-query';
import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { MemoryRouter } from 'react-router';
import { toast } from 'sonner';
import { describe, expect, it, beforeEach, vi } from 'vitest';
import { PurchasablePlansSection } from '@/components/PurchasablePlansSection';
import { DashboardPage } from '@/pages/DashboardPage';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';

describe('PurchasablePlansSection', () => {
  beforeEach(() => {
    window.localStorage.setItem('userId', '42');
  });

  it.each([false, true])('preserves quota precision for contract plan %s', async (contractPlan) => {
    const limits = { daily_limit_usd: 0.000002, weekly_limit_usd: 1.0249, monthly_limit_usd: 499.999998 };
    const group = { id: 9, name: 'Precise plan', display_name: 'Precise plan', price_quota: 10, wallet_price_amount: 1000000, duration_days: 30, ...limits };
    const plan = {
      id: 8, name: 'Precise plan', price_quota: 10, wallet_price_amount: 1000000, validity_days: 30,
      contract: { digest: 'precise', coverage_mode: 'selected_groups', coverage: [], quota_policy: { id: 1, version: 'q1', rate_multiplier: 1, ...limits } },
    };
    server.use(
      http.get('/api/v1/subscriptions/progress', () => HttpResponse.json({ success: false, message: 'subscription not found' })),
      http.get('/api/v1/subscriptions/groups', () => HttpResponse.json({ success: true, data: [group] })),
      http.get('/api/v1/subscriptions/plans', () => HttpResponse.json({ success: true, data: contractPlan ? [plan] : [] })),
    );
    renderWithQuery(<MemoryRouter><PurchasablePlansSection /></MemoryRouter>);

    expect(await screen.findByText('Precise plan')).toBeInTheDocument();
    expect(screen.getByText('每日额度 $0.000002')).toBeInTheDocument();
    expect(screen.getByText('每月额度 $499.999998')).toBeInTheDocument();
    expect(screen.getByText('¥10.00')).toBeInTheDocument();
    expect(screen.getByText('钱包等值 $100.0000')).toBeInTheDocument();
  });

  it('creates a payment order when buying a subscription plan', async () => {
    const openSpy = vi.spyOn(window, 'open').mockReturnValue(null);
    const captured = {
      body: null as Record<string, unknown> | null,
      idempotencyKey: null as string | null,
    };
    server.use(
      http.get('/api/v1/subscriptions/plans', () => HttpResponse.json({ success: true, data: [] })),
      http.get('/api/v1/subscriptions/progress', () =>
        HttpResponse.json({ success: false, message: 'subscription not found' }),
      ),
      http.get('/api/v1/subscriptions/groups', () =>
        HttpResponse.json({
          success: true,
          data: [
            {
              id: 9,
              name: 'codex-pro',
              display_name: 'Codex Pro',
              platform: 'codex',
              daily_limit_usd: 10,
              weekly_limit_usd: null,
              monthly_limit_usd: 100,
              price_quota: 10,
              duration_days: 30,
            },
          ],
        }),
      ),
      http.post('/api/v1/subscriptions/purchase/payment', async ({ request }) => {
        captured.body = (await request.json()) as Record<string, unknown>;
        captured.idempotencyKey = request.headers.get('Idempotency-Key');
        return HttpResponse.json({
          success: true,
          data: {
            subscription: null,
            payment: { trade_no: 'PAY-1', pay_url: 'https://pay.example/PAY-1' },
          },
        });
      }),
    );

    renderWithQuery(
      <MemoryRouter>
        <PurchasablePlansSection />
      </MemoryRouter>,
    );

    expect(await screen.findByText('Codex Pro')).toBeInTheDocument();
    expect(screen.getByText('¥10.00')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: '购买订阅' }));
    await userEvent.click(screen.getByRole('button', { name: '确认购买' }));

    await waitFor(() => expect(captured.body).not.toBeNull());
    expect(captured.body).toMatchObject({ group_id: 9, channel: 'alipay' });
    // v0.18 P0: the purchase request carries an Idempotency-Key. This path
    // (/purchase/payment) is design §5.5 path #3 — the header is the agreed
    // protocol but is currently NOT consumed by the backend (dedupe is
    // deferred to the plan-A middleware, v0.18 follow-up); direct wallet
    // paths (purchase/change/topup) DO consume it via the billing ledger
    // unique key. The assertion guards the client-side contract only.
    expect(captured.idempotencyKey).toBeTruthy();
    expect(openSpy).toHaveBeenCalledWith('about:blank', '_blank');
    expect(openSpy).toHaveBeenCalledWith('https://pay.example/PAY-1', '_blank', 'noopener,noreferrer');
  });

  it('does not submit a wallet upgrade when the server quote is unavailable', async () => {
    let changes = 0;
    const contract = { digest: 'new', coverage_mode: 'selected_groups', coverage: [], quota_policy: { id: 1, version: 'q1', rate_multiplier: 1, daily_limit_usd: null, weekly_limit_usd: null, monthly_limit_usd: null } };
    server.use(
      http.get('/api/v1/subscriptions/progress', () => HttpResponse.json({ success: true, data: {
        id: 7, status: 'active', expires_at: Math.floor(Date.now() / 1000) + 86400,
        daily_used: null, weekly_used: null, monthly_used: null, remaining_seconds: 86400,
        contract: { ...contract, digest: 'old' },
      } })),
      http.get('/api/v1/subscriptions/groups', () => HttpResponse.json({ success: true, data: [] })),
      http.get('/api/v1/subscriptions/plans', () => HttpResponse.json({ success: true, data: [{ id: 8, name: 'New plan', price_quota: 100, wallet_price_amount: null, validity_days: 30, contract }] })),
      http.post('/api/v1/subscriptions/change', () => { changes++; return HttpResponse.json({ success: true }); }),
    );
    renderWithQuery(<MemoryRouter><PurchasablePlansSection /></MemoryRouter>);
    const change = await screen.findByRole('button', { name: '更换套餐' });
    await waitFor(() => expect(change).toBeEnabled());
    await userEvent.click(change);

    expect(screen.getByRole('button', { name: '确认购买' })).toBeDisabled();
    expect(screen.getByRole('alert')).toHaveTextContent('钱包报价暂不可用');
    expect(changes).toBe(0);
  });
});

it('buys the frozen plan and keeps unlimited snapshot limits', async () => {
  window.localStorage.setItem('userId', '42');
  vi.spyOn(window, 'open').mockReturnValue(null);
  let body: Record<string, unknown> | undefined;
  server.use(
    http.get('/api/v1/subscriptions/progress', () => HttpResponse.json({ success: false, message: 'subscription not found' })),
    http.get('/api/v1/subscriptions/groups', () => HttpResponse.json({ success: true, data: [] })),
    http.get('/api/v1/subscriptions/plans', () => HttpResponse.json({ success: true, data: [{ id: 8, name: 'Frozen plan', price_quota: 10, validity_days: 30, group: { daily_limit_usd: 999 }, contract: { digest: 'frozen', coverage_mode: 'selected_groups', coverage: [{ routing_group_id: 9, routing_group_key: 'vip', grants_access: false }], quota_policy: { id: 1, version: 'q1', rate_multiplier: 2, daily_limit_usd: null, weekly_limit_usd: null, monthly_limit_usd: null } } }] })),
    http.post('/api/v1/subscriptions/purchase/payment', async ({ request }) => { body = await request.json() as Record<string, unknown>; return HttpResponse.json({ success: true, data: { payment: { trade_no: 'P8' } } }); }),
  );
  renderWithQuery(<MemoryRouter><PurchasablePlansSection /></MemoryRouter>);
  expect(await screen.findByText('Frozen plan')).toBeInTheDocument();
  expect(screen.getByText('每日额度 不限')).toBeInTheDocument();
  expect(screen.getByText('vip（仅费用覆盖）')).toBeInTheDocument();
  await userEvent.click(screen.getByRole('button', { name: '购买订阅' }));
  await userEvent.click(screen.getByRole('button', { name: '确认购买' }));
  await waitFor(() => expect(body).toMatchObject({ plan_id: 8 }));
  expect(body).not.toHaveProperty('group_id');
});

function RefreshQuery({ queryKey = ['my-subscription-progress'] }: { queryKey?: string[] }) {
  const client = useQueryClient();
  return <button onClick={() => void client.invalidateQueries({ queryKey })}>重新查询</button>;
}

describe('subscription status before purchase', () => {
  beforeEach(() => {
    window.localStorage.setItem('userId', '42');
    server.use(
      http.get('/api/v1/subscriptions/plans', () => HttpResponse.json({ success: true, data: [] })),
      http.get('/api/v1/subscriptions/groups', () => HttpResponse.json({ success: true, data: [{
        id: 9, name: 'codex-pro', display_name: 'Codex Pro', platform: 'codex',
        daily_limit_usd: 10, weekly_limit_usd: null, monthly_limit_usd: 100, price_quota: 10, duration_days: 30,
      }] })),
    );
  });

  it.each([503, 200])('blocks purchasing when subscription status fails with %s', async (status) => {
    const openSpy = vi.spyOn(window, 'open').mockReturnValue(null);
    server.use(http.get('/api/v1/subscriptions/progress', () =>
      HttpResponse.json({ success: false, message: 'subscription service not configured' }, { status }),
    ));
    renderWithQuery(<MemoryRouter><PurchasablePlansSection /></MemoryRouter>);

    expect(await screen.findByText('订阅用量暂不可用')).toBeInTheDocument();
    const buy = await screen.findByRole('button', { name: '购买订阅' });
    expect(buy).toBeDisabled();
    await userEvent.click(buy);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(openSpy).not.toHaveBeenCalled();
  });

  it('waits for subscription status before offering purchase', async () => {
    let release!: () => void;
    const pending = new Promise<void>(resolve => { release = resolve; });
    server.use(http.get('/api/v1/subscriptions/progress', async () => {
      await pending;
      return HttpResponse.json({ success: false, message: 'subscription not found' });
    }));
    renderWithQuery(<MemoryRouter><PurchasablePlansSection /></MemoryRouter>);

    try {
      expect(await screen.findByRole('button', { name: '购买订阅' })).toBeDisabled();
    } finally {
      release();
    }
  });

  it('blocks an open purchase confirmation if a status refresh fails', async () => {
    const openSpy = vi.spyOn(window, 'open').mockReturnValue(null);
    let reads = 0;
    server.use(http.get('/api/v1/subscriptions/progress', () => {
      reads++;
      return reads === 1
        ? HttpResponse.json({ success: false, message: 'subscription not found' })
        : HttpResponse.json({ error: 'billing unavailable' }, { status: 503 });
    }));
    renderWithQuery(<MemoryRouter><PurchasablePlansSection /><RefreshQuery /></MemoryRouter>);
    const buy = await screen.findByRole('button', { name: '购买订阅' });
    const refresh = screen.getByRole('button', { name: '重新查询' });
    await waitFor(() => expect(buy).toBeEnabled());
    await userEvent.click(buy);
    expect(screen.getByRole('dialog')).toBeInTheDocument();
    fireEvent.click(refresh);

    expect(await screen.findByText('订阅用量暂不可用')).toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent('暂时无法确认当前订阅，请稍后重试。');
    const confirm = screen.getByRole('button', { name: '确认购买' });
    expect(confirm).toBeDisabled();
    await userEvent.click(confirm);
    expect(openSpy).not.toHaveBeenCalled();
  });

  it.each([503, 200])('does not offer legacy plans when the V2 catalog fails with %s', async (status) => {
    server.use(
      http.get('/api/v1/subscriptions/progress', () => HttpResponse.json({ success: false, message: 'subscription not found' })),
      http.get('/api/v1/subscriptions/plans', () => HttpResponse.json({ success: false, message: 'catalog unavailable' }, { status })),
    );
    renderWithQuery(<MemoryRouter><PurchasablePlansSection /></MemoryRouter>);

    expect(await screen.findByText('套餐目录暂不可用')).toBeInTheDocument();
    expect(screen.queryByText('Codex Pro')).not.toBeInTheDocument();
    expect(screen.queryByText('暂无可购买套餐')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '购买订阅' })).not.toBeInTheDocument();
  });

  it('requires an explicit empty V2 catalog before falling back to legacy plans', async () => {
    server.use(
      http.get('/api/v1/subscriptions/progress', () => HttpResponse.json({ success: false, message: 'subscription not found' })),
      http.get('/api/v1/subscriptions/plans', () => HttpResponse.json({ success: true })),
    );
    renderWithQuery(<MemoryRouter><PurchasablePlansSection /></MemoryRouter>);

    expect(await screen.findByText('套餐目录暂不可用')).toBeInTheDocument();
    expect(screen.queryByText('Codex Pro')).not.toBeInTheDocument();
  });

  it.each([503, 200])('shows a catalog error when the required legacy catalog fails with %s', async (status) => {
    server.use(
      http.get('/api/v1/subscriptions/progress', () => HttpResponse.json({ success: false, message: 'subscription not found' })),
      http.get('/api/v1/subscriptions/groups', () => HttpResponse.json({ success: false, message: 'catalog unavailable' }, { status })),
    );
    renderWithQuery(<MemoryRouter><PurchasablePlansSection /></MemoryRouter>);

    expect(await screen.findByText('套餐目录暂不可用')).toBeInTheDocument();
    expect(screen.queryByText('暂无可购买套餐')).not.toBeInTheDocument();
  });

  it('uses a confirmed V2 catalog even when the legacy catalog is unavailable', async () => {
    server.use(
      http.get('/api/v1/subscriptions/progress', () => HttpResponse.json({ success: false, message: 'subscription not found' })),
      http.get('/api/v1/subscriptions/plans', () => HttpResponse.json({ success: true, data: [{ id: 8, name: 'V2 plan', price_quota: 10, validity_days: 30 }] })),
      http.get('/api/v1/subscriptions/groups', () => HttpResponse.json({ error: 'catalog unavailable' }, { status: 503 })),
    );
    renderWithQuery(<MemoryRouter><PurchasablePlansSection /></MemoryRouter>);

    expect(await screen.findByText('V2 plan')).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole('button', { name: '购买订阅' })).toBeEnabled());
    expect(screen.queryByText('套餐目录暂不可用')).not.toBeInTheDocument();
  });

  it('blocks an open confirmation if the V2 catalog refresh fails', async () => {
    const openSpy = vi.spyOn(window, 'open').mockReturnValue(null);
    let reads = 0;
    server.use(
      http.get('/api/v1/subscriptions/progress', () => HttpResponse.json({ success: false, message: 'subscription not found' })),
      http.get('/api/v1/subscriptions/plans', () => {
        reads++;
        return reads === 1
          ? HttpResponse.json({ success: true, data: [] })
          : HttpResponse.json({ error: 'catalog unavailable' }, { status: 503 });
      }),
    );
    renderWithQuery(<MemoryRouter><PurchasablePlansSection /><RefreshQuery queryKey={['purchasable-subscription-plans']} /></MemoryRouter>);
    const buy = await screen.findByRole('button', { name: '购买订阅' });
    const refresh = screen.getByRole('button', { name: '重新查询' });
    await waitFor(() => expect(buy).toBeEnabled());
    await userEvent.click(buy);
    fireEvent.click(refresh);

    expect(await screen.findByText('套餐目录暂不可用')).toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent('暂时无法加载订阅套餐，请稍后重试。');
    const confirm = screen.getByRole('button', { name: '确认购买' });
    expect(confirm).toBeDisabled();
    await userEvent.click(confirm);
    expect(openSpy).not.toHaveBeenCalled();
  });

  it.each(['price change', 'missing quote', 'removal'])('rechecks an open confirmation after a successful catalog %s', async (change) => {
    const contract = { digest: 'new', coverage_mode: 'selected_groups', coverage: [], quota_policy: { id: 1, version: 'q1', rate_multiplier: 1, daily_limit_usd: null, weekly_limit_usd: null, monthly_limit_usd: null } };
    const plan = { id: 8, name: 'New plan', price_quota: 100, wallet_price_amount: 10_000_000, validity_days: 30, contract };
    let plans: (Omit<typeof plan, 'wallet_price_amount'> & { wallet_price_amount: number | null })[] = [plan];
    const submitted: Record<string, unknown>[] = [];
    let catalogReads = 0;
    server.use(
      http.get('/api/v1/subscriptions/progress', () => HttpResponse.json({ success: true, data: {
        id: 7, status: 'active', expires_at: Math.floor(Date.now() / 1000) + 86400,
        daily_used: null, weekly_used: null, monthly_used: null, remaining_seconds: 86400,
        contract: { ...contract, digest: 'old' },
      } })),
      http.get('/api/v1/subscriptions/groups', () => HttpResponse.json({ success: true, data: [] })),
      http.get('/api/v1/subscriptions/plans', () => { catalogReads++; return HttpResponse.json({ success: true, data: plans }); }),
      http.post('/api/v1/subscriptions/change', async ({ request }) => { submitted.push(await request.json() as Record<string, unknown>); return HttpResponse.json({ success: true }); }),
    );
    renderWithQuery(<MemoryRouter><PurchasablePlansSection /><RefreshQuery queryKey={['purchasable-subscription-plans']} /></MemoryRouter>);
    const buy = await screen.findByRole('button', { name: '更换套餐' });
    await waitFor(() => expect(buy).toBeEnabled());
    await userEvent.click(buy);
    const dialog = within(screen.getByRole('dialog'));
    expect(dialog.getByText('¥100.00')).toBeInTheDocument();
    plans = change === 'removal' ? [] : [{ ...plan, price_quota: 200, validity_days: 60, wallet_price_amount: change === 'missing quote' ? null : 20_000_000 }];
    fireEvent.click(screen.getByRole('button', { name: '重新查询', hidden: true }));
    await waitFor(() => expect(catalogReads).toBe(2));
    if (change === 'removal') {
      await waitFor(() => expect(dialog.queryByText('¥100.00')).not.toBeInTheDocument());
      expect(dialog.getByRole('button', { name: '确认购买' })).toBeDisabled();
    } else {
      expect(await dialog.findByText('¥200.00')).toBeInTheDocument();
      expect(dialog.getByText(/60/)).toBeInTheDocument();
      if (change === 'missing quote') {
        expect(dialog.getByRole('button', { name: '确认购买' })).toBeDisabled();
        expect(dialog.getByRole('alert')).toHaveTextContent('钱包报价暂不可用');
      } else {
        await userEvent.click(dialog.getByRole('button', { name: '确认购买' }));
        await waitFor(() => expect(submitted).toEqual([{ from_subscription_id: 7, to_plan_id: 8, plan_id: 8, channel: 'alipay' }]));
      }
    }
    if (change !== 'price change') {
      await userEvent.click(dialog.getByRole('button', { name: '确认购买' }));
      expect(submitted).toEqual([]);
    }
  });

  it('refreshes the shared dashboard balance after a successful subscription purchase', async () => {
    vi.spyOn(window, 'open').mockReturnValue(null);
    let purchased = false;
    let dashboardReads = 0;
    const now = Math.floor(Date.now() / 1000);
    server.use(
      http.get('/api/user/self', () => HttpResponse.json({ success: true, data: { id: 42, username: 'admin', display_name: 'admin' } })),
      http.get('/api/token', () => HttpResponse.json({ success: true, data: [] })),
      http.get('/api/user/dashboard', () => {
        dashboardReads++;
        return HttpResponse.json({ success: true, data: { balance: purchased ? 80000 : 100000, usage: [] } });
      }),
      http.get('/api/v1/subscriptions/progress', () => purchased
        ? HttpResponse.json({ success: true, data: {
          id: 7, status: 'active', starts_at: now, expires_at: now + 30 * 86400, remaining_seconds: 30 * 86400,
          daily_used: { used: 0, settled: 0, frozen: 0, available: 10, limit: 10, remaining: 10, window_start: now, next_refresh: now + 86400 },
          weekly_used: null, monthly_used: null, rate_multiplier: 1, usage_source: 'billing',
        } })
        : HttpResponse.json({ success: false, message: 'subscription not found' })),
      http.post('/api/v1/subscriptions/purchase/payment', () => {
        purchased = true;
        return HttpResponse.json({ success: true, data: { subscription: { id: 7 } } });
      }),
    );
    renderWithQuery(<MemoryRouter><DashboardPage /><PurchasablePlansSection /></MemoryRouter>);
    expect(await screen.findByText('$10.0000')).toBeInTheDocument();
    const buy = await screen.findByRole('button', { name: '购买订阅' });
    await waitFor(() => expect(buy).toBeEnabled());
    await userEvent.click(buy);
    await userEvent.click(screen.getByRole('button', { name: '确认购买' }));

    expect(await screen.findByText('$8.0000')).toBeInTheDocument();
    expect(dashboardReads).toBe(2);
  });
});

describe('purchase response contracts', () => {
  it.each([{ success: true }, { subscription: { id: 7 } }])('does not report a purchase as complete without an envelope payload: %j', async response => {
    window.localStorage.setItem('userId', '42');
    vi.spyOn(window, 'open').mockReturnValue(null);
    const success = vi.spyOn(toast, 'success');
    const error = vi.spyOn(toast, 'error');
    server.use(
      http.get('/api/v1/subscriptions/progress', () => HttpResponse.json({ success: false, message: 'subscription not found' })),
      http.get('/api/v1/subscriptions/plans', () => HttpResponse.json({ success: true, data: [] })),
      http.get('/api/v1/subscriptions/groups', () => HttpResponse.json({ success: true, data: [{ id: 9, name: 'Contract plan', price_quota: 10, duration_days: 30 }] })),
      http.post('/api/v1/subscriptions/purchase/payment', () => HttpResponse.json(response)),
    );
    renderWithQuery(<MemoryRouter><PurchasablePlansSection /></MemoryRouter>);
    await userEvent.click(await screen.findByRole('button', { name: '购买订阅' }));
    await userEvent.click(screen.getByRole('button', { name: '确认购买' }));
    await waitFor(() => expect(error).toHaveBeenCalled());
    expect(success).not.toHaveBeenCalled();
  });
});
