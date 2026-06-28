import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { AdminSubscriptionAccountsPage } from './SubscriptionAccountsPage';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';

// baseAccount mirrors the list-summary shape (camelCase). The detail endpoint
// (GET /:id) returns SubscriptionAccountInfo in snake_case; detailAccount below
// is used wherever toDraft() is exercised.
const baseAccount = {
  id: 1,
  name: 'claude-pro-1',
  platform: 'claude',
  accountType: 'oauth',
  status: 1,
  group: 'default',
  models: 'claude-sonnet-4-5',
  priority: 0,
  accountId: 'acct-123',
  expiresAt: 1800000000,
  updatedAt: 1700000000,
};

// detailAccount mirrors GET /api/subscription-accounts/{id} (snake_case JSON,
// nullable string fields as null) returned by common.v1.SubscriptionAccountInfo.
const detailAccount = {
  id: 1,
  name: 'claude-pro-1',
  platform: 'claude',
  account_type: 'oauth',
  status: 1,
  group: 'default',
  models: 'claude-sonnet-4-5',
  priority: 0,
  base_url: null,
  access_token: 'sk-******',
  refresh_token: 'rt-******',
  expires_at: 1800000000,
  account_id: 'acct-123',
  fingerprint: null,
  metadata: null,
  created_at: 1700000000,
  updated_at: 1700000000,
};

