import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { describe, expect, it, vi } from 'vitest';
import { ServiceTierPricingEditor } from './ServiceTierPricingEditor';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';

describe('ServiceTierPricingEditor', () => {
  it('inserts a disabled selected-model long-context example with the threshold marked unconfirmed', async () => {
    const user = userEvent.setup();
    renderWithQuery(<ServiceTierPricingEditor options={[{ key: 'ServiceTierPricing', value: '{"enabled":false}', revision: '8' }]} ready refresh={async () => []} />);
    const insert = await screen.findByRole('button', { name: '填入长上下文关闭示例' });
    await waitFor(() => expect(insert).toBeEnabled());
    await user.click(insert);
    const raw = screen.getByLabelText('档位价格 JSON') as HTMLTextAreaElement;
    expect(JSON.parse(raw.value)).toMatchObject({ enabled: false, version: 2, source_id: 11, model: 'deepseek-flash', long_context: { policy: 'whole_request_input_gt_v1' } });
    expect(screen.getByText(/128000 仅为未确认的示例阈值/)).toBeInTheDocument();
  });

  it('explains whole-request pricing and the unconfirmed threshold in English', async () => {
    window.localStorage.setItem('web:language', JSON.stringify('en-US'));
    renderWithQuery(<ServiceTierPricingEditor options={[{ key: 'ServiceTierPricing', value: '{"enabled":false}' }]} ready refresh={async () => []} />);
    expect(await screen.findByText('Service tier pricing')).toBeInTheDocument();
    expect(screen.getByText(/Only input strictly above threshold_tokens applies long-context prices to all five buckets/)).toBeInTheDocument();
    expect(screen.getByText(/128000 is an unconfirmed example threshold/)).toBeInTheDocument();
    expect(await screen.findByRole('button', { name: 'Insert disabled long-context example' })).toBeInTheDocument();
    expect(screen.getByText(/freeze the peak or off-peak band at reservation time/)).toBeInTheDocument();
  });
  it('inserts a disabled time-pricing example without supplying real prices', async () => {
    const user = userEvent.setup();
    renderWithQuery(<ServiceTierPricingEditor options={[{ key: 'ServiceTierPricing', value: '{"enabled":false}' }]} ready refresh={async () => []} />);
    const insert = await screen.findByRole('button', { name: '填入忙闲分时关闭示例' });
    await waitFor(() => expect(insert).toBeEnabled());
    await user.click(insert);
    expect(JSON.parse((screen.getByLabelText('档位价格 JSON') as HTMLTextAreaElement).value)).toMatchObject({ enabled: false, version: 3, source_id: 11, model: 'deepseek-flash', time_pricing: { timezone: 'Asia/Shanghai', peak_prices: { default: { input_price: null } } } });
    expect(screen.getByText(/忙闲时段示例均未确认/)).toBeInTheDocument();
  });
  it('writes one complete versioned object and waits for confirmed readback', async () => {
    const user = userEvent.setup();
    let payload: { values: Record<string, string>; expected_revisions: Record<string, string> } | undefined;
    let writes = 0;
    const refresh = vi.fn(async () => undefined);
    server.use(http.put('/api/option/', async ({ request }) => { payload = await request.json() as typeof payload; writes++; return HttpResponse.json({ success: true }); }));
    renderWithQuery(<ServiceTierPricingEditor options={[{ key: 'ServiceTierPricing', value: '{"enabled":false}', revision: '8' }]} ready refresh={refresh} />);
    const save = await screen.findByRole('button', { name: '保存档位价格' });
    await waitFor(() => expect(save).toBeEnabled());
    await user.click(save);
    await waitFor(() => expect(payload).toEqual({ values: { ServiceTierPricing: '{"enabled":false}' }, expected_revisions: { ServiceTierPricing: '8' } }));
    expect(writes).toBe(1);
    expect(save).toBeDisabled();
    expect(await screen.findByRole('button', { name: '刷新档位配置' })).toBeEnabled();
    await user.click(save);
    expect(writes).toBe(1);
  });

  it('does not expose procurement config without both read permissions', async () => {
    server.use(http.get('/api/user/authorization', () => HttpResponse.json({ authorization_mode: 'iam', session: { activation_state: 'active' }, permitted_operations: ['system.option.read', 'billing.pricing.read', 'billing.pricing.update'] })));
    renderWithQuery(<ServiceTierPricingEditor options={[{ key: 'ServiceTierPricing', value: '{"upstream_prices":"private-cost"}', revision: '8' }]} ready refresh={async () => []} />);
    expect(await screen.findByText('查看档位配置需要售价与采购成本的读取权限。')).toBeInTheDocument();
    expect(screen.queryByLabelText('档位价格 JSON')).not.toBeInTheDocument();
    expect(screen.queryByText(/private-cost/)).not.toBeInTheDocument();
  });

  it('shows an owner-scoped configuration as unreadable instead of disabled', async () => {
    renderWithQuery(<ServiceTierPricingEditor options={[{ key: 'ServiceTierPricing', value: '', restricted: true }]} ready refresh={async () => []} />);
    expect(await screen.findByText('查看档位配置需要售价与采购成本的读取权限。')).toBeInTheDocument();
    expect(screen.queryByLabelText('档位价格 JSON')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '保存档位价格' })).not.toBeInTheDocument();
  });
});
