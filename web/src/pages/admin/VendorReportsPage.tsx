import { useMemo, useState } from 'react';
import { useAuthorizedQuery, useAuthorization } from '@/lib/authorization';
import { adminApiClient } from '@/lib/api';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { t } from '@/lib/i18n';
import { useQueryClient } from '@tanstack/react-query';
import { VendorReportImport } from './VendorReportImport';

interface Scope { provider: string; account: string; project?: string; upstream_model_id?: string; currency: string; unit: string; start: string; end: string; timezone?: string; granularity: string }
interface SourceMapping { source_kind: string; source_id: string | number; provider: string; account: string; project?: string; start: string; end: string; revision: string; evidence_id: string }
interface Attribution { category: string; amount: string; status: string; evidence_id: string; verification_scope: string }
interface Comparison { key: string; status: string; usage_status?: string; amount_status?: string; vendor_amount: string | null; estimated_amount: string | null; difference?: string | null; unexplained: string | null; unknown_amounts?: number; purpose_counts?: Record<string, number>; attributions?: Attribution[] }
interface FinalityAttestation { kind: string; evidence_id: string; confirmed_at: string; statement: string }
interface RetrospectiveTotals { records: number; amount: string }
interface RetrospectiveResult { revision: string; report_id: string; evidence_id: string; basis: string; provenance: string; execution_join_verified: boolean; owner_arithmetic_replayed: boolean; currency: string; unit: string; records: number; amount: string; supplier_amount: string; difference: string; arithmetic_status: string; by_purpose?: Record<string, RetrospectiveTotals> }
interface Report { id: string; manifest: { scope: Scope; version: string; evidence_id: string; supersedes?: string; complete?: boolean; pages_complete?: boolean; vendor_watermark?: string; local_watermark?: string; coverage_evidence_id?: string; mappings?: SourceMapping[]; absolute_tolerance?: string; relative_tolerance?: string; rounding_evidence_id?: string; finality_attestation?: FinalityAttestation | null }; retrospective_result?: RetrospectiveResult | null; status: string; cost_basis: string; verification_scope: string; correction_delta?: string | null; unknown_time_attempts?: number; unmapped_attempts?: number; internal_attempts?: number; user_reservations?: number; comparisons: Comparison[] }
interface ReviewItem { report_id: string; status: string; age_seconds: number; unknown_amounts: number; next_action: string }
interface Payload { reports?: Report[]; daily_coverage?: unknown[]; weekly_review?: { pending: number; attention: number; unknown_amounts: number; items: ReviewItem[] } }
function vendorMoney(value: string | null | undefined, scope: Pick<Scope, 'currency' | 'unit'>) { return value == null ? t('未知') : `${value} ${scope.currency}${scope.unit === 'currency' ? '' : ` (${scope.unit})`}`; }
const statusLabel = (value: string) => ({ pending: t('待补证'), attention: t('需复核'), matched: t('已核对'), variance: t('有差异'), unknown: t('未知'), not_applicable: t('不适用'), not_comparable: t('不可比较'), confirmed: t('已确认'), hypothesis: t('待验证') })[value] ?? value;
const scopeLabel = (value: string) => ({ none: t('尚未核验'), request: t('按请求核验'), account_period: t('按账号账期核验') })[value] ?? value;
const basisLabel = (value: string) => ({ provider_reported: t('供应商费用报告'), invoice: t('账单凭据'), tariff_estimate: t('费率估价'), monthly_allocation: t('月费分摊'), unknown: t('未知') })[value] ?? value;
const day = (ms: number) => new Date(ms).toISOString().slice(0, 10);
const actionLabel = (value: string) => ({ retain_evidence: t('保留证据'), complete_watermarks_or_late_evidence: t('补齐水位或迟到证据'), review_unexplained_difference: t('复核未解释差额'), resolve_attempt_scope: t('核对执行范围') })[value] ?? value;

export function VendorReportsPage() {
  const auth = useAuthorization();
  return <VendorReportsWorkspace key={auth.generation} auth={auth} />;
}

