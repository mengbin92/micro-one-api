import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';
import { http, HttpResponse } from 'msw';
import { expect, it, vi } from 'vitest';
import { AdminRoute } from '@/components/AdminRoute';
import { useAuthorizationLifecycle } from '@/lib/authorization';
import { server } from '@/test/msw/server';
import { AdminChannelsPage } from './ChannelsPage';

it.each([
  ['test', 'channel.channel.test', /^测试$/],
  ['update_balance', 'channel.channel.balance.refresh', /^刷新$/],
] as const)('refreshes a channel revision after a %s conflict without retrying the action', async (action, operation, buttonName) => {
  vi.spyOn(window, 'prompt').mockReturnValue('test channel 1');
  let revision = '6';
  const attempted: string[] = [];
  const reads = vi.fn();
  const channel = { id: '1', name: 'channel conflict', type: 1, status: 1, group: 'default', models: 'deepseek-flash', permitted_actions: [operation] };
  server.use(
    http.get('/api/user/authorization', () => HttpResponse.json({ authorization_mode: 'iam', session: { activation_state: 'active', revision: '1' }, versions: { policy_revision: '1' }, valid_until: new Date(Date.now() + 600_000).toISOString(), permitted_operations: ['admin.console.enter', 'channel.channel.list', operation] })),
    http.get('/api/channel', () => { reads(); return HttpResponse.json({ success: true, data: [{ ...channel, authorization_revision: revision }] }); }),
    http.get(`/api/channel/${action}/1`, ({ request }) => {
      attempted.push(new URL(request.url).searchParams.get('expected_revision') ?? '');
      if (attempted.length === 1) {
        revision = '7';
        return HttpResponse.json({ reason: 'RESOURCE_VERSION_CONFLICT', message: 'resource revision changed' }, { status: 409 });
      }
      return HttpResponse.json({ success: true });
    }),
  );
  function Shell() { useAuthorizationLifecycle(); return <AdminRoute />; }
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const view = render(<QueryClientProvider client={client}><MemoryRouter initialEntries={['/admin/channels']}><Routes><Route path="/admin" element={<Shell />}><Route path="channels" element={<AdminChannelsPage />} /></Route></Routes></MemoryRouter></QueryClientProvider>);
  try {
    await screen.findByText('channel conflict');
    const initialReads = reads.mock.calls.length;
    fireEvent.click(await screen.findByRole('button', { name: buttonName }));
    await waitFor(() => expect(attempted).toEqual(['6']));
    await waitFor(() => expect(reads.mock.calls.length).toBeGreaterThan(initialReads));
    await waitFor(() => expect(client.getQueryCache().findAll({ queryKey: ['admin-channels'] }).every(q => q.state.fetchStatus === 'idle')).toBe(true));
    expect(attempted).toEqual(['6']);
    fireEvent.click(screen.getByRole('button', { name: buttonName }));
    await waitFor(() => expect(attempted).toEqual(['6', '7']));
  } finally { view.unmount(); client.clear(); }
});
