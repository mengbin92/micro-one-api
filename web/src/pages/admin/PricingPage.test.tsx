import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { describe, expect, it } from 'vitest';
import { AdminPricingPage } from './PricingPage';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';

describe('AdminPricingPage', () => {
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
              value: JSON.stringify({ 'GLM-5.2': { input_price: 0.000001, output_price: 0.000002 } }),
            },
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
        const body = (await request.json()) as { key: string; value: string };
        saved.set(body.key, body.value);
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
