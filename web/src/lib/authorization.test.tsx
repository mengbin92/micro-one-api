import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor, act } from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import { describe, it, expect, vi } from 'vitest';
import { server } from '@/test/msw/server';
import { can, suspendProtectedQueries, useAuthorizedQuery, useAuthorizationLifecycle } from './authorization';
import { adminApiClient } from './api';
import { refreshAuthorization } from './authorization-events';
import type { IAMReply } from './iam-types';
const active = (operations: string[], revision = '1'): IAMReply => ({ authorization_mode: 'iam', session: { activation_state: 'active', revision }, permitted_operations: operations, versions: { policy_revision: revision }, valid_until: new Date(Date.now() + 60_000).toISOString() });
function Consumer() {
 useAuthorizationLifecycle();
 const data = useAuthorizedQuery({ permission: 'channel.channel.list', queryKey: ['protected-list'], queryFn: async () => (await adminApiClient.get('/test-protected')).data.value });
 return <div>{data.data ?? 'hidden'}<button onClick={() => void data.refetch()}>refetch</button></div>;
}
function mount(client: QueryClient) { return render(<QueryClientProvider client={client}><Consumer /></QueryClientProvider>); }
describe('authorization lifecycle', () => {
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
 it('failed refresh never resumes the protected cached query', async () => {
  let failed = false; const requests = vi.fn();
  server.use(http.get('/api/user/authorization', () => failed ? HttpResponse.json({ message: 'unavailable' }, { status: 503 }) : HttpResponse.json(active(['channel.channel.list']))), http.get('/api/test-protected', () => { requests(); return HttpResponse.json({ value: 'secret' }); }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } }); const result = mount(client);
  await screen.findByText('secret'); failed = true; act(() => refreshAuthorization());
  await waitFor(() => expect(client.getQueryCache().findAll({ queryKey: ['authorization'] })[0]?.state.status).toBe('error'));
  expect(screen.queryByText('secret')).not.toBeInTheDocument(); expect(requests).toHaveBeenCalledTimes(1); result.unmount(); client.clear();
 });
});
