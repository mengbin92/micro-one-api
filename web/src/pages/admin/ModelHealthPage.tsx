import { useQuery } from '@tanstack/react-query';
import { Activity, AlertCircle, AlertTriangle, CheckCircle2, RefreshCw } from 'lucide-react';
import { useState } from 'react';
import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { EmptyState } from '@/components/EmptyState';
import { TableSkeleton } from '@/components/LoadingStates';
import { AdminPagination } from '@/components/admin/AdminPagination';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { listModelHealth, type ModelHealthState } from '@/lib/model-management';
import { cn } from '@/lib/utils';
import { locale, t } from '@/lib/i18n';

const healthMeta = {
  healthy: { label: '健康', icon: CheckCircle2, className: 'bg-emerald-100 text-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-300' },
  degraded: { label: '降级', icon: AlertTriangle, className: 'bg-amber-100 text-amber-700 dark:bg-amber-500/10 dark:text-amber-300' },
  unavailable: { label: '不可用', icon: AlertCircle, className: 'bg-red-100 text-red-700 dark:bg-red-500/10 dark:text-red-300' },
} as const;

function numberValue(value: string | number | undefined) {
  const parsed = Number(value ?? 0);
  return Number.isFinite(parsed) ? parsed : 0;
}

function successRate(state: ModelHealthState) {
  const requests = numberValue(state.request_count);
  return requests === 0 ? '—' : `${((numberValue(state.success_count) / requests) * 100).toFixed(1)}%`;
}

function formatUnix(value: string | number | undefined) {
  const timestamp = numberValue(value);
  return timestamp > 0 ? new Date(timestamp * 1000).toLocaleString(locale()) : '—';
}

function HealthBadge({ status }: { status: ModelHealthState['status'] }) {
  const meta = healthMeta[status] ?? healthMeta.degraded;
  const Icon = meta.icon;
  return <span className={cn('inline-flex items-center gap-1 rounded-full px-2 py-1 text-xs font-semibold', meta.className)}><Icon className="size-3.5" />{t(meta.label)}</span>;
}

export function ModelHealthPage() {
  const [keyword, setKeyword] = useState('');
  const [sourceKind, setSourceKind] = useState('');
  const [status, setStatus] = useState('');
  const [autoRefresh, setAutoRefresh] = useState(true);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(50);
  const query = useQuery({
    queryKey: ['admin-model-health', keyword, sourceKind, status, page, pageSize],
    queryFn: () => listModelHealth({ page, page_size: pageSize, keyword: keyword || undefined, source_kind: sourceKind || undefined, status: status || undefined }),
    refetchInterval: autoRefresh ? 30_000 : false,
  });
  const states = query.data?.states ?? [];
  const metrics = {
    total: query.data?.total ?? 0,
    healthy: states.filter((item) => item.status === 'healthy').length,
    degraded: states.filter((item) => item.status === 'degraded').length,
    unavailable: states.filter((item) => item.status === 'unavailable').length,
  };

  return <div className="space-y-6">
    <div className="flex flex-wrap items-start justify-between gap-3">
      <div><h1 className="text-2xl font-bold">{t('模型健康监控')}</h1><p className="mt-1 text-sm text-muted-foreground">{t('基于真实请求被动观测，不产生额外模型调用费用。')}</p></div>
      <div className="flex gap-2"><Button variant={autoRefresh ? 'default' : 'outline'} onClick={() => setAutoRefresh((value) => !value)}>{t(autoRefresh ? '自动刷新已开启' : '自动刷新已关闭')}</Button><Button variant="outline" onClick={() => query.refetch()} disabled={query.isFetching}><RefreshCw className={cn('size-4', query.isFetching && 'animate-spin')} />{t('刷新')}</Button></div>
    </div>
    <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
      {([['观测路由', metrics.total, Activity], ['本页健康', metrics.healthy, CheckCircle2], ['本页降级', metrics.degraded, AlertTriangle], ['本页不可用', metrics.unavailable, AlertCircle]] as const).map(([label, value, Icon]) => <Card key={label}><CardContent className="flex items-center justify-between p-5"><div><p className="text-sm text-muted-foreground">{t(label)}</p><p className="mt-1 text-3xl font-bold">{value}</p></div><Icon className="size-6 text-muted-foreground" /></CardContent></Card>)}
    </div>
    <Card><CardContent className="space-y-4 p-5">
      <div className="grid gap-3 md:grid-cols-[1fr_180px_180px]">
        <Input value={keyword} onChange={(event) => { setKeyword(event.target.value); setPage(1); }} placeholder={t('搜索模型或上游模型')} />
        <select className="h-10 rounded-md border bg-background px-3 text-sm" value={sourceKind} onChange={(event) => { setSourceKind(event.target.value); setPage(1); }}><option value="">{t('全部来源')}</option><option value="channel">{t('API 渠道')}</option><option value="subscription">{t('订阅账号')}</option></select>
        <select className="h-10 rounded-md border bg-background px-3 text-sm" value={status} onChange={(event) => { setStatus(event.target.value); setPage(1); }}><option value="">{t('全部状态')}</option><option value="healthy">{t('健康')}</option><option value="degraded">{t('降级')}</option><option value="unavailable">{t('不可用')}</option></select>
      </div>
      {query.isLoading ? <TableSkeleton columns={[t('状态'), t('模型'), t('上游来源'), t('成功率'), t('平均延迟'), t('连续失败'), t('最后观测'), t('最近错误')]} /> : states.length === 0 ? <EmptyState title={t('暂无模型健康数据')} description={t('模型完成真实请求后，健康状态会自动出现在这里。')} /> : <div className="overflow-x-auto rounded-lg border"><Table><TableHeader><TableRow><TableHead>{t('状态')}</TableHead><TableHead>{t('模型')}</TableHead><TableHead>{t('上游来源')}</TableHead><TableHead>{t('成功率')}</TableHead><TableHead>{t('平均延迟')}</TableHead><TableHead>{t('连续失败')}</TableHead><TableHead>{t('最后观测')}</TableHead><TableHead>{t('最近错误')}</TableHead></TableRow></TableHeader><TableBody>{states.map((state) => <TableRow key={state.id}><TableCell><HealthBadge status={state.status} /></TableCell><TableCell><div className="font-mono text-sm">{state.model_id}</div>{state.upstream_model_id !== state.model_id && <div className="text-xs text-muted-foreground">→ {state.upstream_model_id}</div>}</TableCell><TableCell>{t(state.source_kind === 'channel' ? 'API 渠道' : '订阅账号')} #{state.source_id}</TableCell><TableCell>{successRate(state)} <span className="text-xs text-muted-foreground">({state.success_count}/{state.request_count})</span></TableCell><TableCell>{numberValue(state.avg_latency_ms).toLocaleString()} ms</TableCell><TableCell>{state.consecutive_failures}</TableCell><TableCell>{formatUnix(state.last_checked_at)}</TableCell><TableCell className="max-w-80 truncate" title={state.last_error}>{state.last_error || '—'}</TableCell></TableRow>)}</TableBody></Table></div>}
      {!query.isLoading && states.length > 0 && <AdminPagination page={page} pageSize={pageSize} hasNextPage={page * pageSize < metrics.total} onPageChange={setPage} onPageSizeChange={(value) => { setPageSize(value); setPage(1); }} />}
    </CardContent></Card>
  </div>;
}

export default ModelHealthPage;
