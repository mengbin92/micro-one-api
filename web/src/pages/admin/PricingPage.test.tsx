import { act, screen, waitFor, within } from '@testing-library/react';
import { useQueryClient, type QueryClient } from '@tanstack/react-query';
import userEvent from '@testing-library/user-event';
import { delay, http, HttpResponse } from 'msw';
import { describe, expect, it, vi } from 'vitest';
import { AdminPricingPage } from './PricingPage';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';
import { useAuthorizationLifecycle } from '@/lib/authorization';
import { PricingPage } from '../PricingPage';

describe('AdminPricingPage', () => {
  it('keeps the draft versions when a background read sees another writer', async () => {
    const user = userEvent.setup();
    let revision = '1';
    let client!: QueryClient;
    let sent: Record<string, string> | undefined;
    let calls = 0;
    server.use(
      http.get('/api/option/', () => HttpResponse.json({ success: true, data: [
        { key: 'ModelPrice', value: '{"a":{"input_price":0.000001}}', revision },
        { key: 'ModelRatio', value: '{}', revision }, { key: 'CompletionRatio', value: '{}', revision },
      ] })),
      http.get('/api/admin/models', () => HttpResponse.json({ models: [], total: 0 })),
      http.put('/api/option/', async ({ request }) => {
        calls++; sent = (await request.json() as { expected_revisions: Record<string, string> }).expected_revisions;
        return HttpResponse.json({ error: 'revision conflict' }, { status: 409 });
      }),
    );
    function Shell() { client = useQueryClient(); return <AdminPricingPage />; }
    renderWithQuery(<Shell />);
    await screen.findByDisplayValue('a');
    const input = screen.getAllByRole('spinbutton')[0];
    await user.clear(input); await user.type(input, '3');
    revision = '2';
    await act(async () => { await client.invalidateQueries({ queryKey: ['admin-options'] }); });
    expect(input).toHaveValue(3);
    await user.click(screen.getByRole('button', { name: '保存价格' }));
    await waitFor(() => expect(sent).toEqual({ ModelPrice: '1', ModelRatio: '1', CompletionRatio: '1' }));
    expect(calls).toBe(1);
    expect(screen.getByRole('button', { name: '保存价格' })).toBeDisabled();
  });

  it('blocks more writes after a committed save until its readback succeeds', async () => {
    const user = userEvent.setup();
    const values: Record<string, string> = { ModelPrice: '{"a":{"input_price":0.000001}}', ModelRatio: '{}', CompletionRatio: '{}' };
    let writes = 0;
    let allowReadback = false;
    server.use(
      http.get('/api/option/', () => writes && !allowReadback ? HttpResponse.json({ error: 'read unavailable' }, { status: 503 }) : HttpResponse.json({ success: true, data: Object.entries(values).map(([key, value]) => ({ key, value, revision: writes ? '2' : '1' })) })),
      http.get('/api/admin/models', () => HttpResponse.json({ models: [], total: 0 })),
      http.put('/api/option/', async ({ request }) => { writes++; Object.assign(values, (await request.json() as { values: Record<string, string> }).values); return HttpResponse.json({ success: true }); }),
    );
    renderWithQuery(<AdminPricingPage />);
    await screen.findByDisplayValue('a');
    const save = screen.getByRole('button', { name: '保存价格' });
    await user.click(save);
    const refresh = await screen.findByRole('button', { name: '刷新' });
    await waitFor(() => expect(refresh).toBeEnabled());
    expect(save).toBeDisabled();
    await user.click(save);
    expect(writes).toBe(1);
    allowReadback = true;
    await user.click(refresh);
    await waitFor(() => expect(save).toBeEnabled());
    expect(writes).toBe(1);
  });

  it('does not leave partial prices when one configuration write fails', async () => {
    const user = userEvent.setup();
    const saved = new Map<string, string>();
    let calls = 0;
    server.use(
      http.get('/api/option/', () => HttpResponse.json({ success: true, data: ['ModelPrice', 'ModelRatio', 'CompletionRatio'].map(key => ({ key, value: '{}', revision: '1' })) })),
      http.get('/api/admin/models', () => HttpResponse.json({ models: [{ id: 1, model_id: 'a', status: 1, is_public: true, pricing_input: 1, pricing_output: 2 }], total: 1 })),
      http.put('/api/option/', async ({ request }) => {
        calls++;
        const body = await request.json() as { key?: string; value: string; values?: Record<string, string> };
        if (body.values || body.key === 'ModelRatio') return HttpResponse.json({ error: 'storage unavailable' }, { status: 500 });
        saved.set(body.key!, body.value);
        return HttpResponse.json({ success: true });
      }),
    );
    renderWithQuery(<AdminPricingPage />);
    await screen.findByDisplayValue('a');
    const save = screen.getByRole('button', { name: '保存价格' });
    await user.click(save);
    await waitFor(() => expect(calls).toBeGreaterThan(0));
    await waitFor(() => expect(screen.getByRole('button', { name: '刷新' })).toBeEnabled());
    expect(save).toBeDisabled();
    expect(saved.size).toBe(0);
    expect(calls).toBe(1);
  });

  it.each(['pending', 'failed', 'restricted'])('cannot replace pricing maps when option reads are %s', async (state) => {
    const user = userEvent.setup();
    let writes = 0;
    let reads = 0;
    server.use(
      http.get('/api/user/authorization', () => HttpResponse.json({
        authorization_mode: 'iam', session: { activation_state: 'active' },
        permitted_operations: ['system.option.update', 'system.option.pricing.update', 'billing.pricing.update', 'channel.model.list', ...(state === 'restricted' ? [] : ['system.option.read'])],
      })),
      http.get('/api/option/', async () => {
        reads++;
        if (state === 'pending') await delay('infinite');
        return HttpResponse.json({ message: 'options unavailable' }, { status: 500 });
      }),
      http.get('/api/admin/models', () => HttpResponse.json({ models: [], total: 0 })),
      http.put('/api/option/', () => { writes++; return HttpResponse.json({ success: true }); }),
    );
    vi.spyOn(window, 'prompt').mockReturnValue('调整价格');
    function Shell() { useAuthorizationLifecycle(); return <AdminPricingPage />; }
    renderWithQuery(<Shell />);
    const save = await screen.findByRole('button', { name: '保存价格' });
    if (state === 'failed') await screen.findByText('暂无模型价格');
    if (state === 'pending') await waitFor(() => expect(reads).toBe(1));
    await user.click(save);
    expect(writes).toBe(0);
    expect(save).toBeDisabled();
  });

  it('cannot start an empty draft before existing prices arrive', async () => {
    const user = userEvent.setup();
    let resolveOptions!: (response: Response) => void;
    const options = new Promise<Response>(resolve => { resolveOptions = resolve; });
    let reads = 0;
    server.use(
      http.get('/api/option/', () => { reads++; return options; }),
      http.get('/api/admin/models', () => HttpResponse.json({ models: [], total: 0 })),
    );
    renderWithQuery(<AdminPricingPage />);
    await waitFor(() => expect(reads).toBe(1));
    const add = screen.getByRole('button', { name: '添加模型' });
    await user.click(add);
    resolveOptions(HttpResponse.json({ success: true, data: [{ key: 'ModelPrice', value: JSON.stringify({ 'existing-model': { input_price: 0.000001 } }) }] }));
    expect(await screen.findByDisplayValue('existing-model')).toBeInTheDocument();
  });

  it.each(['new model listing', '新模型上架测试', '', null])('handles save reason %j once and only publishes confirmed prices', async (reason) => {
    const user = userEvent.setup();
    const saved = new Map<string, string>();
    const prompt = vi.spyOn(window, 'prompt').mockReturnValue(reason);
    const options = ['ModelPrice', 'ModelRatio', 'CompletionRatio'].map(key => ({ key, value: '{}', revision: '1' }));
    server.use(
      http.get('/api/user/authorization', () => HttpResponse.json({
        authorization_mode: 'iam', session: { activation_state: 'active' },
        permitted_operations: ['system.option.read', 'system.option.update', 'system.option.pricing.update', 'billing.pricing.update', 'channel.model.list'],
      })),
      http.get('/api/option/', () => HttpResponse.json({ success: true, data: options.map(o => ({ ...o, value: saved.get(o.key) ?? o.value })) })),
      http.get('/api/admin/models', () => HttpResponse.json({ models: [{ id: 519, model_id: 'deepseek-flash', status: 1, is_public: true, pricing_input: 0.3, pricing_output: 1.2, pricing_cache_read: 0.06 }], total: 1 })),
      http.get('/api/pricing', () => HttpResponse.json({ success: true, data: { prices: saved.has('ModelPrice') ? [{ model: 'deepseek-flash', input_price: 0.3, output_price: 1.2, cache_read_price: 0.06 }] : [] } })),
      http.put('/api/option/', async ({ request }) => {
        const body = await request.json() as { values: Record<string, string>; expected_revisions: Record<string, unknown>; reason: string };
        if (Object.values(body.expected_revisions).some(v => typeof v !== 'string')) {
          return HttpResponse.json({ error: 'invalid request body' }, { status: 400 });
        }
        expect(body.expected_revisions).toEqual({ ModelPrice: '1', ModelRatio: '1', CompletionRatio: '1' });
        expect(body.reason).toBe(reason);
        Object.entries(body.values).forEach(([key, value]) => saved.set(key, value));
        return HttpResponse.json({ success: true });
      }),
    );
    function Shell() { useAuthorizationLifecycle(); return <><AdminPricingPage /><PricingPage /></>; }
    renderWithQuery(<Shell />);
    await screen.findByDisplayValue('deepseek-flash');
    await screen.findByText('暂无模型价格');
    await user.click(screen.getByRole('button', { name: '保存价格' }));
    expect(prompt).toHaveBeenCalledTimes(1);
    if (!reason) {
      await waitFor(() => expect(screen.getByRole('button', { name: '保存价格' })).toBeEnabled());
      expect(saved.size).toBe(0);
      return;
    }
    await waitFor(() => expect(JSON.parse(saved.get('ModelPrice') ?? '{}')).toEqual({
      'deepseek-flash': { input_price: 3e-7, output_price: 1.2e-6, cache_read_price: 6e-8 },
    }));
    const row = (await screen.findByText('deepseek-flash')).closest('tr')!;
    expect(within(row).getByText('$0.30 / 1M tokens')).toBeInTheDocument();
    expect(within(row).getByText('$1.20 / 1M tokens')).toBeInTheDocument();
    expect(within(row).getByText('$0.06 / 1M tokens')).toBeInTheDocument();
  });

  it('lists canonical public model IDs once without creating prices', async () => {
    server.use(
      http.get('/api/option/', () => HttpResponse.json({ success: true, data: [] })),
      http.get('/api/admin/models', () =>
        HttpResponse.json({
          models: [
            { id: 1, model_id: 'GLM-5.2', display_name: 'GLM 5.2', status: 1, is_public: true },
            { id: 2, model_id: 'glm-5.2', display_name: 'GLM 5.2 Subscription', status: 1, is_public: true },
            { id: 3, model_id: 'MiniMax-M3', display_name: 'MiniMax M3', status: 1, is_public: true },
            { id: 4, model_id: 'z-ai/glm-5.2', display_name: 'NVIDIA GLM', status: 2, is_public: false },
          ],
          total: 4,
        }),
      ),
    );

    renderWithQuery(<AdminPricingPage />);

    expect(await screen.findByDisplayValue('glm-5.2')).toBeInTheDocument();
    expect(screen.getByDisplayValue('minimax-m3')).toBeInTheDocument();
    expect(screen.getAllByDisplayValue('glm-5.2')).toHaveLength(1);
    expect(screen.queryByDisplayValue('z-ai/glm-5.2')).not.toBeInTheDocument();
  });

  it('saves historical mixed-case prices under the canonical model ID', async () => {
    const user = userEvent.setup();
    const saved = new Map<string, string>();
    server.use(
      http.get('/api/option/', () =>
        HttpResponse.json({
          success: true,
          data: [
            {
              key: 'ModelPrice',
              value: saved.get('ModelPrice') ?? JSON.stringify({ 'GLM-5.2': { input_price: 0.000001, output_price: 0.000002 } }),
            },
            { key: 'ModelRatio', value: saved.get('ModelRatio') ?? '{}' },
            { key: 'CompletionRatio', value: saved.get('CompletionRatio') ?? '{}' },
          ],
        }),
      ),
      http.get('/api/admin/models', () =>
        HttpResponse.json({
          models: [
            { id: 1, model_id: 'glm-5.2', display_name: 'GLM 5.2', status: 1, is_public: true },
            { id: 2, model_id: 'minimax-m3', display_name: 'MiniMax M3', status: 1, is_public: true },
          ],
          total: 2,
        }),
      ),
      http.put('/api/option/', async ({ request }) => {
        const body = (await request.json()) as { values: Record<string, string> };
        Object.entries(body.values).forEach(([key, value]) => saved.set(key, value));
        return HttpResponse.json({ success: true });
      }),
    );

    renderWithQuery(<AdminPricingPage />);

    expect(await screen.findByDisplayValue('glm-5.2')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: '保存价格' }));

    await waitFor(() => {
      expect(JSON.parse(saved.get('ModelPrice') ?? '{}')).toEqual({
        'glm-5.2': { input_price: 0.000001, output_price: 0.000002 },
      });
    });
  });

  it('shows registry prices, availability, and input/output modalities', async () => {
    server.use(
      http.get('/api/option/', () =>
        HttpResponse.json({
          success: true,
          data: [
            { key: 'ModelRatio', value: JSON.stringify({ 'step-3.7-flash': 0.5 }) },
            { key: 'CompletionRatio', value: JSON.stringify({ 'step-3.7-flash': 2 }) },
            { key: 'AmountPerUnit', value: '10000' },
          ],
        }),
      ),
      http.get('/api/admin/models', () =>
        HttpResponse.json({
          models: [
            {
              id: 1,
              model_id: 'step-3.7-flash',
              display_name: 'Step 3.7 Flash',
              status: 1,
              is_public: true,
              pricing_input: 1,
              pricing_output: 4,
              pricing_cache_read: 0.25,
              input_modalities: ['text', 'image'],
              output_modalities: ['text'],
            },
            {
              id: 2,
              model_id: 'step-explore',
              display_name: 'Step Explore',
              status: 0,
              is_public: true,
              input_modalities: ['text'],
              output_modalities: ['text'],
            },
          ],
          total: 2,
        }),
      ),
    );

    renderWithQuery(<AdminPricingPage />);

    const enabled = await screen.findByDisplayValue('step-3.7-flash');
    const enabledRow = enabled.closest('tr');
    expect(enabledRow?.textContent).toContain('启用');
    // Modalities render as OpenRouter-style icons with accessible labels.
    expect(within(enabledRow!).getAllByLabelText('文本')).toHaveLength(2);
    expect(within(enabledRow!).getByLabelText('图像')).toBeInTheDocument();
    // Registry prices are per 1M tokens and prefill input/output/cache-read.
    expect(within(enabledRow!).getByDisplayValue('1')).toBeInTheDocument();
    expect(within(enabledRow!).getByDisplayValue('4')).toBeInTheDocument();
    expect(within(enabledRow!).getByDisplayValue('0.25')).toBeInTheDocument();

    const disabledRow = screen.getByDisplayValue('step-explore').closest('tr');
    expect(disabledRow?.textContent).toContain('禁用');
  });
});
