import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import { describe, expect, it } from 'vitest';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';
import { VendorReportsPage } from './VendorReportsPage';
import { useAuthorizationLifecycle } from '@/lib/authorization';
import { refreshAuthorization } from '@/lib/authorization-events';
import { vendorPayload, vendorReport } from '@/test/fixtures/vendor-report';

const finalityEvidence = 'sha256:' + 'd'.repeat(64);
const arithmeticEvidence = 'sha256:' + 'e'.repeat(64);
function supplementedReport(arithmeticStatus = 'arithmetic_agrees', difference = '0') {
  return {
    ...vendorReport, verification_scope: 'none',
    manifest: { ...vendorReport.manifest, complete: true, scope: { ...vendorReport.manifest.scope, currency: 'CNY' }, local_watermark: undefined, coverage_evidence_id: undefined,
      finality_attestation: { kind: 'maintainer_confirmation', evidence_id: finalityEvidence, confirmed_at: '2026-10-23T12:00:00+08:00', statement: 'no_further_corrections' },
    },
    comparisons: [{ ...vendorReport.comparisons[0], amount_status: 'unknown', estimated_amount: null, difference: null }],
    retrospective_result: {
      revision: 'retrospective-three-bucket-v1', report_id: vendorReport.id, evidence_id: arithmeticEvidence,
      basis: 'retrospective_tariff_recomputation', provenance: 'operator_submitted_archive', execution_join_verified: false, owner_arithmetic_replayed: true,
      currency: 'CNY', unit: 'currency', records: 203, amount: '2.047008960000000001', supplier_amount: arithmeticStatus === 'arithmetic_agrees' ? '2.047008960000000001' : '2.047008960000000000', difference, arithmetic_status: arithmeticStatus,
      by_purpose: { user: { records: 201, amount: '2.046960960000000001' }, internal_test: { records: 2, amount: '0.000048' } },
    },
  };
}

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
    expect(detail.queryByRole('region', { name: '账期最终性依据' })).not.toBeInTheDocument();
    expect(detail.queryByRole('region', { name: '归档补充重算' })).not.toBeInTheDocument();
  });
  it.each([['arithmetic_agrees', '重算金额相符', '0'], ['arithmetic_differs', '重算金额有差异', '-0.000000000000000001']])('separates %s archive arithmetic and maintainer finality from pending owner facts', async (status, label, difference) => {
    server.use(http.get('/api/admin/vendor-reports', () => HttpResponse.json({ ...vendorPayload, reports: [supplementedReport(status, difference)] })));
    renderWithQuery(<VendorReportsPage />);
    fireEvent.click(await screen.findByRole('button', { name: '查看证据' }));
    const detail = within(screen.getByRole('region', { name: '报告证据' }));
    expect(detail.getByText('核验状态').nextElementSibling).toHaveTextContent(/^待补证$/);
    expect(detail.getByText('核验范围').nextElementSibling).toHaveTextContent(/^尚未核验$/);
    expect(detail.getByText('本地估价').nextElementSibling).toHaveTextContent(/^未知$/);
    expect(detail.getByText('金额状态').nextElementSibling).toHaveTextContent(/^未知$/);
    expect(detail.getByText('覆盖证据').nextElementSibling).toHaveTextContent(/^未提供$/);
    expect(detail.getByText('本地水位').nextElementSibling).toHaveTextContent(/^未提供$/);
    const finality = within(detail.getByRole('region', { name: '账期最终性依据' }));
    expect(finality.getByText('维护者确认', { exact: true })).toBeInTheDocument();
    expect(finality.getByText('该账期不再更正')).toBeInTheDocument();
    expect(finality.getByText('2026-10-23T12:00:00+08:00')).toBeInTheDocument();
    expect(finality.getByText(finalityEvidence)).toBeInTheDocument();
    expect(finality.getByText('维护者确认不代表供应商发布或认证，也不补足本地覆盖。')).toBeInTheDocument();
    const arithmetic = within(detail.getByRole('region', { name: '归档补充重算' }));
    expect(arithmetic.getByText(label)).toBeInTheDocument();
    expect(arithmetic.getByText('重算金额').nextElementSibling).toHaveTextContent(/^2\.047008960000000001 CNY$/);
    expect(arithmetic.getByText('差额（供应商减重算）').nextElementSibling).toHaveTextContent(`${difference} CNY`);
    expect(arithmetic.getByText('币种 / 单位').nextElementSibling).toHaveTextContent(/^CNY \/ currency$/);
    expect(arithmetic.getByText('归档记录数').nextElementSibling).toHaveTextContent(/^203$/);
    expect(arithmetic.getByText('user: 201 · 2.046960960000000001 CNY')).toBeInTheDocument();
    expect(arithmetic.getByText('internal_test: 2 · 0.000048 CNY')).toBeInTheDocument();
    expect(arithmetic.getByText('运营提交的归档')).toBeInTheDocument();
    expect(arithmetic.getByText('归档中的执行关联尚未独立核验。')).toBeInTheDocument();
    expect(arithmetic.getByText('金额来自提交的归档材料；算术重放不认证原始文件或执行关联，也不改变主核验结论。')).toBeInTheDocument();
    expect(arithmetic.getByText(arithmeticEvidence)).toBeInTheDocument();
  });
  it('translates supplementary provenance and maintainer confirmation without supplier certification', async () => {
    window.localStorage.setItem('web:language', JSON.stringify('en-US'));
    server.use(http.get('/api/admin/vendor-reports', () => HttpResponse.json({ reports: [supplementedReport()] })));
    renderWithQuery(<VendorReportsPage />);
    fireEvent.click(await screen.findByRole('button', { name: 'View evidence' }));
    const finality = within(screen.getByRole('region', { name: 'Period finality basis' }));
    expect(finality.getByText('Maintainer confirmation', { exact: true })).toBeInTheDocument();
    expect(finality.getByText('Maintainer confirmation is not published or certified by the supplier and does not establish local coverage.')).toBeInTheDocument();
    const arithmetic = within(screen.getByRole('region', { name: 'Supplementary archive arithmetic' }));
    expect(arithmetic.getByText('Operator-submitted archive')).toBeInTheDocument();
    expect(arithmetic.getByText('Recomputed amount agrees')).toBeInTheDocument();
    expect(arithmetic.getByText('Execution joins in the archive have not been independently verified.')).toBeInTheDocument();
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
    server.use(http.get('/api/user/authorization', () => HttpResponse.json({ authorization_mode: 'iam', session: { activation_state: 'active' }, permitted_operations: ['admin.console.enter', ...(allowed ? ['billing.vendor_report.read'] : [])] })), http.get('/api/admin/vendor-reports', () => { requests++; return HttpResponse.json({ ...vendorPayload, reports: [supplementedReport()] }); }));
    function Page() { useAuthorizationLifecycle(); return <VendorReportsPage />; }
    renderWithQuery(<Page />);
    fireEvent.click(await screen.findByRole('button', { name: '查看证据' }));
    expect(screen.getByRole('region', { name: '报告证据' })).toBeInTheDocument();
    expect(screen.getByText(arithmeticEvidence)).toBeInTheDocument();
    allowed = false;
    act(() => refreshAuthorization());
    await waitFor(() => expect(screen.getByRole('button', { name: '导出复盘材料' })).toBeDisabled());
    expect(screen.queryByRole('region', { name: '报告证据' })).not.toBeInTheDocument();
    expect(screen.queryByText(vendorReport.manifest.evidence_id)).not.toBeInTheDocument();
    expect(screen.queryByText(arithmeticEvidence)).not.toBeInTheDocument();
    expect(screen.queryByText(finalityEvidence)).not.toBeInTheDocument();
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
