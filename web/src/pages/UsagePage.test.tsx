import { fireEvent, screen } from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import { describe, expect, it } from 'vitest';
import { UsagePage } from './UsagePage';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';

describe('native usage v2 display', () => {
  it.each(['peak', 'off_peak'])('shows the frozen %s reservation band and its timezone', async band => {
    const timestamp = 1793235600;
    server.use(http.get('/api/user/logs', () => HttpResponse.json({ success: true, data: { logs: [{
      id: 'time-bill', type: 'consume', amount: -10000, balance_after: 10, created_at: timestamp + 7200,
      model_name: 'time-fixture', pricing_time_band: band, pricing_timezone: 'Asia/Shanghai', pricing_time_unix: timestamp,
    }], total: 1 } })));
    renderWithQuery(<UsagePage />);
    fireEvent.click(await screen.findByText('服务档位与计价依据'));
    expect(screen.getByText(`冻结计价时段：${band === 'peak' ? '忙时' : '闲时'}`)).toBeInTheDocument();
    expect(screen.getByText(`预扣计价时刻：${new Date(timestamp * 1000).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai' })}`)).toBeInTheDocument();
    expect(screen.getByText('计价时区：Asia/Shanghai')).toBeInTheDocument();
    expect(screen.getByText('按预扣时刻冻结时段，跨时段完成仍使用冻结价格。')).toBeInTheDocument();
  });
  it.each(['base', 'long'])('explains the recorded %s context tier with cached input included', async tier => {
    server.use(http.get('/api/user/logs', () => HttpResponse.json({ success: true, data: { logs: [{
      id: 'context-bill', type: 'consume', amount: -10000, balance_after: 10, created_at: 1,
      model_name: 'context-fixture', service_tier_policy: 'actual_or_max_v1',
      long_context_policy: 'whole_request_input_gt_v1', long_context_threshold: 128000,
      long_context_input_tokens: tier === 'base' ? 128000 : 128001, long_context_tier: tier,
    }], total: 1 } })));
    renderWithQuery(<UsagePage />);
    fireEvent.click(await screen.findByText('服务档位与计价依据'));
    expect(screen.getByText(`上下文计价档位：${tier === 'base' ? '基础上下文' : '长上下文'}`)).toBeInTheDocument();
    expect(screen.getByText(`实际输入总量（含缓存）：${tier === 'base' ? '128,000' : '128,001'}`)).toBeInTheDocument();
    expect(screen.getByText('长上下文阈值：128,000')).toBeInTheDocument();
    expect(screen.getByText(/实际输入总量严格大于阈值时/)).toBeInTheDocument();
  });
  it('explains an unknown actual tier from the recorded bill without substituting the request', async () => {
    server.use(http.get('/api/user/logs', () => HttpResponse.json({ success: true, data: { logs: [{
      id: 'tier-bill', type: 'consume', amount: -10000, balance_after: 10, created_at: 1,
      model_name: 'tier-fixture', requested_service_tier: 'priority', actual_service_tier: '',
      service_tier_status: 'missing', billed_service_tier: 'max', service_tier_policy: 'actual_or_max_v1', service_tier_config_hash: 'frozen-bill',
    }], total: 1 } })));
    renderWithQuery(<UsagePage />);
    const summary = await screen.findByText('服务档位与计价依据');
    fireEvent.click(summary);
    expect(screen.getByText('请求档位：priority')).toBeInTheDocument();
    expect(screen.getByText('上游实际档位：未知 · missing')).toBeInTheDocument();
    expect(screen.getByText('结算档位：逐桶最高价')).toBeInTheDocument();
    expect(screen.getByText(/frozen-bill/)).toBeInTheDocument();
    expect(screen.queryByText(/长上下文阈值/)).not.toBeInTheDocument();
    expect(screen.queryByText(/冻结计价时段/)).not.toBeInTheDocument();
  });
  for (const status of ['verified', 'missing', 'invalid', 'unsupported']) {
    it(`keeps raw totals separate for ${status} zero financial usage`, async () => {
      server.use(http.get('/api/user/logs', () => HttpResponse.json({ success: true, data: { logs: [{
        id: 'native-v2', type: 'consume', amount: 0, balance_after: 10, created_at: 1,
        model_name: 'native-v2-fixture', prompt_tokens: 42, reported_total_tokens: 42,
        billable_total_tokens: 0, usage_contract_version: 2, usage_parse_status: status,
      }], total: 1 } })));
      renderWithQuery(<UsagePage />);
      const row = (await screen.findByText('native-v2-fixture')).closest('tr');
      const detail = row?.querySelector('[tabindex="0"]');
      expect(detail).not.toBeNull();
      fireEvent.mouseEnter(detail!);
      expect(await screen.findByText('上游上报总 Token')).toBeInTheDocument();
    });
  }
});
