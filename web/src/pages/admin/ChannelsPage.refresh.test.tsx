import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';
import { http, HttpResponse } from 'msw';
import { expect, it, vi } from 'vitest';
import { AdminRoute } from '@/components/AdminRoute';
import { authorizationKey, useAuthorizationLifecycle } from '@/lib/authorization';
import { server } from '@/test/msw/server';
import { AdminChannelsPage } from './ChannelsPage';

it('keeps channel actions and export mounted during repeated background authorization refreshes', async () => {
  const intervals = vi.spyOn(globalThis, 'setInterval');
  let calls = 0;
  let release: (() => void) | undefined;
  const reads = vi.fn();
  server.use(
    http.get('/api/user/authorization', async () => {
      if (++calls > 1) await new Promise<void>(resolve => { release = resolve; });
      return HttpResponse.json({ authorization_mode: 'iam', session: { activation_state: 'active', revision: '1' }, versions: { policy_revision: '1' }, valid_until: new Date(Date.now() + 600_000).toISOString(), permitted_operations: ['admin.console.enter', 'channel.channel.list', 'channel.channel.update', 'channel.channel.export'] });
    }),
    http.get('/api/channel', () => {
      reads();
      return HttpResponse.json({ success: true, data: [{ id: '1', name: 'stable channel', type: 1, status: 1, group: 'default', models: 'gpt-4o', permitted_actions: ['channel.channel.update'] }] });
    }),
  );
  function Shell() { useAuthorizationLifecycle(); return <AdminRoute />; }
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(<QueryClientProvider client={client}><MemoryRouter initialEntries={['/admin/channels']}><Routes><Route path="/admin" element={<Shell />}><Route path="channels" element={<AdminChannelsPage />} /></Route></Routes></MemoryRouter></QueryClientProvider>);
  try {
    await screen.findByText('stable channel');
    const edit = await screen.findByRole('button', { name: /编辑/ });
    const download = screen.getByRole('button', { name: /Export CSV/ });
    // Drive the real authorization interval three times without waiting 90s.
    const poll = intervals.mock.calls.find(([, delay]) => delay === 30_000)?.[0];
    expect(poll).toBeTypeOf('function');
    for (let cycle = 0; cycle < 3; cycle++) {
      act(() => { if (typeof poll === 'function') poll(); });
      await waitFor(() => expect(calls).toBe(cycle + 2));
      expect(screen.getByRole('button', { name: /编辑/ })).toBe(edit);
      expect(edit).toBeDisabled();
      expect(screen.getByRole('button', { name: /Export CSV/ })).toBe(download);
      expect(download).toBeDisabled();
      expect(screen.getByText('stable channel')).toBeVisible();
      await act(async () => { release?.(); });
      await waitFor(() => expect(client.getQueryCache().findAll({ queryKey: authorizationKey })[0].state.fetchStatus).toBe('idle'));
      await waitFor(() => expect(edit).toBeEnabled());
      expect(download).toBeEnabled();
      expect(reads).toHaveBeenCalledTimes(1);
    }
  } finally {
    await act(async () => { release?.(); });
    view.unmount(); client.clear();
  }
});
