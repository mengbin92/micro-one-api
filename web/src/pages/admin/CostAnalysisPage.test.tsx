import { screen } from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import { describe, expect, it } from 'vitest';
import { CostAnalysisPage } from './CostAnalysisPage';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';

describe('cost summary section restrictions', () => {
  it('shows a restriction instead of zero or cached totals when an owner rejects a section', async () => {
    server.use(http.get('/api/admin/summary', () => HttpResponse.json({ success: true, data: { sections: { ledger: { available: false, reason: 'restricted' } }, totals: { upstream_cost: 1234 }, cost_analysis: { revenue_quota: 100 } } })));
    renderWithQuery(<CostAnalysisPage />);
    await screen.findByText('权限受限');
    expect(screen.queryByText('暂无成本数据')).not.toBeInTheDocument();
    expect(screen.queryByText('总收入')).not.toBeInTheDocument();
  });
  it('distinguishes owner unavailability from absent cost data', async () => {
    server.use(http.get('/api/admin/summary', () => HttpResponse.json({ success: true, data: { sections: { ledger: { available: false, reason: 'unavailable' } } } })));
    renderWithQuery(<CostAnalysisPage />);
    await screen.findByText('数据暂不可用');
    expect(screen.queryByText('暂无成本数据')).not.toBeInTheDocument();
  });
});
