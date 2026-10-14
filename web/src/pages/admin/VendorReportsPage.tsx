import { useMemo, useState } from 'react';
import { useAuthorizedQuery, useAuthorization } from '@/lib/authorization';
import { adminApiClient } from '@/lib/api';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { t } from '@/lib/i18n';

interface Scope { provider: string; account: string; project?: string; upstream_model_id?: string; currency: string; unit: string; start: string; end: string; granularity: string }
interface Comparison { key: string; status: string; vendor_amount: string | null; estimated_amount: string | null; unexplained: string | null; unknown_amounts: number }
interface Report { id: string; manifest: { scope: Scope; version: string; evidence_id: string; supersedes?: string }; status: string; cost_basis: string; verification_scope: string; correction_delta?: string; comparisons: Comparison[] }
interface ReviewItem { report_id: string; status: string; age_seconds: number; unknown_amounts: number; next_action: string }
interface Payload { reports?: Report[]; daily_coverage?: unknown[]; weekly_review?: { pending: number; attention: number; unknown_amounts: number; items: ReviewItem[] } }
export function vendorMoney(value: string | null | undefined, scope: Scope) { return value == null ? t('未知') : `${value} ${scope.currency}${scope.unit === 'currency' ? '' : ` (${scope.unit})`}`; }
const statusLabel = (value: string) => ({ pending: t('待补证'), attention: t('需复核'), matched: t('已核对'), variance: t('有差异'), unknown: t('未知'), not_applicable: t('不适用') })[value] ?? value;
const scopeLabel = (value: string) => ({ none: t('尚未核验'), request: t('按请求核验'), account_period: t('按账号账期核验') })[value] ?? value;
const basisLabel = (value: string) => ({ provider_reported: t('供应商费用报告'), invoice: t('账单凭据'), tariff_estimate: t('费率估价'), monthly_allocation: t('月费分摊'), unknown: t('未知') })[value] ?? value;
const day = (ms: number) => new Date(ms).toISOString().slice(0, 10);
const actionLabel = (value: string) => ({ retain_evidence: t('保留证据'), complete_watermarks_or_late_evidence: t('补齐水位或迟到证据'), review_unexplained_difference: t('复核未解释差额'), resolve_attempt_scope: t('核对执行范围') })[value] ?? value;

export function VendorReportsPage() {
  const [start, setStart] = useState(day(Date.now() - 7 * 86400000));
  const [end, setEnd] = useState(day(Date.now() + 86400000));
  const [selected, setSelected] = useState<string | null>(null);
  const auth = useAuthorization();
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
    {detail && <section className="rounded-lg border p-4"><div className="flex justify-between"><h3 className="font-semibold">{t('报告证据')}</h3><Button variant="ghost" onClick={() => setSelected(null)}>{t('关闭')}</Button></div><dl className="break-all text-sm"><dt>{t('报告 ID')}</dt><dd>{detail.id}</dd><dt>{t('版本 / 证据引用')}</dt><dd>{detail.manifest.version} / {detail.manifest.evidence_id}</dd><dt>{t('更正前序')}</dt><dd>{detail.manifest.supersedes ?? '-'}</dd></dl></section>}
  </div>;
}
