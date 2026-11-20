import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { beforeEach, describe, expect, it } from 'vitest';
import { MemoryRouter } from 'react-router';
import { TokensPage } from './TokensPage';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';
import { takePlaygroundCredential } from '@/lib/playground-credential';
import { LanguageToggle } from '@/components/LanguageToggle';
import { SecurityVerificationDialog } from '@/components/SecurityVerificationDialog';

function renderTokensPage() {
  return renderWithQuery(
    <MemoryRouter>
      <TokensPage />
    </MemoryRouter>,
  );
}

describe('TokensPage', () => {
  it('keeps unavailable ordered candidates visible and reorders the actual IDs', async () => {
    const posted: Record<string, unknown>[] = [];
    server.use(
      http.get('/api/token', () => HttpResponse.json({ success: true, data: [{ id: 1, name: 'ordered key', status: 1, created_time: 1, routing_mode: 'ordered', routing_group_ids: [10, 20, 30], routing_revision: 1 }] })),
      http.get('/api/v1/routing-groups/available', () => HttpResponse.json({ success: true, data: { creation_enabled: true, default_available: true, facts: {}, groups: [20, 30].map((id) => ({ id, key: `group-${id}`, display_name: `Group ${id}`, price_ratio: 1, models: [], sources: [], ordered_eligible: true })), next_page_token: '' } })),
      http.patch('/api/v1/routing-tokens/1', async ({ request }) => {
        posted.push(await request.json() as Record<string, unknown>);
        return HttpResponse.json({ success: true });
      }),
    );
    renderTokensPage();
    await userEvent.click(await screen.findByRole('button', { name: '分组设置' }));
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText(/#10/)).toBeVisible();
    const up = within(dialog).getAllByRole('button', { name: '↑' });
    await userEvent.click(up[1]);
    await userEvent.click(within(dialog).getByRole('button', { name: '保存' }));
    await waitFor(() => expect(posted[0]?.routing_group_ids).toEqual([20, 10, 30]));
  });
  beforeEach(() => {
    server.use(
      http.get('/api/status', () => HttpResponse.json({ success: true, data: {} })),
      http.get('/api/pricing', () => HttpResponse.json({ success: true, data: { prices: [] } })),
    );
  });

  it('switches the page title and empty state together', async () => {
    server.use(
      http.get('/api/token', () => HttpResponse.json({ success: true, data: { items: [], total: 0 } })),
    );
    const user = userEvent.setup();

    renderWithQuery(
      <MemoryRouter>
        <LanguageToggle />
        <TokensPage />
      </MemoryRouter>,
    );

    expect(await screen.findByRole('heading', { name: 'API 密钥' })).toBeInTheDocument();
    expect(await screen.findByText('暂无 API 密钥')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: '切换至英文' }));

    expect(await screen.findByRole('heading', { name: 'API Keys' })).toBeInTheDocument();
    expect(screen.getByText('No API keys yet')).toBeInTheDocument();
    expect(document.body.textContent?.replaceAll('中文', '')).not.toMatch(/[\u3400-\u9fff]/);
  });

  it('does not show unnamed session tokens as API keys', async () => {
    server.use(
      http.get('/api/token', () =>
        HttpResponse.json({
          success: true,
          data: {
            items: [
              {
                id: 1,
                name: '',
                masked_key: 'sess********oken',
                status: 1,
                remain_quota: 1000,
                created_time: 1760000000,
              },
            ],
            total: 1,
          },
        }),
      ),
    );

    renderTokensPage();

    expect(await screen.findByText('暂无 API 密钥')).toBeInTheDocument();
    expect(screen.queryByText('sess********oken')).not.toBeInTheDocument();
  });

  it('shows saved key restrictions, expiry and exhausted quotas in the list', async () => {
    const future = Date.UTC(2030, 0, 2, 4, 34) / 1000;
    server.use(
      http.get('/api/v1/routing-groups/available', () => HttpResponse.json({ success: true, data: { default_available: true, groups: [] } })),
      http.get('/api/token', () => HttpResponse.json({ success: true, data: { items: [
      { id: 21, name: '受限测试', status: 1, routing_mode: 'fixed', routing_group_id: 2,
        masked_key: 'sk-s********ture', expired_time: future, remain_quota: 1536, used_quota: 512,
        unlimited_quota: false, models: ['model-vip', 'model-next'] },
      { id: 22, name: '过期测试', status: 1, expired_time: 1, remain_quota: 256, unlimited_quota: false },
      { id: 23, name: '额度耗尽测试', status: 4, remain_quota: 0, used_quota: 2048, unlimited_quota: false },
      { id: 24, name: '普通测试', status: 1, expired_time: -1, remain_quota: 99999, unlimited_quota: true },
    ] } })));

    renderTokensPage();

    const card = (name: string) => within(screen.getByRole('listitem', { name }));
    await screen.findByRole('listitem', { name: '受限测试' });
    expect(card('受限测试').getByText('到期时间（本地时间）')).toBeVisible();
    expect(card('受限测试').getByText(/2030/)).toBeVisible();
    expect(card('受限测试').getByText('剩余额度（Token）')).toBeVisible();
    expect(card('受限测试').getByText('1,536', { exact: true })).toBeVisible();
    expect(card('受限测试').getByText('已用额度（Token）')).toBeVisible();
    expect(card('受限测试').getByText('512', { exact: true })).toBeVisible();
    expect(card('受限测试').getByText('model-vip', { exact: true })).toBeVisible();
    expect(card('受限测试').getByText('model-next', { exact: true })).toBeVisible();
    expect(card('过期测试').getByText('已过期', { exact: true })).toBeVisible();
    expect(card('额度耗尽测试').getByText('额度已耗尽', { exact: true })).toBeVisible();
    expect(card('额度耗尽测试').getByText('0', { exact: true })).toBeVisible();
    expect(card('普通测试').getByText('不限额', { exact: true })).toBeVisible();
    expect(card('普通测试').queryByText('99,999')).not.toBeInTheDocument();
  });

  it('requires explicit confirmation to delete a key and translates the warning', async () => {
    let deletes = 0;
    server.use(
      http.get('/api/token', () => HttpResponse.json({ success: true, data: [{ id: 7, name: 'test key', status: 1, created_time: 1 }] })),
      http.delete('/api/token/7', () => { deletes++; return HttpResponse.json({ success: true }); }),
    );
    const user = userEvent.setup();
    renderWithQuery(<MemoryRouter><LanguageToggle /><TokensPage /></MemoryRouter>);
    await user.click(await screen.findByRole('button', { name: '删除' }));
    expect(deletes).toBe(0);
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: '取消' }));
    expect(deletes).toBe(0);
    await user.click(screen.getByRole('button', { name: '切换至英文' }));
    await user.click(await screen.findByRole('button', { name: 'Delete' }));
    const dialog = screen.getByRole('dialog');
    expect(within(dialog).getByText('Delete API key')).toBeVisible();
    expect(within(dialog).getByText('Requests using this key will no longer authenticate. This action cannot be undone.')).toBeVisible();
    expect(dialog.textContent).not.toMatch(/[\u3400-\u9fff]/);
    await user.click(within(dialog).getByRole('button', { name: 'Confirm deletion' }));
    await waitFor(() => expect(deletes).toBe(1));
  });

  it('shows the full API key only in the creation dialog', async () => {
    const user = userEvent.setup();
    const fullKey = 'sk-full-token-value-created-once';
    const maskedKey = 'sk-f************************once';
    let created = false;

    server.use(
      http.get('/api/token', () =>
        HttpResponse.json({
          success: true,
          data: {
            items: created
              ? [
                  {
                    id: 1,
                    name: 'test key',
                    masked_key: maskedKey,
                    status: 1,
                    remain_quota: 0,
                    created_time: 1760000000,
                  },
                ]
              : [],
            total: created ? 1 : 0,
          },
        }),
      ),
      http.post('/api/token', async () => {
        created = true;
        return HttpResponse.json({
          success: true,
          data: {
            id: 1,
            name: 'test key',
            key: fullKey,
            status: 1,
            remain_quota: 0,
            created_time: 1760000000,
          },
        });
      }),
    );

    renderTokensPage();

    await user.click(await screen.findByRole('button', { name: '创建 Token' }));
    await user.type(screen.getByLabelText('Token 名称'), 'test key');
    await user.click(screen.getByRole('button', { name: '创建' }));

    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByDisplayValue('test key')).toBeInTheDocument();
    expect(within(dialog).getByDisplayValue(fullKey)).toBeInTheDocument();

    await waitFor(() => {
      expect(screen.getByText(maskedKey)).toBeInTheDocument();
    });
    expect(screen.getByDisplayValue(fullKey)).toBeInTheDocument();

    await user.click(within(dialog).getByRole('button', { name: '在在线调试中使用' }));

    await waitFor(() => {
      expect(screen.queryByDisplayValue(fullKey)).not.toBeInTheDocument();
    });
    expect(takePlaygroundCredential()).toBe(fullKey);
    expect(screen.queryByText(fullKey)).not.toBeInTheDocument();
    expect(screen.getByText(maskedKey)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'CC Switch' })).not.toBeInTheDocument();
  });
});

