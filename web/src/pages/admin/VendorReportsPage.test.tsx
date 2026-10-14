import { screen } from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import { describe, expect, it } from 'vitest';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';
import { VendorReportsPage, vendorMoney } from './VendorReportsPage';

describe('supplier evidence reports', () => {
  it('keeps decimal zero separate from unknown and exposes weekly owner follow-ups', async () => {
    server.use(http.get('/api/admin/vendor-reports', () => HttpResponse.json({ reports: [{ id: 'report-1', manifest: { version: 'original', evidence_id: 'original-page', scope: { provider: 'supplier', account: 'account', currency: 'USD', unit: 'currency', start: '2026-10-01', end: '2026-10-02', granularity: 'account_period' } }, status: 'pending', cost_basis: 'provider_reported', verification_scope: 'none', comparisons: [{ key: 'period', vendor_amount: '0', unexplained: null }] }], weekly_review: { pending: 1, attention: 0, unknown_amounts: 1, items: [{ report_id: 'report-1', status: 'pending', age_seconds: 3600, next_action: 'complete_watermarks_or_late_evidence' }] } })));
    renderWithQuery(<VendorReportsPage />);
    expect(await screen.findByText('supplier / account')).toBeInTheDocument();
    expect(screen.getByText('0 USD')).toBeInTheDocument();
    expect(screen.getByText('未知')).toBeInTheDocument();
    expect(screen.getByText('周复盘待办')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '导出复盘材料' })).toBeEnabled();
  });
  it('does not send supplier queries after read permission is denied', async () => {
    let requests = 0;
    server.use(http.get('/api/user/authorization', () => HttpResponse.json({ authorization_mode: 'iam', session: { activation_state: 'active' }, permitted_operations: ['admin.console.enter'] })), http.get('/api/admin/vendor-reports', () => { requests++; return HttpResponse.json({ reports: [] }); }));
    renderWithQuery(<VendorReportsPage />);
    expect(await screen.findByRole('button', { name: '导出复盘材料' })).toBeDisabled();
    expect(requests).toBe(0);
  });
  it('does not convert precise amounts through floating point', () => {
    const scope = { currency: 'USD', unit: 'currency', provider: '', account: '', start: '', end: '', granularity: '' };
    expect(vendorMoney('0.000000000000000001', scope)).toBe('0.000000000000000001 USD');
  });
});
