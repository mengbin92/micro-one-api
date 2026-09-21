import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { describe, expect, it, vi } from 'vitest';
import { OrdersPage } from './OrdersPage';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';

describe('OrdersPage', () => {
  it('ignores a stale admin token for a normal user session', async () => {
    localStorage.setItem('adminToken', 'stale');
    let adminCalls = 0;
    let userCalls = 0;
    server.use(
      http.get('/api/user/self', () => HttpResponse.json({ success: true, data: { id: 7, role: 1 } })),
      http.get('/api/payment/orders', () => { adminCalls += 1; return HttpResponse.json({ success: true, data: { orders: [] } }); }),
      http.get('/api/user/payment/orders', () => { userCalls += 1; return HttpResponse.json({ success: true, data: { orders: [] } }); }),
      http.get('/api/user/logs', () => HttpResponse.json({ success: true, data: { logs: [] } })),
    );

    renderWithQuery(<OrdersPage />);
    await screen.findByText('暂无订单记录');
    expect(userCalls).toBe(1);
    expect(adminCalls).toBe(0);
  });

  it('opens pending payment order details and continues payment', async () => {
    const user = userEvent.setup();
    const openSpy = vi.spyOn(window, 'open').mockReturnValue(null);

    server.use(
      http.get('/api/user/payment/orders', () =>
        HttpResponse.json({
          success: true,
          data: {
            orders: [
              {
                id: 1,
                trade_no: 'PAY-PENDING',
                channel: 'alipay',
                status: 'pending',
                asset_amount: 1000000,
                money_cents: 200,
                currency: 'CNY',
                asset_issue_status: 'pending',
                pay_url: 'https://pay.example.test/PAY-PENDING',
                created_at: 1760000000,
              },
            ],
          },
        }),
      ),
      http.get('/api/user/logs', () => HttpResponse.json({ success: true, data: { logs: [] } })),
      http.get('/api/user/payment/orders/PAY-PENDING', () =>
        HttpResponse.json({
          success: true,
          data: {
            order: {
              id: 1,
              trade_no: 'PAY-PENDING',
              channel: 'alipay',
              status: 'pending',
              asset_amount: 1000000,
              money_cents: 200,
              currency: 'CNY',
              asset_issue_status: 'pending',
              pay_url: 'https://pay.example.test/PAY-PENDING',
              created_at: 1760000000,
            },
          },
        }),
      ),
    );

    renderWithQuery(<OrdersPage />);

    await user.click(await screen.findByRole('button', { name: /查看订单 PAY-PENDING/i }));

    expect(await screen.findByRole('heading', { name: '订单详情' })).toBeInTheDocument();
    const dialog = screen.getByRole('dialog');
    expect(within(dialog).getByText('PAY-PENDING')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: '继续支付' }));

    await waitFor(() => {
      expect(openSpy).toHaveBeenCalledWith('https://pay.example.test/PAY-PENDING', '_blank', 'noopener,noreferrer');
    });
  });

  it('shows paid state instead of disabled continue payment after detail refresh', async () => {
    const user = userEvent.setup();
    const openSpy = vi.spyOn(window, 'open').mockReturnValue(null);

    server.use(
      http.get('/api/user/payment/orders', () =>
        HttpResponse.json({
          success: true,
          data: {
            orders: [
              {
                id: 1,
                trade_no: 'PAY-PAID',
                channel: 'alipay',
                status: 'pending',
                asset_amount: 1000000,
                money_cents: 200,
                currency: 'CNY',
                asset_issue_status: 'pending',
                pay_url: 'https://pay.example.test/PAY-PAID',
                created_at: 1760000000,
              },
            ],
          },
        }),
      ),
      http.get('/api/user/logs', () => HttpResponse.json({ success: true, data: { logs: [] } })),
      http.get('/api/user/payment/orders/PAY-PAID', () =>
        HttpResponse.json({
          success: true,
          data: {
            order: {
              id: 1,
              trade_no: 'PAY-PAID',
              channel: 'alipay',
              status: 'paid',
              asset_amount: 1000000,
              money_cents: 200,
              currency: 'CNY',
              asset_issue_status: 'issued',
              pay_url: 'https://pay.example.test/PAY-PAID',
              created_at: 1760000000,
            },
          },
        }),
      ),
    );

    renderWithQuery(<OrdersPage />);

    await user.click(await screen.findByRole('button', { name: /查看订单 PAY-PAID/i }));

    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText('已支付')).toBeInTheDocument();
    expect(within(dialog).getByText('订单已支付')).toBeInTheDocument();
    expect(within(dialog).queryByRole('button', { name: '继续支付' })).not.toBeInTheDocument();

    expect(openSpy).not.toHaveBeenCalled();
  });
});
