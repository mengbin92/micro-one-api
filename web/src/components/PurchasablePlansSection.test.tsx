import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { MemoryRouter } from 'react-router';
import { describe, expect, it, beforeEach, vi } from 'vitest';
import { PurchasablePlansSection } from '@/components/PurchasablePlansSection';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';

describe('PurchasablePlansSection', () => {
  beforeEach(() => {
    window.localStorage.setItem('userId', '42');
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
        HttpResponse.json({ success: false, message: 'not found' }),
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
    expect(screen.getByText('$10.00')).toBeInTheDocument();
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
});

it('buys the frozen plan and keeps unlimited snapshot limits', async () => {
  window.localStorage.setItem('userId', '42');
  vi.spyOn(window, 'open').mockReturnValue(null);
  let body: Record<string, unknown> | undefined;
  server.use(
    http.get('/api/v1/subscriptions/progress', () => HttpResponse.json({ success: false })),
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
