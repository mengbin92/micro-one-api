import { screen, within } from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import { describe, expect, it } from 'vitest';
import { PricingPage } from './PricingPage';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';

describe('PricingPage', () => {
  it('renders per-million prices and input/output modality icons', async () => {
    server.use(
      http.get('/api/pricing', () =>
        HttpResponse.json({
          success: true,
          message: '',
          data: {
            unit: '1M tokens',
            prices: [
              {
                model: 'step-3.7-flash',
                input_price: 0.2,
                output_price: 1.15,
                cache_read_price: 0.04,
                input_modalities: ['text', 'image', 'video'],
                output_modalities: ['text'],
              },
            ],
          },
        }),
      ),
    );

    renderWithQuery(<PricingPage />);

    const model = await screen.findByText('step-3.7-flash');
    const row = model.closest('tr');
    expect(within(row!).getAllByLabelText('文本')).toHaveLength(2);
    expect(within(row!).getByLabelText('图像')).toBeInTheDocument();
    expect(within(row!).getByLabelText('视频')).toBeInTheDocument();
    expect(row?.textContent).toContain('$0.20 / 1M tokens');
    expect(row?.textContent).toContain('$1.15 / 1M tokens');
    expect(row?.textContent).toContain('$0.04 / 1M tokens');
  });
});
