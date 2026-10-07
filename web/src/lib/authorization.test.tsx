import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor, act } from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import { describe, it, expect, vi } from 'vitest';
import { server } from '@/test/msw/server';
import { authorizationKey, can, canAll, canAny, suspendProtectedQueries, useAuthorizedQuery, useAuthorizationLifecycle } from './authorization';
import { adminApiClient } from './api';
import { refreshAuthorization } from './authorization-events';
import type { IAMReply } from './iam-types';
import { prepareAdminRequest } from './authorization-events';
import { AxiosHeaders } from 'axios';
const active = (operations: string[], revision = '1'): IAMReply => ({ authorization_mode: 'iam', session: { activation_state: 'active', revision }, permitted_operations: operations, versions: { policy_revision: revision }, valid_until: new Date(Date.now() + 60_000).toISOString() });
function Consumer() {
 useAuthorizationLifecycle();
 const data = useAuthorizedQuery({ permission: 'channel.channel.list', queryKey: ['protected-list'], queryFn: async () => (await adminApiClient.get('/test-protected')).data.value });
 return <div>{data.data ?? 'hidden'}<button onClick={() => void data.refetch()}>refetch</button></div>;
}
function mount(client: QueryClient) { return render(<QueryClientProvider client={client}><Consumer /></QueryClientProvider>); }
describe('authorization lifecycle', () => {
 it('keeps verified data visible and avoids owner refetch during a same-version background authorization refresh', async () => {
  const snapshot = { ...active(['channel.channel.list']), versions: { policy_revision: '1', user_revision: '2' }, authorized_role_ids: ['2', '1'], active_role_ids: ['2', '1'] };
  let authCalls = 0;
  let release: (() => void) | undefined;
  const requests = vi.fn();
  server.use(
   http.get('/api/user/authorization', async () => {
    authCalls++;
    if (authCalls > 1) await new Promise<void>(resolve => { release = resolve; });
    return HttpResponse.json({ ...snapshot, authorized_role_ids: authCalls > 1 ? ['1', '2'] : ['2', '1'], active_role_ids: authCalls > 1 ? ['1', '2'] : ['2', '1'], versions: { user_revision: '2', policy_revision: '1' }, valid_until: new Date(Date.now() + 60_000).toISOString() });
   }),
   http.get('/api/test-protected', () => { requests(); return HttpResponse.json({ value: 'stable data' }); }),
  );
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 60_000 } } });
  const result = mount(client);
  let refreshing: Promise<void> | undefined;
  let writeBlockedBeforeRender = false;
  try {
   await screen.findByText('stable data');
   const initialOwnerQuery = client.getQueryCache().findAll({ queryKey: ['protected-list'] })[0];
   expect(initialOwnerQuery).toBeDefined();
   expect(prepareAdminRequest({ url: '/channel', method: 'put', data: { reason: 'reviewed reason' }, headers: new AxiosHeaders() }).data.reason).toBe('reviewed reason');
   act(() => {
    refreshing = client.refetchQueries({ queryKey: authorizationKey });
    try { prepareAdminRequest({ url: '/channel', method: 'put', data: { reason: 'reviewed reason' }, headers: new AxiosHeaders() }); }
    catch { writeBlockedBeforeRender = true; }
   });
   await waitFor(() => expect(authCalls).toBe(2));
   expect(writeBlockedBeforeRender).toBe(true);
   expect(screen.getByText('stable data')).toBeVisible();
   expect(() => prepareAdminRequest({ url: '/channel', method: 'put', data: { reason: 'reviewed reason' }, headers: new AxiosHeaders() })).toThrow('授权状态不可用');
   expect(() => prepareAdminRequest({ url: '/v1/admin/iam/roles', method: 'post', data: {}, headers: new AxiosHeaders() })).toThrow('授权状态不可用');
   await act(async () => { release?.(); await refreshing; });
   expect(client.getQueryCache().findAll({ queryKey: ['protected-list'] })[0]).toBe(initialOwnerQuery);
   expect(screen.getByText('stable data')).toBeVisible();
   expect(requests).toHaveBeenCalledTimes(1);
   expect(prepareAdminRequest({ url: '/v1/admin/iam/roles', method: 'patch', headers: new AxiosHeaders() }).method).toBe('patch');
  } finally {
   await act(async () => { release?.(); await refreshing; });
   result.unmount(); client.clear();
  }
 });
 it.each(['versions', 'operations', 'session', 'session_revision', 'roles', 'expired', 'malformed_expiry', 'unknown_mode', 'legacy_mode', 'failed'])('clears previously displayed data when background authorization changes %s', async (change) => {
  let snapshot = active(['channel.channel.list']);
  let failed = false;
  let calls = 0;
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 60_000 } } });
  server.use(
   http.get('/api/user/authorization', () => failed ? HttpResponse.json({ message: 'unavailable' }, { status: 503 }) : HttpResponse.json(snapshot)),
   http.get('/api/test-protected', () => HttpResponse.json({ value: ++calls === 1 ? 'old protected data' : 'new protected data' })),
  );
  const view = mount(client);
  try {
   await screen.findByText('old protected data');
   if (change === 'versions') snapshot = active(['channel.channel.list'], '2');
   if (change === 'operations') snapshot = { ...snapshot, permitted_operations: [] };
   if (change === 'session') snapshot = { ...snapshot, session: { activation_state: 'selection_required', revision: '2' } };
   if (change === 'session_revision') snapshot = { ...snapshot, session: { activation_state: 'active', revision: '2' } };
   if (change === 'roles') snapshot = { ...snapshot, active_role_ids: ['2'] };
   if (change === 'expired') snapshot = { ...snapshot, valid_until: new Date(0).toISOString() };
   if (change === 'malformed_expiry') snapshot = { ...snapshot, valid_until: 'not-a-date' };
   if (change === 'unknown_mode') snapshot = { ...snapshot, authorization_mode: 'unknown' };
   if (change === 'legacy_mode') snapshot = { ...snapshot, authorization_mode: 'legacy', legacy_admin: false };
   if (change === 'failed') failed = true;
   await act(async () => { await client.refetchQueries({ queryKey: authorizationKey }); });
   await waitFor(() => expect(screen.queryByText('old protected data')).not.toBeInTheDocument());
   if (change === 'versions' || change === 'session_revision' || change === 'roles') {
    expect(await screen.findByText('new protected data')).toBeVisible();
    expect(calls).toBe(2);
   } else {
    expect(screen.getByText('hidden')).toBeVisible(); expect(calls).toBe(1);
    expect(client.getQueryCache().findAll({ predicate: q => q.meta?.protected === true }).every(q => q.state.data === undefined)).toBe(true);
   }
  } finally { view.unmount(); client.clear(); }
 });
 it('starts a new permitted owner read without serially waiting for an unchanged background summary', async () => {
  let authCalls = 0;
  let release: (() => void) | undefined;
  server.use(http.get('/api/user/authorization', async () => {
   if (++authCalls > 1) await new Promise<void>(resolve => { release = resolve; });
   return HttpResponse.json(active(['channel.channel.list']));
  }), http.get('/api/test-protected', () => HttpResponse.json({ value: 'first page data' })), http.get('/api/next-page', () => HttpResponse.json({ value: 'next page data' })));
  function NextPage() {
   const data = useAuthorizedQuery({ permission: 'channel.channel.list', queryKey: ['next-page-list'], queryFn: async () => (await adminApiClient.get('/next-page')).data.value });
   return <div>{data.data ?? 'next page loading'}</div>;
  }
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 60_000 } } }); const view = mount(client);
  let refreshing: Promise<void> | undefined;
  try {
   await screen.findByText('first page data');
   act(() => { refreshing = client.refetchQueries({ queryKey: authorizationKey }); });
   await waitFor(() => expect(authCalls).toBe(2));
   view.rerender(<QueryClientProvider client={client}><Consumer /><NextPage /></QueryClientProvider>);
   expect(await screen.findByText('next page data')).toBeVisible();
   expect(client.getQueryCache().findAll({ queryKey: authorizationKey })[0].state.fetchStatus).toBe('fetching');
  } finally {
   await act(async () => { release?.(); await refreshing; });
   view.unmount(); client.clear();
  }
 });
 it('expires displayed data on time even while a background authorization response is blocked', async () => {
  const started = Date.now();
  const now = vi.spyOn(Date, 'now').mockReturnValue(started);
  const originalSetTimeout = window.setTimeout.bind(window);
  const originalClearTimeout = window.clearTimeout.bind(window);
  const deadlines = new Map<number, TimerHandler>();
  vi.spyOn(window, 'setTimeout').mockImplementation((handler, delay, ...args) => {
   const timer = originalSetTimeout(handler, delay, ...args);
   if (delay === 60_000) deadlines.set(timer, handler);
   return timer;
  });
  vi.spyOn(window, 'clearTimeout').mockImplementation(timer => { if (timer !== undefined) deadlines.delete(timer); originalClearTimeout(timer); });
  const snapshot = active(['channel.channel.list']);
  const releases: Array<() => void> = [];
  let calls = 0;
  server.use(http.get('/api/user/authorization', async () => {
   if (++calls > 1) await new Promise<void>(resolve => releases.push(resolve));
   return HttpResponse.json(snapshot);
  }), http.get('/api/test-protected', () => HttpResponse.json({ value: 'expires during polling' })));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 60_000 } } }); const view = mount(client);
  let refreshing: Promise<void> | undefined;
  try {
   await screen.findByText('expires during polling');
   act(() => { refreshing = client.refetchQueries({ queryKey: authorizationKey }); });
   await waitFor(() => expect(calls).toBe(2));
   expect(deadlines.size).toBe(1);
   expect(screen.getByText('expires during polling')).toBeVisible();
   now.mockReturnValue(started + 60_001);
   await act(async () => { for (const callback of [...deadlines.values()]) if (typeof callback === 'function') callback(); });
   await waitFor(() => expect(screen.queryByText('expires during polling')).not.toBeInTheDocument());
   expect(client.getQueryCache().findAll({ predicate: q => q.meta?.protected === true }).every(q => q.state.data === undefined)).toBe(true);
  } finally {
   view.unmount(); client.clear();
   for (const release of releases) release();
   await refreshing;
  }
 });
 it('does not reload authorization or owner data for another tab changing appearance preferences', async () => {
  let calls = 0;
  const requests = vi.fn();
  server.use(http.get('/api/user/authorization', () => { calls++; return HttpResponse.json(active(['channel.channel.list'])); }), http.get('/api/test-protected', () => { requests(); return HttpResponse.json({ value: 'stable preference data' }); }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 60_000 } } }); const view = mount(client);
  try {
   await screen.findByText('stable preference data');
   await act(async () => { window.dispatchEvent(new StorageEvent('storage', { key: 'web:theme', newValue: '"dark"' })); await new Promise(resolve => setTimeout(resolve, 0)); });
   expect(screen.getByText('stable preference data')).toBeVisible();
   expect(calls).toBe(1); expect(requests).toHaveBeenCalledTimes(1);
  } finally { view.unmount(); client.clear(); }
 });
 it('requires all operations for financial paths and permits any only when explicitly requested', () => {
  const snapshot = active(['billing.payment.list']);
  expect(canAll(snapshot, ['admin.console.enter', 'billing.payment.list'])).toBe(false);
  expect(canAny(snapshot, ['billing.payment.read', 'billing.payment.list'])).toBe(true);
  expect(canAny(snapshot, ['billing.payment.read'])).toBe(false);
  expect(can({ ...snapshot, authorization_mode: 'future-mode' }, 'billing.payment.list')).toBe(false);
  expect(can({ ...snapshot, session: undefined }, 'billing.payment.list')).toBe(false);
 });
 it('uses verified legacy state and IAM summary, ignores numeric local role, rejects expiry and unknown modes', () => {
  localStorage.setItem('userRole', '100');
  expect(can(undefined, 'admin.console.enter')).toBe(false);
  expect(can({ authorization_mode: 'legacy', legacy_admin: false }, 'admin.console.enter')).toBe(false);
  expect(can({ authorization_mode: 'legacy', legacy_admin: true }, 'iam.role.create')).toBe(false);
  expect(can(active(['channel.channel.list']), 'channel.channel.list')).toBe(true);
  expect(can(active(['channel.channel.list']), 'channel.channel.delete')).toBe(false);
  expect(can({ ...active(['channel.channel.list']), valid_until: new Date(0).toISOString() }, 'channel.channel.list')).toBe(false);
  expect(can({ ...active(['channel.channel.list']), session: { activation_state: 'selection_required' } }, 'channel.channel.list')).toBe(false);
 });
 it('clears protected cache and cancels inflight requests without deleting public cache', async () => {
  const client = new QueryClient();
  client.setQueryDefaults(['secret'], { meta: { protected: true } });
  client.setQueryData(['secret'], 'confidential'); client.setQueryData(['public'], 'public');
  suspendProtectedQueries(client);
  expect(client.getQueryData(['secret'])).toBeUndefined(); expect(client.getQueryData(['public'])).toBe('public');
 });
 it('does not start unpermitted queries and gates manual refetch', async () => {
  server.use(http.get('/api/user/authorization', () => HttpResponse.json(active([]))));
  const request = vi.fn(); server.use(http.get('/api/test-protected', () => { request(); return HttpResponse.json({ value: 'secret' }); }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } }); const result = mount(client);
  await waitFor(() => expect(client.getQueryCache().findAll({ queryKey: ['authorization'] })[0]?.state.status).toBe('success'));
  expect(screen.getByText('hidden')).toBeVisible();
  await act(async () => { screen.getByRole('button', { name: 'refetch' }).click(); });
  expect(request).not.toHaveBeenCalled(); result.unmount(); client.clear();
 });
 it('on 403 retains JWT, clears old generation and refreshes once without query retries', async () => {
  localStorage.setItem('token', 'valid-user-jti'); let snapshot = active(['channel.channel.list']); let deny = false;
  const authRequests = vi.fn(); const protectedRequests = vi.fn();
  server.use(http.get('/api/user/authorization', () => { authRequests(); return HttpResponse.json(snapshot); }), http.get('/api/test-protected', () => { protectedRequests(); return deny ? HttpResponse.json({ message: 'revoked' }, { status: 403 }) : HttpResponse.json({ value: 'confidential' }); }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: 3 } } }); const result = mount(client);
  await screen.findByText('confidential'); deny = true; snapshot = active([], '2');
  await act(async () => { await client.invalidateQueries({ queryKey: ['protected-list'] }); });
  await waitFor(() => expect(authRequests).toHaveBeenCalledTimes(2));
  expect(localStorage.getItem('token')).toBe('valid-user-jti'); expect(screen.queryByText('confidential')).not.toBeInTheDocument();
  expect(client.getQueryCache().findAll({ predicate: q => q.meta?.protected === true }).every(q => q.state.data === undefined)).toBe(true);
  expect(protectedRequests).toHaveBeenCalledTimes(2); result.unmount(); client.clear();
 });
 it('stops automatic reads after persistent 403 with an unchanged authorization summary', async () => {
  localStorage.setItem('token', 'valid-user-jti');
  const authRequests = vi.fn(); const protectedRequests = vi.fn();
  let snapshot = active(['channel.channel.list']);
  let deny = true;
  server.use(
   http.get('/api/user/authorization', () => { authRequests(); return HttpResponse.json(snapshot); }),
   http.get('/api/test-protected', () => { protectedRequests(); return deny ? HttpResponse.json({ message: 'authorization denied', success: false }, { status: 403 }) : HttpResponse.json({ value: 'recovered data' }); }),
  );
  const client = new QueryClient({ defaultOptions: { queries: { retry: 3 } } });
  const view = mount(client);
  try {
   await waitFor(() => expect(authRequests.mock.calls.length).toBeGreaterThanOrEqual(2));
   await act(async () => { await new Promise(resolve => setTimeout(resolve, 100)); });
   expect(protectedRequests).toHaveBeenCalledTimes(1);
   expect(authRequests).toHaveBeenCalledTimes(2);
   expect(localStorage.getItem('token')).toBe('valid-user-jti');
   // The denial must survive both route remounts and unchanged background polls.
   view.rerender(<QueryClientProvider client={client}>{null}</QueryClientProvider>);
   view.rerender(<QueryClientProvider client={client}><Consumer /></QueryClientProvider>);
   await act(async () => { await client.refetchQueries({ queryKey: authorizationKey }); });
   await act(async () => { await client.refetchQueries({ queryKey: ['protected-list'] }); });
   expect(protectedRequests).toHaveBeenCalledTimes(1);
   // A real policy boundary change can automatically try the read again.
   deny = false; snapshot = active(['channel.channel.list'], '2');
   await act(async () => { await client.refetchQueries({ queryKey: authorizationKey }); });
   expect(await screen.findByText('recovered data')).toBeVisible();
   expect(protectedRequests).toHaveBeenCalledTimes(2);
  } finally { view.unmount(); client.clear(); }
 });
 it('allows an explicit retry after a denied read without trusting an unavailable summary', async () => {
  let deny = true;
  let authFailed = false;
  const requests = vi.fn();
  server.use(http.get('/api/user/authorization', () => authFailed ? HttpResponse.json({ message: 'unavailable' }, { status: 503 }) : HttpResponse.json(active(['channel.channel.list']))), http.get('/api/test-protected', () => {
   requests(); return deny ? HttpResponse.json({ message: 'authorization denied' }, { status: 403 }) : HttpResponse.json({ value: 'manual recovery' });
  }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } }); const view = mount(client);
  try {
   await waitFor(() => expect(client.getQueryCache().findAll({ queryKey: ['authorization-denial'] }).some(q => q.state.data)).toBe(true));
   await waitFor(() => expect(client.getQueryCache().findAll({ queryKey: authorizationKey })[0]?.state.fetchStatus).toBe('idle'));
   authFailed = true;
   await act(async () => { await client.refetchQueries({ queryKey: authorizationKey }); });
   await act(async () => { screen.getByRole('button', { name: 'refetch' }).click(); });
   expect(requests).toHaveBeenCalledTimes(1);
   authFailed = false;
   await act(async () => { await client.refetchQueries({ queryKey: authorizationKey }); });
   deny = false;
   await act(async () => { screen.getByRole('button', { name: 'refetch' }).click(); });
   expect(await screen.findByText('manual recovery')).toBeVisible();
   expect(requests).toHaveBeenCalledTimes(2);
  } finally { view.unmount(); client.clear(); }
 });
 it('failed refresh never resumes the protected cached query', async () => {
  let failed = false; const requests = vi.fn();
  server.use(http.get('/api/user/authorization', () => failed ? HttpResponse.json({ message: 'unavailable' }, { status: 503 }) : HttpResponse.json(active(['channel.channel.list']))), http.get('/api/test-protected', () => { requests(); return HttpResponse.json({ value: 'secret' }); }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } }); const result = mount(client);
  await screen.findByText('secret'); failed = true; act(() => refreshAuthorization());
  await waitFor(() => expect(client.getQueryCache().findAll({ queryKey: ['authorization'] })[0]?.state.status).toBe('error'));
  expect(screen.queryByText('secret')).not.toBeInTheDocument(); expect(requests).toHaveBeenCalledTimes(1); result.unmount(); client.clear();
 });
});
