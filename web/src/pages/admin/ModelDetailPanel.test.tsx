import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { MemoryRouter } from 'react-router';
import { describe, expect, it, vi } from 'vitest';
import { AdminModelsPage } from './ModelsPage';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';
import { useAuthorizationLifecycle } from '@/lib/authorization';

const model = { id: 519, model_id: 'deepseek-flash', display_name: 'DeepSeek Flash', status: 0, configured_status: 1, authorization_revision: '7', is_public: true };
const mapping = { id: 31, model_pk: 519, enabled: false, priority: 3, upstream_model_id: 'deepseek-flash' };

describe('model source activation', () => {
  it.each(['channel', 'subscription'] as const)('enables an existing %s mapping and refreshes effective availability', async (kind) => {
    let enabled = false;
    let revision = 7;
    let body: Record<string, unknown> | undefined;
    vi.spyOn(window, 'prompt').mockReturnValue('activate approved source');
    const channelMapping = { ...mapping, channel_id: 11, config: '{"existing":true}' };
    const subscriptionMapping = { ...mapping, subscription_account_id: 12, group_name: 'vip' };
    server.use(
      http.get('/api/user/authorization', () => HttpResponse.json({
        authorization_mode: 'iam', session: { activation_state: 'active' },
        permitted_operations: ['channel.model.list', 'channel.model.read', 'channel.model_mapping.read', 'channel.model_mapping.update'],
      })),
      http.get('/api/admin/models', () => HttpResponse.json({ models: [{ ...model, status: enabled ? 1 : 0 }], total: 1 })),
      http.get('/api/admin/models/519', () => HttpResponse.json({
        model: { ...model, status: enabled ? 1 : 0, authorization_revision: String(revision) },
        channel_mappings: kind === 'channel' ? [{ ...channelMapping, enabled }] : [],
        subscription_mappings: kind === 'subscription' ? [{ ...subscriptionMapping, enabled }] : [],
      })),
      http.get('/api/admin/models/519/usage-stats', () => HttpResponse.json({ stats: [], total: 0 })),
      http.post(`/api/admin/${kind === 'channel' ? 'channels/11' : 'subscription-accounts/12'}/models`, async ({ request }) => {
        body = await request.json() as Record<string, unknown>;
        if (body.expected_model_revision !== revision) return HttpResponse.json({}, { status: 409 });
        enabled = body.enabled === true;
        revision++;
        return HttpResponse.json({ success: true });
      }),
    );
    const user = userEvent.setup();
    function Shell() { useAuthorizationLifecycle(); return <AdminModelsPage />; }
    renderWithQuery(<MemoryRouter><Shell /></MemoryRouter>);
    const row = (await screen.findByText('deepseek-flash')).closest('tr')!;
    expect(within(row).getByText('已启用，暂无可用来源')).toBeInTheDocument();
    await user.click(within(row).getByRole('button', { name: '详情' }));
    const dialog = await screen.findByRole('dialog');
    await user.click(await within(dialog).findByRole('button', { name: '启用映射' }));
    await waitFor(() => expect(body).toMatchObject({
      model_pk: 519, enabled: true, priority: 3, upstream_model_id: 'deepseek-flash', expected_model_revision: 7, reason: 'activate approved source',
      ...(kind === 'channel' ? { config: channelMapping.config } : { group_name: 'vip' }),
    }));
    await waitFor(() => expect(within(row).queryByText('已启用，暂无可用来源')).not.toBeInTheDocument());
    expect(await within(dialog).findByRole('button', { name: '禁用映射' })).toBeEnabled();
    await user.click(within(dialog).getByRole('button', { name: '禁用映射' }));
    await waitFor(() => expect(body?.enabled).toBe(false));
    expect(body?.expected_model_revision).toBe(8);
    expect(await within(row).findByText('已启用，暂无可用来源')).toBeInTheDocument();
  });

  it('refreshes a conflicting revision without retrying the mapping write', async () => {
    let writes = 0;
    let reads = 0;
    server.use(
      http.get('/api/admin/models', () => HttpResponse.json({ models: [model], total: 1 })),
      http.get('/api/admin/models/519', () => {
        reads++;
        return HttpResponse.json({ model: { ...model, authorization_revision: String(6 + reads) }, channel_mappings: [{ ...mapping, channel_id: 11, config: '{}' }] });
      }),
      http.get('/api/admin/models/519/usage-stats', () => HttpResponse.json({ stats: [], total: 0 })),
      http.post('/api/admin/channels/11/models', () => {
        writes++;
        return HttpResponse.json({ message: 'revision conflict' }, { status: 409 });
      }),
    );
    const user = userEvent.setup();
    renderWithQuery(<MemoryRouter><AdminModelsPage /></MemoryRouter>);
    await user.click(await screen.findByRole('button', { name: '详情' }));
    const dialog = await screen.findByRole('dialog');
    await user.click(await within(dialog).findByRole('button', { name: '启用映射' }));
    await waitFor(() => expect(reads).toBe(2));
    expect(writes).toBe(1);
    expect(within(dialog).getByText('已启用，暂无可用来源')).toBeInTheDocument();
    expect(within(dialog).queryByRole('button', { name: '禁用映射' })).not.toBeInTheDocument();
  });

  it('hides mapping writes without update permission', async () => {
    server.use(
      http.get('/api/user/authorization', () => HttpResponse.json({
        authorization_mode: 'iam', session: { activation_state: 'active' },
        permitted_operations: ['channel.model.list', 'channel.model.read', 'channel.model_mapping.read'],
      })),
      http.get('/api/admin/models', () => HttpResponse.json({ models: [model], total: 1 })),
      http.get('/api/admin/models/519', () => HttpResponse.json({ model, channel_mappings: [{ ...mapping, channel_id: 11 }] })),
    );
    const user = userEvent.setup();
    renderWithQuery(<MemoryRouter><AdminModelsPage /></MemoryRouter>);
    await user.click(await screen.findByRole('button', { name: '详情' }));
    const dialog = await screen.findByRole('dialog');
    await within(dialog).findByText('11');
    expect(within(dialog).queryByRole('button', { name: '启用映射' })).not.toBeInTheDocument();
  });
});
