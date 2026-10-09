import { describe, it, expect } from 'vitest';
import { safePaymentURL } from './payment-url';
describe('payment URL', () => {
 it('allows HTTPS and rejects executable or untrusted schemes', () => {
   expect(safePaymentURL('https://pay.example/order')).toBe('https://pay.example/order');
   for (const value of ['javascript:alert(1)', 'data:text/html,test', 'http://pay.example', '//pay.example', 'https://user:pass@pay.example']) expect(safePaymentURL(value)).toBeNull();
 });
});