describe('fixed routing keys', () => {
  const available = { success: true, data: { creation_enabled: true, default_available: false, next_page_token: '', facts: { default_routing_group_id: 1, revision: 3, public_group_access: 'explicit_only', grants: [] }, groups: [{ id: 2, key: 'vip', display_name: 'VIP', price_ratio: 0.8, price_source: 'user_routing_price_override', price_version: 'user_routing_price:2:9:1', billing_mode: 'subscription_first', subscription_covered: false, models: ['model-vip'], sources: [{ source_type: 'admin', source_ref: 'manual', expires_at: 0 }] }] } };

  beforeEach(() => {
    server.use(
      http.get('/api/status', () => HttpResponse.json({ success: true, data: {} })),
      http.get('/api/v1/routing-groups/available', () => HttpResponse.json(available)),
      http.get('/api/token', () => HttpResponse.json({ success: true, data: [] })),
      http.get('/api/pricing', () => HttpResponse.json({ success: true, data: { prices: [] } })),
    );
  });

  it('shows the invalid default and sends the selected fixed group', async () => {
    const user = userEvent.setup();
    let body: unknown;
    server.use(
      http.post('/api/v1/routing-tokens', async ({ request }) => { body = await request.json(); return HttpResponse.json({ success: true, data: { id: 20, key: 'sk-fixed-secret', name: 'vip-key', status: 1, routing_mode: 'fixed', routing_group_id: 2, routing_revision: 1 } }); }),
    );
    renderTokensPage();
    expect(await screen.findByRole('alert')).toHaveTextContent('默认分组失效');
    await user.click(screen.getByRole('button', { name: '创建 Token' }));
    expect(screen.queryByRole('checkbox', { name: '短期 Key' })).not.toBeInTheDocument();
    await user.type(screen.getByLabelText('Token 名称'), 'vip-key');
    expect(screen.getByRole('button', { name: '创建' })).toBeDisabled();
    const routingSelect = screen.getByLabelText('路由分组') as HTMLSelectElement;
    expect(routingSelect.textContent).toContain('VIP · ×0.8');
    expect(routingSelect.textContent).toContain('（用户专属）');
    await user.selectOptions(screen.getByLabelText('路由分组'), '2');
    expect(screen.getByText(/有效价格：×0.8/)).toBeInTheDocument();
    expect(screen.getByText(/价格来源：user_routing_price_override/)).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: '创建' }));
    await waitFor(() => expect(body).toEqual({ name: 'vip-key', routing_mode: 'fixed', routing_group_id: 2 }));
    expect(await screen.findByDisplayValue('sk-fixed-secret')).toBeInTheDocument();
  });

  it('requires valid restrictions and preserves the exact restricted request through step-up', async () => {
    const user = userEvent.setup();
    const expiry = '2030-01-02T12:34';
    const expireAt = new Date(expiry).getTime() / 1000;
    const bodies: unknown[] = [];
    let verified = false;
    localStorage.setItem('token', 'session-fixture');
    server.use(
      http.post('/api/v1/routing-tokens/restricted', async ({ request }) => {
        bodies.push(await request.json());
        if (!verified) return HttpResponse.json({ reason: 'STEP_UP_REQUIRED' }, { status: 403 });
        return HttpResponse.json({ success: true, data: {
          id: 22, name: 'short key', key: 'sk-short-fixture', status: 1, routing_mode: 'fixed', routing_group_id: 2,
          routing_revision: 1, expired_time: expireAt, remain_quota: 4096, unlimited_quota: false, models: ['model-vip'],
        } });
      }),
      http.get('/api/user/security', () => HttpResponse.json({ enabled: true })),
      http.post('/api/user/security/verify', async ({ request }) => {
        verified = request.headers.get('authorization') === 'Bearer session-fixture'
          && (await request.json() as { code: string }).code === '123456';
        return HttpResponse.json({}, { status: verified ? 200 : 400 });
      }),
    );
    renderWithQuery(<MemoryRouter><TokensPage /><SecurityVerificationDialog /></MemoryRouter>);
    await screen.findByRole('alert');
    await user.click(screen.getByRole('button', { name: '创建 Token' }));
    await user.selectOptions(screen.getByLabelText('路由分组'), '2');
    await user.type(screen.getByLabelText('Token 名称'), 'short key');
    await user.click(screen.getByRole('checkbox', { name: '短期 Key' }));

    const create = screen.getByRole('button', { name: '创建' });
    const expiryInput = screen.getByLabelText('到期时间（本地时间）');
    const quotaInput = screen.getByLabelText('配额（Token）');
    expect(create).toBeDisabled();
    expect(within(screen.getByLabelText('路由分组')).getByRole('option', { name: /跟随默认分组/ })).toBeDisabled();
    fireEvent.change(expiryInput, { target: { value: expiry } });
    fireEvent.change(quotaInput, { target: { value: '4096' } });
    expect(create).toBeDisabled();
    await user.click(screen.getByRole('checkbox', { name: 'model-vip' }));
    for (const invalidQuota of ['0', '1.5', '9007199254740992']) {
      fireEvent.change(quotaInput, { target: { value: invalidQuota } });
      expect(create).toBeDisabled();
    }
    fireEvent.change(quotaInput, { target: { value: '4096' } });
    fireEvent.change(expiryInput, { target: { value: '2000-01-01T00:00' } });
    expect(create).toBeDisabled();
    expect(bodies).toHaveLength(0);
    fireEvent.change(expiryInput, { target: { value: expiry } });
    await user.click(create);

    const stepUp = await screen.findByRole('dialog', { name: '安全验证' });
    await user.type(await within(stepUp).findByLabelText('验证码或恢复码'), '123456');
    await user.click(within(stepUp).getByRole('button', { name: '验证并继续' }));
    expect(await screen.findByDisplayValue('sk-short-fixture')).toBeInTheDocument();
    const expected = { name: 'short key', routing_mode: 'fixed', routing_group_id: 2,
      restrictions: { expire_at: expireAt, remain_quota: 4096, models: ['model-vip'] } };
    expect(bodies).toEqual([expected, expected]);
    const result = within(screen.getByRole('dialog', { name: 'Token 已创建' }));
    expect(result.getByText('4096')).toBeVisible();
    expect(result.getByText('model-vip')).toBeVisible();
  });

  it('clears selected models on group changes and resets the form on close', async () => {
    const user = userEvent.setup();
    const otherGroup = { ...available.data.groups[0], id: 3, display_name: 'Second VIP', models: ['model-vip', 'model-next'] };
    server.use(http.get('/api/v1/routing-groups/available', () => HttpResponse.json({
      ...available, data: { ...available.data, groups: [...available.data.groups, otherGroup] },
    })));
    renderTokensPage();
    await screen.findByRole('alert');
    const open = screen.getByRole('button', { name: '创建 Token' });
    await user.click(open);
    await user.selectOptions(screen.getByLabelText('路由分组'), '2');
    await user.click(screen.getByRole('checkbox', { name: '短期 Key' }));
    await user.click(screen.getByRole('checkbox', { name: 'model-vip' }));
    await user.type(screen.getByLabelText('配额（Token）'), '4096');
    await user.selectOptions(screen.getByLabelText('路由分组'), '3');
    expect(screen.getByRole('checkbox', { name: 'model-vip' })).not.toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'model-next' })).toBeInTheDocument();
    expect(screen.getByLabelText('配额（Token）')).toHaveValue(4096);

    await user.click(screen.getByRole('button', { name: '关闭' }));
    await user.click(open);
    expect(screen.getByLabelText('路由分组')).toHaveValue('inherit');
    await user.selectOptions(screen.getByLabelText('路由分组'), '2');
    expect(screen.getByRole('checkbox', { name: '短期 Key' })).not.toBeChecked();
    await user.click(screen.getByRole('checkbox', { name: '短期 Key' }));
    expect(screen.getByLabelText('配额（Token）')).toHaveValue(null);
    expect(screen.getByLabelText('到期时间（本地时间）')).toHaveValue('');
    expect(screen.getByRole('checkbox', { name: 'model-vip' })).not.toBeChecked();
  });
});
