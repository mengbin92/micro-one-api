import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { OrdersPage } from './OrdersPage';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';

describe('OrdersPage', () => {
  beforeEach(() => server.use(http.get('/api/user/authorization', () => HttpResponse.json({ authorization_mode: 'legacy', legacy_admin: false }))));
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

  it('uses self order endpoints in IAM despite a forged local admin role', async () => {
    localStorage.setItem('userRole', '100');
    let adminCalls = 0;
    let selfCalls = 0;
    server.use(
      http.get('/api/user/authorization', () => HttpResponse.json({ authorization_mode: 'iam', session: { activation_state: 'active' }, permitted_operations: [] })),
      http.get('/api/payment/orders', () => { adminCalls++; return HttpResponse.json({ success: true, data: { orders: [] } }); }),
      http.get('/api/user/payment/orders', () => { selfCalls++; return HttpResponse.json({ success: true, data: { orders: [] } }); }),
      http.get('/api/user/logs', () => HttpResponse.json({ success: true, data: { logs: [] } })),
    );
    renderWithQuery(<OrdersPage />);
    await waitFor(() => expect(selfCalls).toBe(1));
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

  it.each([
    ['closed', 'https://pay.example.test/closed'],
    ['pending', ''],
    ['pending', 'mock://isolated-payment'],
  ])('never opens a payment for status=%s url=%s', async (status, pay_url) => {
    const user = userEvent.setup();
    const open = vi.spyOn(window, 'open').mockReturnValue(null);
    const order = { id: 1, trade_no: 'SAFE-ORDER', status, pay_url, money_cents: 1, asset_amount: 0, currency: 'CNY' };
    server.use(
      http.get('/api/user/payment/orders', () => HttpResponse.json({ success: true, data: { orders: [order] } })),
      http.get('/api/user/payment/orders/SAFE-ORDER', () => HttpResponse.json({ success: true, data: { order } })),
      http.get('/api/user/logs', () => HttpResponse.json({ success: true, data: { logs: [] } })),
    );
    renderWithQuery(<OrdersPage />);
    await user.click(await screen.findByRole('button', { name: '查看订单 SAFE-ORDER' }));
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText('CNY 0.01')).toBeVisible();
    expect(within(dialog).getByText('0.0000')).toBeVisible();
    if (status === 'closed') expect(within(dialog).queryByRole('button', { name: '继续支付' })).not.toBeInTheDocument();
    else if (!pay_url) expect(within(dialog).getByRole('button', { name: '继续支付' })).toBeDisabled();
    else await user.click(within(dialog).getByRole('button', { name: '继续支付' }));
    expect(open).not.toHaveBeenCalled();
  });

  it('keeps Native orders in payment filters and refreshes an uncertain order without a QR', async () => {
    const user = userEvent.setup();
    const open = vi.spyOn(window, 'open').mockReturnValue(null);
    const urls: URL[] = [];
    let paid = false;
    const order = () => ({ id: 1, trade_no: 'WECHAT-ORDER', channel: 'wechat', status: paid ? 'paid' : 'pending', money_cents: 100, currency: 'CNY' });
    server.use(
      http.get('/api/user/payment/orders', ({ request }) => {
        urls.push(new URL(request.url));
        return HttpResponse.json({ success: true, data: { orders: [order()] } });
      }),
      http.get('/api/user/payment/orders/WECHAT-ORDER', () => HttpResponse.json({ success: true, data: { order: order() } })),
      http.get('/api/user/logs', () => HttpResponse.json({ success: true, data: { logs: [] } })),
    );
    renderWithQuery(<OrdersPage />);
    await screen.findByRole('button', { name: '查看订单 WECHAT-ORDER' });
    await user.selectOptions(screen.getByRole('combobox'), 'payment');
    await waitFor(() => expect(urls).toHaveLength(2));
    expect(urls[1].searchParams.has('channel')).toBe(false);
    await user.selectOptions(screen.getByRole('combobox'), 'recharge');
    await waitFor(() => expect(urls).toHaveLength(3));
    expect(urls[2].searchParams.has('channel')).toBe(false);
    await user.click(await screen.findByRole('button', { name: '查看订单 WECHAT-ORDER' }));
    const dialog = await screen.findByRole('dialog');
    const refresh = within(dialog).getByRole('button', { name: '刷新支付状态' });
    expect(refresh).toBeEnabled();
    expect(within(dialog).getByText('下单结果暂未确认，请先刷新订单状态。再次尝试将复用本次订单。')).toBeInTheDocument();
    paid = true;
    await user.click(refresh);
    expect(await within(dialog).findByText('订单已支付')).toBeInTheDocument();
    expect(open).not.toHaveBeenCalled();
  });

  it.each(['alipay', 'wechat'])('disables %s payment actions without a trade number', async channel => {
    const user = userEvent.setup();
    const open = vi.spyOn(window, 'open').mockReturnValue(null);
    let detailCalls = 0;
    const order = { id: 1, channel, status: 'pending', pay_url: 'https://pay.example.test/order', money_cents: 100, currency: 'CNY' };
    server.use(
      http.get('/api/user/payment/orders', () => HttpResponse.json({ success: true, data: { orders: [order] } })),
      http.get('/api/user/payment/orders/:tradeNo', () => {
        detailCalls++;
        return HttpResponse.json({ success: true, data: { order } });
      }),
      http.get('/api/user/logs', () => HttpResponse.json({ success: true, data: { logs: [] } })),
    );
    renderWithQuery(<OrdersPage />);
    await user.click(await screen.findByRole('button', { name: '查看订单 -' }));
    const dialog = await screen.findByRole('dialog');
    const action = within(dialog).getByRole('button', { name: channel === 'wechat' ? '刷新支付状态' : '继续支付' });
    expect(action).toBeDisabled();
    await user.click(action);
    expect(detailCalls).toBe(0);
    expect(open).not.toHaveBeenCalled();
  });

  it('keeps payment rows when the ledger payload is malformed, and vice versa', async () => {
    const order = { id: 1, trade_no: 'PAY-RESILIENT', channel: 'alipay', status: 'paid', money_cents: 100, asset_amount: 500000, currency: 'CNY' };
    server.use(
      http.get('/api/user/payment/orders', () => HttpResponse.json({ success: true, data: { orders: [order] } })),
      // A proxy 502/error page returns HTML, not an envelope; one broken
      // section must not take the other down (allSettled only isolates
      // transport failures, not parse failures).
      http.get('/api/user/logs', () => new HttpResponse('<!doctype html><title>502</title>', { headers: { 'Content-Type': 'text/html' } })),
    );
    renderWithQuery(<OrdersPage />);
    expect(await screen.findByText('PAY-RESILIENT')).toBeVisible();

    server.use(
      http.get('/api/user/payment/orders', () => new HttpResponse('<!doctype html><title>502</title>', { headers: { 'Content-Type': 'text/html' } })),
      http.get('/api/user/logs', () =>
        HttpResponse.json({ success: true, data: { logs: [{ id: 9, type: 'redeem', remark: '兑换额度 500000', amount: 500000, balance_after: 500000, created_at: 1779200000 }] } })),
    );
    renderWithQuery(<OrdersPage />);
    expect(await screen.findByText('兑换额度 500000')).toBeVisible();
  });
});