function VendorReportsWorkspace({ auth }: { auth: ReturnType<typeof useAuthorization> }) {
  const client = useQueryClient();
  const [start, setStart] = useState(() => day(Date.now() - 7 * 86400000));
  const [end, setEnd] = useState(() => day(Date.now() + 86400000));
  const [selected, setSelected] = useState<string | null>(null);
  const from = Date.parse(`${start}T00:00:00Z`) / 1000, to = Date.parse(`${end}T00:00:00Z`) / 1000;
  const valid = Number.isFinite(from) && Number.isFinite(to) && to > from && to - from <= 31 * 86400;
  const query = useAuthorizedQuery({ permission: 'billing.vendor_report.read', queryKey: ['vendor-reports', from, to], enabled: valid,
    queryFn: async () => (await adminApiClient.get<Payload>('/admin/vendor-reports', { params: { start: from, end: to, limit: 100 } })).data });
  const reports = useMemo(() => query.data?.reports ?? [], [query.data]);
  const detail = reports.find(r => r.id === selected);
  const review = query.data?.weekly_review;
  const exportJSON = () => {
    if (!auth.can('billing.vendor_report.read') || !query.data) return;
    const url = URL.createObjectURL(new Blob([JSON.stringify(query.data, null, 2)], { type: 'application/json' }));
    const a = document.createElement('a'); a.href = url; a.download = `vendor-review-${start}-${end}.json`; a.click(); URL.revokeObjectURL(url);
  };
  return <div className="space-y-5">
    <div><h2 className="text-2xl font-bold">{t('供应商费用核验')}</h2><p className="text-sm text-muted-foreground">{t('原币金额与用户收费分别核验；未知金额保留未知，账期总额不代表逐请求实扣。')}</p></div>
    <div className="flex flex-wrap items-end gap-3">
      <label>{t('导入窗口开始（UTC）')}<Input aria-label={t('导入窗口开始（UTC）')} type="date" value={start} onChange={e => setStart(e.target.value)} /></label>
      <label>{t('导入窗口结束（UTC，不含）')}<Input aria-label={t('导入窗口结束（UTC，不含）')} type="date" value={end} onChange={e => setEnd(e.target.value)} /></label>
      <Button disabled={!valid || query.isFetching} onClick={() => void query.refetch()}>{t('刷新')}</Button>
      <Button variant="outline" disabled={!auth.can('billing.vendor_report.read') || !query.data} onClick={exportJSON}>{t('导出复盘材料')}</Button>
    </div>
    <VendorReportImport auth={auth} refresh={async id => { await client.invalidateQueries({ queryKey: ['vendor-reports'] }); setSelected(id); }} />
    {!valid && <p role="alert">{t('请选择最长 31 天的有效固定窗口。')}</p>}
    {query.isError && <p role="alert">{t('读取失败，请检查权限或缩小窗口；当前覆盖不能确认为完整。')}</p>}
    {query.isLoading && <p>{t('加载中')}</p>}
    {review && <div className="rounded-lg border p-4">{t('待补证')}：{review.pending} · {t('需复核')}：{review.attention} · {t('未知金额记录')}：{review.unknown_amounts}</div>}
    <Table><TableHeader><TableRow>{['供应商 / 账号', '核验范围 / 账期', '状态 / 金额依据', '原币费用', '未解释差额', '操作'].map(v => <TableHead key={v}>{t(v)}</TableHead>)}</TableRow></TableHeader>
      <TableBody>{reports.map(r => <TableRow key={r.id}><TableCell>{r.manifest.scope.provider} / {r.manifest.scope.account}<div className="text-xs">{r.manifest.scope.project ?? ''}</div></TableCell>
        <TableCell>{scopeLabel(r.verification_scope)}<div className="text-xs">{r.manifest.scope.start} → {r.manifest.scope.end}</div></TableCell><TableCell>{statusLabel(r.status)} / {basisLabel(r.cost_basis)}</TableCell>
        <TableCell>{r.comparisons.map(c => <div key={c.key}>{vendorMoney(c.vendor_amount, r.manifest.scope)}</div>)}</TableCell><TableCell>{r.comparisons.map(c => <div key={c.key}>{vendorMoney(c.unexplained, r.manifest.scope)}</div>)}</TableCell>
        <TableCell><Button variant="outline" onClick={() => setSelected(r.id)}>{t('查看证据')}</Button></TableCell></TableRow>)}</TableBody></Table>
    {!query.isLoading && !query.isError && reports.length === 0 && <p>{t('此窗口暂无供应商报告，费用核验覆盖保持未知。')}</p>}
    {review?.items?.length ? <section className="rounded-lg border p-4"><h3 className="font-semibold">{t('周复盘待办')}</h3>{review.items.map(i => <div key={i.report_id} className="my-2 text-sm"><span className="font-mono">{i.report_id.slice(0, 12)}</span> · {statusLabel(i.status)} · {Math.floor(i.age_seconds / 3600)}h · {actionLabel(i.next_action)}</div>)}</section> : null}
    {detail && !query.isError && auth.displayCan('billing.vendor_report.read') && <VendorReportEvidence report={detail} review={review?.items.find(i => i.report_id === detail.id)} close={() => setSelected(null)} />}
  </div>;
}

