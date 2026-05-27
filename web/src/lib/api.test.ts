import { describe, expect, it } from 'vitest';
import { isSessionAuthPath } from './api';

describe('api auth path handling', () => {
  it('treats user session endpoints as login-expiring paths', () => {
    expect(isSessionAuthPath('/user/self')).toBe(true);
    expect(isSessionAuthPath('/user/topup')).toBe(true);
    expect(isSessionAuthPath('/token')).toBe(true);
    expect(isSessionAuthPath('/token/1')).toBe(true);
  });

  it('does not clear the user session for admin-only endpoint auth failures', () => {
    expect(isSessionAuthPath('/admin/summary')).toBe(false);
    expect(isSessionAuthPath('/redemption')).toBe(false);
    expect(isSessionAuthPath('/channel/1')).toBe(false);
  });
});
