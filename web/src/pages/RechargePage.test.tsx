import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { RechargePage } from './RechargePage';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';

vi.mock('@/components/PurchasablePlansSection', () => ({ PurchasablePlansSection: () => null }));
const wechat = { channel: 'wechat', currency: 'CNY', asset_types: ['balance'] };

describe('RechargePage payment methods', () => {
  beforeEach(() => {
    server.use(http.get('/api/user/dashboard', () => HttpResponse.json({ success: true, data: { balance: 0 } })));
  });

  it.each(['empty', 'offline'])('disables payment when channels are %s', async mode => {
    server.use(http.get('/api/user/payment/channels', () => mode === 'offline'
      ? HttpResponse.json({ success: false }, { status: 503 })
      : HttpResponse.json({ success: true, data: { channels: [] } })));
    renderWithQuery(<RechargePage />);
    expect(await screen.findByText(mode === 'offline' ? '支付方式暂不可用，请稍后重试' : '暂无可用支付方式')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /确认支付/ })).toBeDisabled();
    expect(screen.queryByRole('button', { name: '支付宝' })).not.toBeInTheDocument();
  });

  it('shows only eligible configured channels and renders Native QR locally', async () => {
    const user = userEvent.setup();
    const open = vi.spyOn(window, 'open').mockReturnValue(null);
    let body: unknown;
    let idempotencyKey: string | null = null;
    let paid = false;
    let dashboardReads = 0;
    server.use(
      http.get('/api/user/dashboard', () => { dashboardReads++; return HttpResponse.json({success:true,data:{balance:paid ? 2000000 : 0}}); }),
      http.get('/api/user/payment/channels', () => HttpResponse.json({ success: true, data: { channels: [wechat,
        { channel: 'alipay', currency: 'CNY', asset_types: ['subscription'] },
      ] } })),
      http.post('/api/user/pay', async ({ request }) => {
        body = await request.json(); idempotencyKey = request.headers.get('Idempotency-Key');
        return HttpResponse.json({ success: true, data: { trade_no: 'WX-1', pay_url: 'weixin://wxpay/bizpayurl?pr=local-fixture', order: { status: 'pending' } } });
      }),
      http.get('/api/user/payment/orders/WX-1', () => HttpResponse.json({ success: true, data: { order: { status: paid ? 'paid' : 'pending' } } })),
    );
    renderWithQuery(<RechargePage />);
    await screen.findByRole('button', { name: '微信支付' });
    expect(screen.queryByRole('button', { name: '支付宝' })).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: /确认支付/ }));
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByTitle('微信支付二维码')).toBeInTheDocument();
    expect(body).toEqual({ amount: 20, payment_method: 'wechat' });
    expect(idempotencyKey).toBeTruthy();
    expect(open).not.toHaveBeenCalled();
    await waitFor(() => expect(within(dialog).getByRole('button', { name: '刷新支付状态' })).toBeEnabled());
    paid = true;
    await user.click(within(dialog).getByRole('button', { name: '刷新支付状态' }));
    expect(await within(dialog).findByText('订单已支付')).toBeInTheDocument();
    expect(within(dialog).queryByTitle('微信支付二维码')).not.toBeInTheDocument();
    await waitFor(() => expect(dashboardReads).toBeGreaterThan(1));
  });

  it('keeps the same attempt key after an uncertain create result', async () => {
    const user = userEvent.setup();
    const keys: (string | null)[] = [];
    server.use(
      http.get('/api/user/payment/channels', () => HttpResponse.json({ success: true, data: { channels: [wechat] } })),
      http.post('/api/user/pay', ({ request }) => {
        keys.push(request.headers.get('Idempotency-Key'));
        return HttpResponse.json({ success: false, message: 'uncertain' }, { status: 503 });
      }),
    );
    renderWithQuery(<RechargePage />);
    await screen.findByRole('button', { name: '微信支付' });
    const submit = screen.getByRole('button', { name: /确认支付/ });
    await user.click(submit);
    await waitFor(() => expect(keys).toHaveLength(1));
    await waitFor(() => expect(submit).toBeEnabled());
    await user.click(submit);
    await waitFor(() => expect(keys).toHaveLength(2));
    expect(keys[0]).toBeTruthy();
    expect(keys[1]).toBe(keys[0]);
  });

  it('retains the attempt when the owner returns a pending order without a QR', async () => {
    const user = userEvent.setup();
    const keys: (string | null)[] = [];
    server.use(
      http.get('/api/user/payment/channels', () => HttpResponse.json({ success: true, data: { channels: [wechat] } })),
      http.post('/api/user/pay', ({ request }) => {
        keys.push(request.headers.get('Idempotency-Key'));
        return HttpResponse.json({ success: true, data: { trade_no: 'UNCERTAIN-1', order: { status: 'pending' } } });
      }),
      http.get('/api/user/payment/orders/UNCERTAIN-1', () => HttpResponse.json({ success: true, data: { order: { status: 'pending' } } })),
    );
    renderWithQuery(<RechargePage />);
    await screen.findByRole('button', { name: '微信支付' });
    await user.click(screen.getByRole('button', { name: /确认支付/ }));
    let dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText('下单结果暂未确认，请先刷新订单状态。再次尝试将复用本次订单。')).toBeInTheDocument();
    expect(within(dialog).queryByTitle('微信支付二维码')).not.toBeInTheDocument();
    await user.click(within(dialog).getByRole('button', { name: '关闭' }));
    await user.click(screen.getByRole('button', { name: /确认支付/ }));
    dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText('UNCERTAIN-1', { exact: false })).toBeInTheDocument();
    expect(keys).toHaveLength(2);
    expect(keys[1]).toBe(keys[0]);
  });

  it('does not poll a malformed create response and retains its attempt key', async () => {
    const user = userEvent.setup();
    const keys: (string | null)[] = [];
    const lookups: string[] = [];
    server.use(
      http.get('/api/user/payment/channels', () => HttpResponse.json({ success: true, data: { channels: [wechat] } })),
      http.post('/api/user/pay', ({ request }) => { keys.push(request.headers.get('Idempotency-Key')); return HttpResponse.json({success:true,data:{order:{status:'pending'}}}); }),
      http.get('/api/user/payment/orders/:tradeNo', ({params}) => { lookups.push(String(params.tradeNo)); return HttpResponse.json({success:false}); }),
    );
    renderWithQuery(<RechargePage />);
    await screen.findByRole('button', {name:'微信支付'});
    const submit = screen.getByRole('button', {name:/确认支付/});
    await user.click(submit);
    await waitFor(() => expect(submit).toBeEnabled());
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    await user.click(submit);
    await waitFor(() => expect(keys).toHaveLength(2));
    expect(keys[1]).toBe(keys[0]);
    expect(lookups).toEqual([]);
  });

  it.each(['paid', 'closed'])('starts a new same-amount attempt after closing a %s order', async status => {
    const user = userEvent.setup();
    const keys: (string | null)[] = [];
    server.use(
      http.get('/api/user/payment/channels', () => HttpResponse.json({ success: true, data: { channels: [wechat] } })),
      http.post('/api/user/pay', ({ request }) => {
        keys.push(request.headers.get('Idempotency-Key'));
        return HttpResponse.json({ success: true, data: { trade_no: `TERMINAL-${keys.length}`, order: { status: 'pending' } } });
      }),
      http.get('/api/user/payment/orders/:tradeNo', () => HttpResponse.json({ success: true, data: { order: { status } } })),
    );
    renderWithQuery(<RechargePage />);
    await screen.findByRole('button', { name: '微信支付' });
    await user.click(screen.getByRole('button', { name: /确认支付/ }));
    const dialog = await screen.findByRole('dialog');
    await within(dialog).findByText(status === 'paid' ? '订单已支付' : '订单已关闭');
    await user.click(within(dialog).getByRole('button', { name: '关闭' }));
    await user.click(screen.getByRole('button', { name: /确认支付/ }));
    await waitFor(() => expect(keys).toHaveLength(2));
    expect(keys[0]).toBeTruthy();
    expect(keys[1]).not.toBe(keys[0]);
  });
});
