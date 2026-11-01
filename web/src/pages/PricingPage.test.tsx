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

describe('PricingPage time pricing', () => {
  it.each([true, false])('distinguishes tiers and context prices with time pricing %s', async timePricing => {
    const base = { input_price: 0.2, output_price: 1, cache_read_price: 0 };
    const tiers = (input: number) => ({ default: { ...base, input_price: input }, priority: { ...base, input_price: input + 0.1 }, flex: { ...base, input_price: input } });
    server.use(http.get('/api/pricing', () => HttpResponse.json({ success: true, data: { prices: [{
      model: 'deepseek-flash', service_tier_prices: tiers(0.2),
      long_context: { threshold_tokens: 128000, prices: tiers(0.4) },
      time_pricing: timePricing ? { timezone: 'Asia/Shanghai', calendar_valid_through: '2026-12-31', holidays: [], peak_windows: [{ weekdays: [7], start_minute: 0, end_minute: 1440 }], peak_prices: tiers(0.8), peak_long_context_prices: tiers(1.2) } : undefined,
    }] } })));
    renderWithQuery(<PricingPage />);
    const price = await screen.findByText(timePricing ? '$1.30 / 1M tokens' : '$0.50 / 1M tokens');
    const row = within(price.closest('tr')!);
    if (timePricing) expect(row.getByText('忙时')).toBeInTheDocument();
    else expect(row.queryByText('基础价格')).not.toBeInTheDocument();
    expect(row.getByText('priority')).toBeInTheDocument();
    expect(row.getByText('输入 > 128,000 tokens（整单）')).toBeInTheDocument();
    expect(screen.getAllByText('default / flex')).toHaveLength(timePricing ? 4 : 2);
    if (timePricing) expect(screen.getByText('00:00–24:00')).toBeInTheDocument();
    expect(screen.getAllByText('$0.00 / 1M tokens')).toHaveLength(timePricing ? 8 : 4);
  });

  it('shows a load failure instead of an empty price list', async () => {
    server.use(http.get('/api/pricing', () => new HttpResponse(null, { status: 503 })));
    renderWithQuery(<PricingPage />);
    expect(await screen.findByText('价格加载失败')).toBeInTheDocument();
    expect(screen.queryByText('暂无模型价格')).not.toBeInTheDocument();
  });

  it.each([null, '', ' ', false])('does not present invalid price %j as free', async inputPrice => {
    server.use(http.get('/api/pricing', () => HttpResponse.json({ success: true, data: { prices: [{ model: 'unpriced', input_price: inputPrice, output_price: 0 }] } })));
    renderWithQuery(<PricingPage />);
    const row = within((await screen.findByText('unpriced')).closest('tr')!);
    expect(row.getAllByText('-')).toHaveLength(2);
    expect(row.getByText('$0.00 / 1M tokens')).toBeInTheDocument();
  });

  it('shows both busy and idle prices with the configured calendar', async () => {
    const idle = { input_price: 0.2, output_price: 0.4, cache_read_price: 0.02 };
    const peak = { input_price: 0.4, output_price: 0.8, cache_read_price: 0.04 };
    server.use(http.get('/api/pricing', () => HttpResponse.json({ success: true, data: {
      unit: '1M tokens', prices: [{ model: 'deepseek-flash', ...idle,
        service_tier_prices: { default: idle, priority: idle, flex: idle },
        time_pricing: { timezone: 'Asia/Shanghai', calendar_valid_through: '2026-12-31', holidays: ['2026-10-01'], peak_windows: [{ weekdays: [1, 2, 3, 4, 5], start_minute: 540, end_minute: 1080 }], peak_prices: { default: peak, priority: peak, flex: peak } },
      }],
    } })));
    renderWithQuery(<PricingPage />);
    await screen.findByText('deepseek-flash');
    expect(screen.getByText('忙时')).toBeInTheDocument();
    expect(screen.getByText('闲时')).toBeInTheDocument();
    expect(screen.getByText('Asia/Shanghai')).toBeInTheDocument();
    expect(screen.getByText(/09:00–18:00/)).toBeInTheDocument();
    expect(screen.getByText('$0.80 / 1M tokens')).toBeInTheDocument();
    expect(screen.getByText('$0.02 / 1M tokens')).toBeInTheDocument();
    expect(screen.getByText(/2026-12-31/)).toBeInTheDocument();
    expect(screen.getByText(/2026-10-01/)).toBeInTheDocument();
  });
});
