import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';
import { http, HttpResponse } from 'msw';
import { expect, it, vi } from 'vitest';
import { AdminRoute } from '@/components/AdminRoute';
import { useAuthorizationLifecycle } from '@/lib/authorization';
import { server } from '@/test/msw/server';
import { AdminChannelsPage } from './ChannelsPage';

function mountChannels(client: QueryClient) {
  function Shell() { useAuthorizationLifecycle(); return <AdminRoute />; }
  return render(<QueryClientProvider client={client}><MemoryRouter initialEntries={['/admin/channels']}><Routes><Route path="/admin" element={<Shell />}><Route path="channels" element={<AdminChannelsPage />} /></Route></Routes></MemoryRouter></QueryClientProvider>);
}

it.each([
  ['test', 'channel.channel.test', /^测试$/, 'get', 1],
  ['update_balance', 'channel.channel.balance.refresh', /^刷新$/, 'get', 1],
  ['enable', 'channel.channel.enable', /^启用$/, 'post', 2],
  ['disable', 'channel.channel.disable', /^禁用$/, 'post', 1],
] as const)('refreshes a channel revision after a %s conflict without retrying the action', async (action, operation, buttonName, method, status) => {
  localStorage.setItem('token', 'session');
  vi.spyOn(window, 'prompt').mockReturnValue('复核渠道可用性');
  let revision = '6';
  const attempted: string[] = [];
  const reads = vi.fn();
  const channel = { id: '1', name: 'channel conflict', type: 2, status, group: 'default', models: 'step-test', permitted_actions: [operation] };
  server.use(
    http.get('/api/user/authorization', () => HttpResponse.json({ authorization_mode: 'iam', session: { activation_state: 'active', revision: '1' }, versions: { policy_revision: '1' }, valid_until: new Date(Date.now() + 600_000).toISOString(), permitted_operations: ['admin.console.enter', 'channel.channel.list', operation] })),
    http.get('/api/channel', () => { reads(); return HttpResponse.json({ success: true, data: [{ ...channel, authorization_revision: revision }] }); }),
    http[method](`/api/channel/${action}/1`, ({ request }) => {
      expect(request.headers.has('x-authorization-reason')).toBe(false);
      expect(new URL(request.url).searchParams.get('reason')).toBe('复核渠道可用性');
      attempted.push(new URL(request.url).searchParams.get('expected_revision') ?? '');
      if (attempted.length === 1) {
        revision = '7';
        return HttpResponse.json({ reason: 'RESOURCE_VERSION_CONFLICT', message: 'resource revision changed' }, { status: 409 });
      }
      return HttpResponse.json({ success: true });
    }),
  );
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const view = mountChannels(client);
  try {
    await screen.findByText('channel conflict');
    const initialReads = reads.mock.calls.length;
    fireEvent.click(await screen.findByRole('button', { name: buttonName }));
    await waitFor(() => expect(attempted).toEqual(['6']));
    await waitFor(() => expect(reads.mock.calls.length).toBeGreaterThan(initialReads));
    await waitFor(() => expect(client.getQueryCache().findAll({ queryKey: ['admin-channels'] }).every(q => q.state.fetchStatus === 'idle')).toBe(true));
    expect(attempted).toEqual(['6']);
    const button = screen.getByRole('button', { name: buttonName });
    await waitFor(() => expect(button).toBeEnabled());
    fireEvent.click(button);
    await waitFor(() => expect(attempted).toEqual(['6', '7']));
  } finally { view.unmount(); client.clear(); }
});

it('waits for the new channel revision between a successful test and enable', async () => {
  localStorage.setItem('token', 'session');
  vi.spyOn(window, 'prompt').mockReturnValue('复核渠道可用性');
  let revision = 2;
  let releaseList: (() => void) | undefined;
  const attempts: string[] = [];
  const operations = ['channel.channel.test', 'channel.channel.enable'];
  server.use(
    http.get('/api/user/authorization', () => HttpResponse.json({ authorization_mode: 'iam', session: { activation_state: 'active' }, permitted_operations: ['admin.console.enter', 'channel.channel.list', ...operations] })),
    http.get('/api/channel', async () => {
      if (revision === 3) await new Promise<void>(resolve => { releaseList = resolve; });
      return HttpResponse.json({ success: true, data: [{ id: 9, name: 'probe channel', type: 2, status: 2, group: 'default', models: 'step-test', authorization_revision: revision, permitted_actions: operations }] });
    }),
    http.get('/api/channel/test/9', ({ request }) => {
      attempts.push(new URL(request.url).searchParams.get('expected_revision') ?? '');
      revision = 3;
      return HttpResponse.json({ success: true });
    }),
    http.post('/api/channel/enable/9', ({ request }) => {
      attempts.push(new URL(request.url).searchParams.get('expected_revision') ?? '');
      revision = 4;
      return HttpResponse.json({ success: true });
    }),
  );
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = mountChannels(client);
  try {
    fireEvent.click(await screen.findByRole('button', { name: /^测试$/ }));
    await waitFor(() => expect(releaseList).toBeTypeOf('function'));
    const enable = screen.getByRole('button', { name: /^启用$/ });
    expect(enable).toBeDisabled();
    expect(attempts).toEqual(['2']);
    releaseList?.();
    await waitFor(() => expect(enable).toBeEnabled());
    fireEvent.click(enable);
    await waitFor(() => expect(attempts).toEqual(['2', '3']));
  } finally { releaseList?.(); view.unmount(); client.clear(); }
});
