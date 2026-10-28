import { describe, it, expect } from 'vitest';
import { safePaymentURL, safeWechatPaymentURL } from './payment-url';
describe('payment URL', () => {
 it('allows HTTPS and rejects executable or untrusted schemes', () => {
   expect(safePaymentURL('https://pay.example/order')).toBe('https://pay.example/order');
   for (const value of ['javascript:alert(1)', 'data:text/html,test', 'http://pay.example', '//pay.example', 'https://user:pass@pay.example']) expect(safePaymentURL(value)).toBeNull();
 });
 it('accepts only Native payment QR payloads without opening their scheme', () => {
   const url = 'weixin://wxpay/bizpayurl?pr=test-123';
   expect(safeWechatPaymentURL(url)).toBe(url);
   const nativeURL = 'weixin://wxpay/bizpayurl/up?pr=test-123';
   expect(safeWechatPaymentURL(nativeURL)).toBe(nativeURL);
   expect(safePaymentURL(url)).toBeNull();
   for (const value of ['javascript:alert(1)', 'weixin://evil/bizpayurl?pr=x', 'weixin://wxpay/other?pr=x', 'weixin://wxpay/bizpayurl', 'weixin://user@wxpay/bizpayurl?pr=x']) {
     expect(safeWechatPaymentURL(value)).toBeNull();
   }
 });
});
