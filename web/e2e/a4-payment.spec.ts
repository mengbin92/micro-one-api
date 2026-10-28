import { expect, test } from '@playwright/test';

// Provider traffic is confined to fixtures; no payment or real credentials.
test('configured Native recharge, uncertain result retry and order continuation', async ({ page }, testInfo) => {
  let enabled = true;
  let uncertain = true;
  let paid = false;
  const keys: string[] = [];
  const qr = 'weixin://wxpay/bizpayurl/up?pr=local-native-fixture';
  const order = () => ({ id: 1, trade_no: 'WX-FIXTURE', channel: 'wechat', status: paid ? 'paid' : 'pending', asset_type: 'balance', asset_amount: 2_000_000, money_cents: 2000, currency: 'CNY', pay_url: uncertain ? '' : qr });
  await page.route('**/api/**', route => route.fulfill({ json: { success: true, data: [] } }));
  await page.route('**/api/user/authorization?**', route => route.fulfill({ json: { authorization_mode: 'legacy', legacy_admin: false } }));
  await page.route('**/api/user/self', route => route.fulfill({ json: { success: true, data: { id: 7, username: 'fixture-user', role: 1 } } }));
  await page.route('**/api/user/payment/channels', route => route.fulfill({ json: { success: true, data: { channels: enabled ? [{ channel: 'wechat', currency: 'CNY', asset_types: ['balance'] }] : [] } } }));
  await page.route('**/api/user/pay', route => {
    const body = route.request().postDataJSON();
    expect(body).toEqual({ amount: 20, payment_method: 'wechat' });
    keys.push(route.request().headers()['idempotency-key']);
    return route.fulfill({ json: { success: true, data: { trade_no: 'WX-FIXTURE', pay_url: uncertain ? '' : qr, order: order() } } });
  });
  await page.route('**/api/user/payment/orders?**', route => route.fulfill({ json: { success: true, data: { orders: [order()], total: 1 } } }));
  await page.route('**/api/user/payment/orders/WX-FIXTURE', route => route.fulfill({ json: { success: true, data: { order: order() } } }));
  await page.route('**/api/v1/subscriptions/progress', route => route.fulfill({ json: { success: false, message: 'subscription not found' } }));
  await page.route('**/api/user/logs?**', route => route.fulfill({ json: { success: true, data: { logs: [] } } }));
  await page.addInitScript(() => {
    localStorage.setItem('token', 'a4-fixture-token');
    localStorage.setItem('userRole', '1');
    localStorage.setItem('web:language', JSON.stringify('zh-CN'));
  });
  await page.goto('/recharge');
  await expect(page.getByRole('button', { name: '微信支付', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: '支付宝', exact: true })).toHaveCount(0);
  await page.getByRole('button', { name: /确认支付/ }).click();
  let dialog = page.getByRole('dialog');
  await expect(dialog.getByText('下单结果暂未确认，请先刷新订单状态。再次尝试将复用本次订单。')).toBeVisible();
  await expect(dialog.getByRole('img', { name: '微信支付二维码' })).toHaveCount(0);
  await dialog.getByRole('button', { name: '关闭', exact: true }).click();
  uncertain = false;
  await page.getByRole('button', { name: /确认支付/ }).click();
  dialog = page.getByRole('dialog');
  await expect(dialog.getByRole('img', { name: '微信支付二维码' })).toHaveCount(1);
  expect(keys).toHaveLength(2);
  expect(keys[0]).toBeTruthy();
  expect(keys[1]).toBe(keys[0]);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: testInfo.outputPath('native-recharge.png'), fullPage: false, animations: 'disabled' });
  await dialog.getByRole('button', { name: '关闭', exact: true }).click();

  await page.goto('/orders');
  await page.getByRole('button', { name: /WX-FIXTURE/ }).click();
  dialog = page.getByRole('dialog');
  await expect(dialog.getByRole('img', { name: '微信支付二维码' })).toHaveCount(1);
  paid = true;
  await dialog.getByRole('button', { name: '刷新支付状态', exact: true }).click();
  await expect(dialog.getByText('订单已支付', { exact: true })).toBeVisible();
  await expect(dialog.getByRole('img', { name: '微信支付二维码' })).toHaveCount(0);
  await page.screenshot({ path: testInfo.outputPath('native-paid-order.png'), fullPage: false, animations: 'disabled' });
  enabled = false;
  await page.goto('/recharge');
  await expect(page.getByText('暂无可用支付方式', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: /确认支付/ })).toBeDisabled();
});
