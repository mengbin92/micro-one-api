import { describe, expect, it, vi } from 'vitest';
import { apiClient, adminApiClient, isSessionAuthPath } from './api';
import { AUTHORIZATION_INVALIDATE, AUTHORIZATION_REFRESH, protectedSignal } from './authorization-events';

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
  const events = vi.spyOn(window, 'dispatchEvent');
  for (const client of [apiClient, adminApiClient]) {
    for (const status of [401, 403]) {
      const signal = protectedSignal();
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
      expect(events.mock.calls.filter(([event]) => event.type === AUTHORIZATION_REFRESH)).toHaveLength(0);
      expect(signal.aborted).toBe(false);
    }
  }
});


it('rejects a bound request before dispatch if its session was replaced', async () => {
  localStorage.setItem('token', 'replacement-session');
  for (const client of [apiClient, adminApiClient]) {
    const adapter = vi.fn();
    await expect(client.post('/user/security/verify', { password: 'old-factor' }, {
      headers: { Authorization: 'Bearer old-session' }, adapter,
    })).rejects.toThrow('Session changed before request');
    expect(adapter).not.toHaveBeenCalled();
  }
});

describe('managed write invalidates the authorization snapshot', () => {
  function countInvalidations(calls: unknown[][]) {
    return calls.filter(([event]) => (event as Event)?.type === AUTHORIZATION_INVALIDATE).length;
  }
  const casParams = { expected_revision: 2, expected_policy_revision: 24, reason: 'stop' };

  it('reloads the snapshot after a successful policy-managed write, not after reads', async () => {
    localStorage.setItem('token', 'session');
    const spy = vi.spyOn(window, 'dispatchEvent');
    try {
      await adminApiClient.get('/user', { adapter: async config => ({ data: { success: true }, status: 200, statusText: 'OK', headers: {}, config }) });
      expect(countInvalidations(spy.mock.calls as unknown[][])).toBe(0);

      await adminApiClient.post('/user/disable/12', undefined, { params: casParams, adapter: async config => ({ data: { success: true }, status: 200, statusText: 'OK', headers: {}, config }) });
      expect(countInvalidations(spy.mock.calls as unknown[][])).toBe(1);
    } finally {
      spy.mockRestore();
    }
  });

  it('reloads the snapshot after a 409 version conflict on a policy-managed write', async () => {
    localStorage.setItem('token', 'session');
    const spy = vi.spyOn(window, 'dispatchEvent');
    try {
      await expect(adminApiClient.post('/user/disable/12', undefined, { params: casParams, adapter: async config => {
        throw { config, response: { status: 409, data: { success: false, message: 'authorization changed; reload and preview again' } } };
      } })).rejects.toBeDefined();
      expect(countInvalidations(spy.mock.calls as unknown[][])).toBe(1);
    } finally {
      spy.mockRestore();
    }
  });

  it.each([200, 409])('recognizes a body-only policy CAS token after Axios serialization (%s)', async status => {
    localStorage.setItem('token', 'session');
    const spy = vi.spyOn(window, 'dispatchEvent');
    const request = adminApiClient.patch('/v1/admin/iam/roles/12', casParams, { adapter: async config => {
      expect(typeof config.data).toBe('string');
      if (status === 409) throw { config, response: { status, data: {} } };
      return { data: {}, status, statusText: 'OK', headers: {}, config };
    } });
    if (status === 409) await expect(request).rejects.toBeDefined();
    else await request;
    expect(countInvalidations(spy.mock.calls as unknown[][])).toBe(1);
  });

  it.each(['authorization:simulate', 'roles/12/preview', 'users/12/roles:preview', 'authorization:check', 'authorization:explain'])('does not invalidate after a successful IAM read-only POST (%s)', async path => {
    localStorage.setItem('token', 'session');
    const spy = vi.spyOn(window, 'dispatchEvent');
    await adminApiClient.post(`/v1/admin/iam/${path}`, casParams, {
      params: casParams,
      adapter: async config => ({ data: {}, status: 200, statusText: 'OK', headers: {}, config }),
    });
    expect(countInvalidations(spy.mock.calls as unknown[][])).toBe(0);
  });

  it('revalidates after a policy conflict on an IAM preview', async () => {
    localStorage.setItem('token', 'session');
    const spy = vi.spyOn(window, 'dispatchEvent');
    await expect(adminApiClient.post('/v1/admin/iam/authorization:simulate', casParams, { adapter: async config => {
      throw { config, response: { status: 409, data: {} } };
    } })).rejects.toBeDefined();
    expect(countInvalidations(spy.mock.calls as unknown[][])).toBe(1);
  });

  it.each([200, 409])('ignores policy responses from a replaced session (%s)', async status => {
    localStorage.setItem('token', 'old-session');
    const spy = vi.spyOn(window, 'dispatchEvent');
    const request = adminApiClient.post('/user/disable/12', undefined, { params: casParams, adapter: async config => {
      localStorage.setItem('token', 'replacement-session');
      if (status === 409) throw { config, response: { status, data: {} } };
      return { data: {}, status, statusText: 'OK', headers: {}, config };
    } });
    if (status === 409) await expect(request).rejects.toBeDefined();
    else await request;
    expect(countInvalidations(spy.mock.calls as unknown[][])).toBe(0);
  });

  it('does not invalidate for writes without a policy CAS token (unrelated pages)', async () => {
    localStorage.setItem('token', 'session');
    const spy = vi.spyOn(window, 'dispatchEvent');
    try {
      await adminApiClient.post('/upstream-costs/migrate', { reason: 'reviewed' }, { adapter: async config => ({ data: { success: true }, status: 200, statusText: 'OK', headers: {}, config }) });
      expect(countInvalidations(spy.mock.calls as unknown[][])).toBe(0);

      await expect(adminApiClient.post('/upstream-costs/migrate', { reason: 'reviewed' }, { adapter: async config => {
        throw { config, response: { status: 409, data: { success: false } } };
      } })).rejects.toBeDefined();
      expect(countInvalidations(spy.mock.calls as unknown[][])).toBe(0);
    } finally {
      spy.mockRestore();
    }
  });

  it('does not reload the snapshot for a 409 on a read', async () => {
    localStorage.setItem('token', 'session');
    const spy = vi.spyOn(window, 'dispatchEvent');
    try {
      await expect(adminApiClient.get('/user/12', { adapter: async config => {
        throw { config, response: { status: 409, data: { success: false } } };
      } })).rejects.toBeDefined();
      expect(countInvalidations(spy.mock.calls as unknown[][])).toBe(0);
    } finally {
      spy.mockRestore();
    }
  });
});
