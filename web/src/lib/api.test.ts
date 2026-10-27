import { describe, expect, it } from 'vitest';
import { apiClient, adminApiClient, isSessionAuthPath } from './api';

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


it('ignores late authorization errors from an old or cleared session', async () => {
  for (const client of [apiClient, adminApiClient]) {
    for (const status of [401, 403]) {
      localStorage.setItem('token', 'old-session');
      await expect(client.get('/user/self', { adapter: async config => {
        localStorage.setItem('token', 'new-session');
        throw { config, response: { status, data: { reason: 'STEP_UP_REQUIRED' } } };
      } })).rejects.toBeDefined();
      expect(localStorage.getItem('token')).toBe('new-session');
      localStorage.setItem('token', 'old-session');
      await expect(client.get('/user/self', { adapter: async config => {
        localStorage.removeItem('token');
        throw { config, response: { status, data: { reason: 'STEP_UP_REQUIRED' } } };
      } })).rejects.toBeDefined();
      expect(localStorage.getItem('token')).toBeNull();
    }
  }
});
