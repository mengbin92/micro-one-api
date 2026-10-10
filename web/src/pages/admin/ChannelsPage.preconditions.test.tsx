import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';
import { http, HttpResponse } from 'msw';
import { expect, it, vi } from 'vitest';
import { AdminRoute } from '@/components/AdminRoute';
import { useAuthorizationLifecycle } from '@/lib/authorization';
import { server } from '@/test/msw/server';
import { AdminChannelsPage } from './ChannelsPage';

it.each([
 ['camelCase', { authorizationRevision: '3' }, { authorizationRevision: '4' }],
 ['snake_case', { authorization_revision: 3 }, { authorization_revision: 4 }],
])('preserves the channel draft revision when the %s list updates in the background', async (_format, initialRevision, updatedRevision) => {
 let captured: Record<string, unknown> | undefined;
 vi.spyOn(window, 'prompt').mockReturnValue('edit');
 const channel = { id: '1', name: 'stable channel', type: 1, status: 1, group: 'default', models: 'gpt-4o', ...initialRevision, permitted_actions: ['channel.channel.update'] };
 server.use(
  http.get('/api/user/authorization', () => HttpResponse.json({ authorization_mode: 'iam', session: { activation_state: 'active', revision: '1' }, versions: { policy_revision: '1' }, valid_until: new Date(Date.now() + 600_000).toISOString(), permitted_operations: ['admin.console.enter', 'channel.channel.list', 'channel.channel.update'] })),
  http.get('/api/channel', () => HttpResponse.json({ success: true, data: [channel] })),
  http.put('/api/channel', async ({request}) => { captured = await request.json() as Record<string, unknown>; return HttpResponse.json({success:true}); }),
 );
 function Shell() { useAuthorizationLifecycle(); return <AdminRoute />; }
 const c = new QueryClient({defaultOptions:{queries:{retry:false}}});
 const view = render(<QueryClientProvider client={c}><MemoryRouter initialEntries={['/admin/channels']}><Routes><Route path="/admin" element={<Shell />}><Route path="channels" element={<AdminChannelsPage />} /></Route></Routes></MemoryRouter></QueryClientProvider>);
 try {
  await screen.findByText('stable channel');
  const edit = await screen.findByRole('button', {name:/编辑/});
  fireEvent.click(edit);
  await screen.findByRole('button', {name:/保存配置/});
  const key = c.getQueryCache().findAll({queryKey:['admin-channels']})[0].queryKey;
  await act(async () => { c.setQueryData(key, [{...channel, name:'someone else updated', ...updatedRevision}]); });
  fireEvent.click(screen.getByRole('button', {name:/保存配置/}));
  await waitFor(() => expect(captured).toBeDefined());
  expect(captured!.expected_revision).toBe(3);
 } finally { view.unmount(); c.clear(); }
});
