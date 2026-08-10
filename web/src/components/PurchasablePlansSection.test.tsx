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
