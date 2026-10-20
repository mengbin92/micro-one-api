import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import { describe, expect, it } from 'vitest';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';
import { VendorReportsPage } from './VendorReportsPage';
import { useAuthorizationLifecycle } from '@/lib/authorization';
import { refreshAuthorization } from '@/lib/authorization-events';
import { vendorPayload, vendorReport } from '@/test/fixtures/vendor-report';

describe('supplier evidence reports', () => {
  it.each([['pending', '待补证'], ['attention', '需复核'], ['matched', '已核对']])('explains %s from owner evidence without changing its verdict', async (status, label) => {
    server.use(http.get('/api/admin/vendor-reports', () => HttpResponse.json({ ...vendorPayload, reports: [{ ...vendorReport, status }] })));
    renderWithQuery(<VendorReportsPage />);
    fireEvent.click(await screen.findByRole('button', { name: '查看证据' }));
    const detail = within(screen.getByRole('region', { name: '报告证据' }));
    expect(detail.getByText(label, { exact: true })).toBeInTheDocument();
    for (const text of ['不完整', '完整', 'coverage-proof', 'mapping-v1', vendorReport.manifest.mappings[0].evidence_id, 'previous-report', '-0.000000000000000001 USD', 'attribution-proof', '待验证', 'user: 4 · canary: 3', '补齐水位或迟到证据']) {
      expect(detail.getAllByText(text, { exact: false }).length).toBeGreaterThan(0);
    }
    expect(detail.getAllByText('0 USD', { exact: true }).length).toBeGreaterThan(0);
    expect(detail.getByText('报告完整性').nextElementSibling).toHaveTextContent(/^不完整$/);
    expect(detail.getByText('0.000000000000000001 USD', { exact: true })).toBeInTheDocument();
    expect(detail.getAllByText('未知').length).toBeGreaterThan(0);
  });
  it.each([undefined, null])('keeps absent completeness (%s) and zero-time watermarks unknown', async (absent) => {
    server.use(http.get('/api/admin/vendor-reports', () => HttpResponse.json({ reports: [{ ...vendorReport, manifest: { ...vendorReport.manifest, complete: absent, pages_complete: absent, vendor_watermark: '0001-01-01T00:00:00Z', local_watermark: absent, mappings: [] }, comparisons: [{ ...vendorReport.comparisons[0], purpose_counts: undefined, attributions: [] }] }] })));
    renderWithQuery(<VendorReportsPage />);
    fireEvent.click(await screen.findByRole('button', { name: '查看证据' }));
    const detail = within(screen.getByRole('region', { name: '报告证据' }));
    expect(detail.getAllByText('未知').length).toBeGreaterThanOrEqual(3);
    expect(detail.getAllByText('未提供').length).toBeGreaterThan(1);
    expect(detail.queryByText('0001-01-01T00:00:00Z')).not.toBeInTheDocument();
    expect(detail.getByText('未提供来源映射，不能确认生产来源覆盖。')).toBeInTheDocument();
    expect(detail.getByText('未提供差异归因证据。')).toBeInTheDocument();
  });
  it('removes selected evidence on revocation and never reopens old selection after a new grant', async () => {
    let allowed = true, requests = 0;
    server.use(http.get('/api/user/authorization', () => HttpResponse.json({ authorization_mode: 'iam', session: { activation_state: 'active' }, permitted_operations: ['admin.console.enter', ...(allowed ? ['billing.vendor_report.read'] : [])] })), http.get('/api/admin/vendor-reports', () => { requests++; return HttpResponse.json(vendorPayload); }));
    function Page() { useAuthorizationLifecycle(); return <VendorReportsPage />; }
    renderWithQuery(<Page />);
    fireEvent.click(await screen.findByRole('button', { name: '查看证据' }));
    expect(screen.getByRole('region', { name: '报告证据' })).toBeInTheDocument();
    allowed = false;
    act(() => refreshAuthorization());
    await waitFor(() => expect(screen.getByRole('button', { name: '导出复盘材料' })).toBeDisabled());
    expect(screen.queryByRole('region', { name: '报告证据' })).not.toBeInTheDocument();
    expect(screen.queryByText(vendorReport.manifest.evidence_id)).not.toBeInTheDocument();
    const deniedRequests = requests;
    fireEvent.click(screen.getByRole('button', { name: '刷新' }));
    expect(requests).toBe(deniedRequests);
    allowed = true;
    act(() => refreshAuthorization());
    await screen.findByRole('button', { name: '查看证据' });
    expect(screen.queryByRole('region', { name: '报告证据' })).not.toBeInTheDocument();
  });
  it.each(['0', '0.000000000000000001'])('displays %s exactly, keeps unknown separate and exposes weekly owner follow-ups', async (amount) => {
    server.use(http.get('/api/admin/vendor-reports', () => HttpResponse.json({ reports: [{ id: 'report-1', manifest: { version: 'original', evidence_id: 'original-page', scope: { provider: 'supplier', account: 'account', currency: 'USD', unit: 'currency', start: '2026-10-01', end: '2026-10-02', granularity: 'account_period' } }, status: 'pending', cost_basis: 'provider_reported', verification_scope: 'none', comparisons: [{ key: 'period', vendor_amount: amount, unexplained: null }] }], weekly_review: { pending: 1, attention: 0, unknown_amounts: 1, items: [{ report_id: 'report-1', status: 'pending', age_seconds: 3600, next_action: 'complete_watermarks_or_late_evidence' }] } })));
    renderWithQuery(<VendorReportsPage />);
    expect(await screen.findByText('supplier / account')).toBeInTheDocument();
    expect(screen.getByText(`${amount} USD`)).toBeInTheDocument();
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
});