const provided = (value: string | number | null | undefined) => value == null || value === '' || (typeof value === 'string' && value.startsWith('0001-01-01T')) ? t('未提供') : String(value);
const completeLabel = (value: boolean | null | undefined) => value == null ? t('未知') : value ? t('完整') : t('不完整');

function VendorReportEvidence({ report, review, close }: { report: Report; review?: ReviewItem; close: () => void }) {
  const m = report.manifest, scope = m.scope, finality = m.finality_attestation, retrospective = report.retrospective_result;
  const fields = [
    ['报告 ID', report.id], ['版本 / 证据引用', `${m.version} / ${m.evidence_id}`],
    ['核验状态', statusLabel(report.status)], ['核验范围', scopeLabel(report.verification_scope)],
    ['账期开始', scope.start], ['账期结束（不含）', scope.end], ['账期时区', provided(scope.timezone)],
    ['币种 / 单位', `${scope.currency} / ${scope.unit}`], ['上游模型', provided(scope.upstream_model_id)],
    ['报告完整性', completeLabel(m.complete)], ['分页完整性', completeLabel(m.pages_complete)],
    ['供应商水位', provided(m.vendor_watermark)], ['本地水位', provided(m.local_watermark)], ['覆盖证据', provided(m.coverage_evidence_id)],
    ['后续动作', review ? actionLabel(review.next_action) : t('未提供')],
    ['执行时间未知', provided(report.unknown_time_attempts)], ['未映射执行', provided(report.unmapped_attempts)],
    ['内部调用', provided(report.internal_attempts)], ['用户预留', provided(report.user_reservations)],
    ['绝对容差', vendorMoney(m.absolute_tolerance, scope)], ['相对容差', provided(m.relative_tolerance)], ['舍入证据', provided(m.rounding_evidence_id)],
    ['更正前序', provided(m.supersedes)], ['更正差额', vendorMoney(report.correction_delta, scope)],
  ];
  return <section aria-label={t('报告证据')} className="min-w-0 space-y-4 rounded-lg border p-4">
    <div className="flex items-center justify-between gap-3"><h3 className="font-semibold">{t('报告证据')}</h3><Button variant="ghost" onClick={close}>{t('关闭')}</Button></div>
    <p className="text-sm text-muted-foreground">{t('状态由服务端判定；证据引用不代表核验通过，账期核验不证明逐请求实扣。')}</p>
    <dl className="grid min-w-0 gap-3 text-sm sm:grid-cols-2 lg:grid-cols-3">{fields.map(([label, value]) => <div key={label} className="min-w-0"><dt className="text-muted-foreground">{t(label)}</dt><dd className="break-all">{value}</dd></div>)}</dl>
    {finality && <section aria-label={t('账期最终性依据')} className="min-w-0 space-y-3 rounded border p-3 text-sm">
      <h4 className="font-medium">{t('账期最终性依据')}</h4>
      <p className="text-muted-foreground">{t('维护者确认不代表供应商发布或认证，也不补足本地覆盖。')}</p>
      <dl className="grid min-w-0 gap-3 sm:grid-cols-2">{[
        ['确认类型', finality.kind === 'maintainer_confirmation' ? t('维护者确认') : provided(finality.kind)],
        ['确认内容', finality.statement === 'no_further_corrections' ? t('该账期不再更正') : provided(finality.statement)],
        ['确认时间', provided(finality.confirmed_at)], ['确认证据', provided(finality.evidence_id)],
      ].map(([label, value]) => <div key={label} className="min-w-0"><dt className="text-muted-foreground">{t(label)}</dt><dd className="break-all">{value}</dd></div>)}</dl>
    </section>}
    {retrospective && <section aria-label={t('归档补充重算')} className="min-w-0 space-y-3 rounded border p-3 text-sm">
      <h4 className="font-medium">{t('归档补充重算')}</h4>
      <p className="text-muted-foreground">{t('金额来自提交的归档材料；算术重放不认证原始文件或执行关联，也不改变主核验结论。')}</p>
      {retrospective.execution_join_verified === false && <p>{t('归档中的执行关联尚未独立核验。')}</p>}
      <dl className="grid min-w-0 gap-3 sm:grid-cols-2 lg:grid-cols-3">{[
        ['算术结论', ({ arithmetic_agrees: t('重算金额相符'), arithmetic_differs: t('重算金额有差异') })[retrospective.arithmetic_status] ?? provided(retrospective.arithmetic_status)],
        ['材料来源', retrospective.provenance === 'operator_submitted_archive' ? t('运营提交的归档') : provided(retrospective.provenance)],
        ['算术重放', retrospective.owner_arithmetic_replayed ? t('已重放') : t('未确认')],
        ['重算金额', vendorMoney(retrospective.amount, retrospective)], ['供应商金额', vendorMoney(retrospective.supplier_amount, retrospective)],
        ['差额（供应商减重算）', vendorMoney(retrospective.difference, retrospective)], ['币种 / 单位', `${retrospective.currency} / ${retrospective.unit}`],
        ['归档记录数', provided(retrospective.records)], ['重算版本', provided(retrospective.revision)], ['重算结果证据', provided(retrospective.evidence_id)],
      ].map(([label, value]) => <div key={label} className="min-w-0"><dt className="text-muted-foreground">{t(label)}</dt><dd className="break-all">{value}</dd></div>)}</dl>
      <div>{t('调用用途')}：{retrospective.by_purpose ? <ul>{Object.entries(retrospective.by_purpose).map(([purpose, total]) => <li key={purpose} className="break-all">{purpose}: {total.records} · {vendorMoney(total.amount, retrospective)}</li>)}</ul> : t('未提供')}</div>
    </section>}
    <h4 className="font-medium">{t('来源映射')}</h4>
    {m.mappings?.length ? <ul className="space-y-3 text-sm">{m.mappings.map((mapping, i) => <li key={i} className="min-w-0 break-all rounded border p-3">
      <div>{mapping.source_kind} / {mapping.source_id} → {mapping.provider} / {mapping.account}{mapping.project ? ` / ${mapping.project}` : ''}</div>
      <div>{t('生效范围')}：{mapping.start} → {mapping.end}</div>
      <div>{t('映射版本')}：{mapping.revision}</div><div>{t('映射证据')}：{mapping.evidence_id}</div>
    </li>)}</ul> : <p className="text-sm">{t('未提供来源映射，不能确认生产来源覆盖。')}</p>}
    <h4 className="font-medium">{t('比较与归因')}</h4>
    {report.comparisons.map(c => <div key={c.key} className="min-w-0 space-y-3 rounded border p-3 text-sm">
      <div className="break-all font-medium">{c.key} · {statusLabel(c.status)}</div>
      <dl className="grid min-w-0 gap-3 sm:grid-cols-2 lg:grid-cols-3">{[
        ['用量状态', statusLabel(c.usage_status ?? 'unknown')], ['金额状态', statusLabel(c.amount_status ?? 'unknown')],
        ['供应商金额', vendorMoney(c.vendor_amount, scope)], ['本地估价', vendorMoney(c.estimated_amount, scope)],
        ['差额', vendorMoney(c.difference, scope)], ['未解释差额', vendorMoney(c.unexplained, scope)], ['未知金额记录', provided(c.unknown_amounts)],
      ].map(([label, value]) => <div key={label} className="min-w-0"><dt className="text-muted-foreground">{t(label)}</dt><dd className="break-all">{value}</dd></div>)}</dl>
      <div>{t('调用用途')}：{c.purpose_counts ? Object.entries(c.purpose_counts).map(([purpose, count]) => `${purpose}: ${count}`).join(' · ') : t('未提供')}</div>
      {c.attributions?.length ? <ul className="space-y-2">{c.attributions.map((a, i) => <li key={i} className="break-all border-l-2 pl-3">
        <div>{a.category} · {statusLabel(a.status)} · {vendorMoney(a.amount, scope)} · {scopeLabel(a.verification_scope)}</div><div>{t('归因证据')}：{a.evidence_id}</div>
      </li>)}</ul> : <p>{t('未提供差异归因证据。')}</p>}
    </div>)}
  </section>;
}
