import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { AdminOverviewPage } from './OverviewPage';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';

function renderOverview() {
  return renderWithQuery(<MemoryRouter><AdminOverviewPage /></MemoryRouter>);
}

describe('AdminOverviewPage availability', () => {
  beforeEach(() => window.localStorage.setItem('web:language', JSON.stringify('zh-CN')));
  afterEach(() => window.localStorage.removeItem('web:language'));

  it('keeps healthy data and distinguishes unavailable channels and usage from empty results', async () => {
    server.use(http.get('/api/admin/summary', () => HttpResponse.json({ success: true, data: {
      partial: true,
      alerts_complete: false,
      sections: {
        channels: { available: false, reason: 'timeout' },
        usage_stats: { available: false, reason: 'unavailable' },
        top_users: { available: false, reason: 'unavailable' },
        users: { available: true },
      },
      totals: { users: 41, channels: null, request_count: null, quota_used: null },
      recent_users: [{ id: 42, username: 'healthy-user', email: 'healthy@example.com', status: 1 }],
      channels: null,
      top_users: null,
      cost_analysis: null,
      alerts: [],
    } })));

    renderOverview();
    expect(await screen.findByText('部分数据暂不可用')).toBeInTheDocument();
    expect(screen.getByText('healthy-user')).toBeInTheDocument();
    expect(screen.getByText('41')).toBeInTheDocument();
    expect(screen.getAllByText('数据暂不可用')).toHaveLength(2);
    expect(screen.queryByText('暂无渠道')).not.toBeInTheDocument();
    expect(screen.queryByText('暂无用户用量')).not.toBeInTheDocument();
    expect(screen.queryByText('运行正常，暂无告警')).not.toBeInTheDocument();
    expect(screen.getByText('告警数据不完整，暂时无法确认运行状态')).toBeInTheDocument();
    expect(screen.queryByText('0.0000')).not.toBeInTheDocument();
  });

  it('shows genuine successful empty data as empty and zero', async () => {
    server.use(http.get('/api/admin/summary', () => HttpResponse.json({ success: true, data: {
      partial: false, alerts_complete: true, channels: [], alerts: [],
      totals: { channels: 0, users: 0, request_count: 0 },
      sections: { channels: { available: true }, usage_stats: { available: true } },
    } })));
    renderOverview();
    expect(await screen.findByText('暂无渠道')).toBeInTheDocument();
    expect(screen.getByText('运行正常，暂无告警')).toBeInTheDocument();
    expect(screen.queryByText('部分数据暂不可用')).not.toBeInTheDocument();
    expect(screen.getAllByText('0.0000').length).toBeGreaterThan(0);
  });

  it('refreshes a partial response and clears the unavailable state after recovery', async () => {
    let calls = 0;
    server.use(http.get('/api/admin/summary', () => {
      calls++;
      return HttpResponse.json({ success: true, data: calls === 1 ? {
        partial: true, alerts_complete: false,
        sections: { channels: { available: false, reason: 'timeout' } }, channels: null,
      } : {
        partial: false, alerts_complete: true,
        sections: { channels: { available: true } }, channels: [],
      } });
    }));
    renderOverview();
    await screen.findByText('部分数据暂不可用');
    await userEvent.click(screen.getByRole('button', { name: '重试' }));
    expect(await screen.findByText('暂无渠道')).toBeInTheDocument();
    expect(screen.queryByText('部分数据暂不可用')).not.toBeInTheDocument();
    expect(calls).toBe(2);
  });

  it('shows a request failure with retry and no fabricated health or revenue', async () => {
    server.use(http.get('/api/admin/summary', () => new HttpResponse(null, { status: 503 })));
    renderOverview();
    expect(await screen.findByText('运营总览加载失败')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument();
    expect(screen.queryByText('运行正常，暂无告警')).not.toBeInTheDocument();
    expect(screen.queryByText('0.0000')).not.toBeInTheDocument();
  });
});
