import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';
import { http, HttpResponse } from 'msw';
import { expect, it, vi } from 'vitest';
import { AdminRoute } from '@/components/AdminRoute';
import { useAuthorizationLifecycle } from '@/lib/authorization';
import { server } from '@/test/msw/server';
import { AdminChannelsPage } from './ChannelsPage';

function setup() {
  vi.spyOn(window, 'prompt').mockReturnValue('纠正上游供应商类型');
  server.use(
    http.get('/api/user/authorization', () => HttpResponse.json({ authorization_mode: 'iam', session: { activation_state: 'active', revision: '1' }, versions: { policy_revision: '1' }, valid_until: new Date(Date.now() + 600_000).toISOString(), permitted_operations: ['admin.console.enter', 'channel.channel.list', 'channel.channel.create', 'channel.channel.update'] })),
    http.get('/api/channel', () => HttpResponse.json({ success: true, data: [{ id: '11', name: 'ds', type: 14, status: 1, group: 'default', models: '', authorization_revision: '2', permitted_actions: ['channel.channel.update'] }] })),
  );
  function Shell() { useAuthorizationLifecycle(); return <AdminRoute />; }
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(<QueryClientProvider client={client}><MemoryRouter initialEntries={['/admin/channels']}><Routes><Route path="/admin" element={<Shell />}><Route path="channels" element={<AdminChannelsPage />} /></Route></Routes></MemoryRouter></QueryClientProvider>);
  return () => { view.unmount(); client.clear(); };
}

it('sends backend DeepSeek type 6 when creating a channel', async () => {
  let captured: Record<string, unknown> | undefined;
  server.use(http.post('/api/channel', async ({ request }) => { captured = await request.json() as Record<string, unknown>; return HttpResponse.json({ success: true }); }));
  const cleanup = setup();
  try {
    fireEvent.click(await screen.findByRole('button', { name: /^创建渠道$/ }));
    const dialog = within(await screen.findByRole('dialog'));
    const select = dialog.getByLabelText('供应商');
    const deepseek = within(select).getByRole('option', { name: 'DeepSeek' }) as HTMLOptionElement;
    fireEvent.change(select, { target: { value: deepseek.value } });
    fireEvent.change(dialog.getByLabelText('名称'), { target: { value: 'official' } });
    fireEvent.change(dialog.getByLabelText('基础 URL'), { target: { value: 'https://api.deepseek.com/' } });
    fireEvent.change(dialog.getByLabelText('API 密钥'), { target: { value: 'fixture-key' } });
    fireEvent.click(dialog.getByRole('button', { name: /^创建$/ }));
    await waitFor(() => expect(captured).toBeDefined());
    expect(captured!.type).toBe(6);
    expect(captured!.reason).toBe('纠正上游供应商类型');
  } finally { cleanup(); }
});

it('corrects an existing provider with empty models while preserving its displayed revision and credential', async () => {
  let captured: Record<string, unknown> | undefined;
  server.use(http.put('/api/channel', async ({ request }) => { captured = await request.json() as Record<string, unknown>; return HttpResponse.json({ success: true }); }));
  const cleanup = setup();
  try {
    fireEvent.click(await screen.findByRole('button', { name: /^编辑$/ }));
    const dialog = within(await screen.findByRole('dialog'));
    fireEvent.change(dialog.getByLabelText('供应商'), { target: { value: '6' } });
    fireEvent.click(dialog.getByRole('button', { name: /^保存配置$/ }));
    await waitFor(() => expect(captured).toBeDefined());
    expect(captured!.type).toBe(6);
    expect(captured!.expected_revision).toBe(2);
    expect(captured!.reason).toBe('纠正上游供应商类型');
    expect(captured!.models).toBe('');
    expect(captured).not.toHaveProperty('key');
  } finally { cleanup(); }
});