describe('AdminSubscriptionAccountsPage', () => {
  it('lists subscription accounts from the /api alias', async () => {
    server.use(
      http.get('/api/subscription-accounts', () =>
        HttpResponse.json({ accounts: [baseAccount], total: 1 }),
      ),
    );

    renderWithQuery(
      <MemoryRouter>
        <AdminSubscriptionAccountsPage />
      </MemoryRouter>,
    );

    const row = await screen.findByText('claude-pro-1');
    expect(row.closest('tr')?.textContent).toContain('Claude (Claude Code OAuth)');
  });

  it('sends platform + status filters to the list request', async () => {
    const listRequest = { url: null as URL | null };

    server.use(
      http.get('/api/subscription-accounts', ({ request }) => {
        listRequest.url = new URL(request.url);
        return HttpResponse.json({ accounts: [], total: 0 });
      }),
    );

    renderWithQuery(
      <MemoryRouter>
        <AdminSubscriptionAccountsPage />
      </MemoryRouter>,
    );

    await screen.findByText('暂无订阅账号');

    await userEvent.selectOptions(screen.getByLabelText('按平台筛选'), 'codex');
    await userEvent.selectOptions(screen.getByLabelText('按状态筛选'), '1');

    await waitFor(() => {
      expect(listRequest.url?.searchParams.get('platform')).toBe('codex');
      expect(listRequest.url?.searchParams.get('status')).toBe('1');
    });
  });

  it('creates an account with the expected payload', async () => {
    const user = userEvent.setup();
    const created: Record<string, unknown>[] = [];

    server.use(
      http.get('/api/subscription-accounts', () =>
        HttpResponse.json({ accounts: [], total: 0 }),
      ),
      http.post('/api/subscription-accounts', async ({ request }) => {
        const body = (await request.json()) as Record<string, unknown>;
        created.push(body);
        return HttpResponse.json({ success: true, message: '', account_id: 2 });
      }),
    );

    renderWithQuery(
      <MemoryRouter>
        <AdminSubscriptionAccountsPage />
      </MemoryRouter>,
    );

    await screen.findByText('暂无订阅账号');

    await user.click(screen.getByRole('button', { name: /新建订阅账号/i }));

    await user.type(screen.getByLabelText('名称'), 'codex-team');
    await user.type(screen.getByLabelText('Access Token'), 'sk-test-access');
    await user.type(screen.getByLabelText('Refresh Token'), 'rt-test-refresh');

    await user.click(screen.getByRole('button', { name: '创建' }));

    await waitFor(() => expect(created).toHaveLength(1));
    expect(created[0]).toMatchObject({
      name: 'codex-team',
      access_token: 'sk-test-access',
      refresh_token: 'rt-test-refresh',
      account_type: 'oauth',
    });
  });

  it('edits an account (loads detail, updates models) without trim() crash', async () => {
    const user = userEvent.setup();
    const updates: Array<Record<string, unknown>> = [];

    server.use(
      http.get('/api/subscription-accounts', () =>
        HttpResponse.json({ accounts: [baseAccount], total: 1 }),
      ),
      http.get('/api/subscription-accounts/:id', () =>
        HttpResponse.json(detailAccount),
      ),
      http.put('/api/subscription-accounts/:id', async ({ request }) => {
        updates.push((await request.json()) as Record<string, unknown>);
        return HttpResponse.json({ success: true, message: '' });
      }),
    );

    renderWithQuery(
      <MemoryRouter>
        <AdminSubscriptionAccountsPage />
      </MemoryRouter>,
    );

    const row = await screen.findByText('claude-pro-1');
    const actions = row.closest('tr')!;
    await user.click(within(actions).getByRole('button', { name: '编辑' }));

    const modelsInput = await screen.findByLabelText('模型（逗号分隔）');
    await user.clear(modelsInput);
    await user.type(modelsInput, 'claude-sonnet-4-5,claude-opus-4-1');

    await user.click(screen.getByRole('button', { name: '保存配置' }));

    await waitFor(() => expect(updates).toHaveLength(1));
    // The update payload must use snake_case keys and the models must be trimmed.
    expect(updates[0]).toMatchObject({
      account_type: 'oauth',
      base_url: '',
      account_id: 'acct-123',
      fingerprint: '',
      metadata: '',
      models: 'claude-sonnet-4-5,claude-opus-4-1',
    });
  });

  it('toggles account status via the /status endpoint', async () => {
    const user = userEvent.setup();
    const toggled: Array<{ id: number; status: number }> = [];

    server.use(
      http.get('/api/subscription-accounts', () =>
        HttpResponse.json({ accounts: [{ ...baseAccount, status: 1 }], total: 1 }),
      ),
      http.put('/api/subscription-accounts/:id/status', async ({ request, params }) => {
        const body = (await request.json()) as { status: number };
        toggled.push({ id: Number(params.id), status: body.status });
        return HttpResponse.json({ success: true, message: '' });
      }),
    );

    renderWithQuery(
      <MemoryRouter>
        <AdminSubscriptionAccountsPage />
      </MemoryRouter>,
    );

    const row = await screen.findByText('claude-pro-1');
    const actions = row.closest('tr')!;
    await user.click(within(actions).getByRole('button', { name: '停用' }));

    await waitFor(() => expect(toggled).toHaveLength(1));
    expect(toggled[0]).toEqual({ id: 1, status: 2 });
  });

  it('deletes an account after confirmation', async () => {
    const user = userEvent.setup();
    const deleted: number[] = [];

    server.use(
      http.get('/api/subscription-accounts', () =>
        HttpResponse.json({ accounts: [baseAccount], total: 1 }),
      ),
      http.delete('/api/subscription-accounts/:id', ({ params }) => {
        deleted.push(Number(params.id));
        return HttpResponse.json({ success: true, message: '' });
      }),
    );

    renderWithQuery(
      <MemoryRouter>
        <AdminSubscriptionAccountsPage />
      </MemoryRouter>,
    );

    const row = await screen.findByText('claude-pro-1');
    const actions = row.closest('tr')!;

    // confirm dialog
    const confirmSpy = vi.spyOn(window, 'confirm');
    confirmSpy.mockReturnValue(true);
    await user.click(within(actions).getByRole('button', { name: '删除' }));

    await waitFor(() => expect(deleted).toEqual([1]));
    confirmSpy.mockRestore();
  });
});
