import { useMemo, useState } from 'react';
import { RefreshCw } from 'lucide-react';
import { EmptyState } from '@/components/EmptyState';
import { TableSkeleton } from '@/components/LoadingStates';
import { AdminPagination } from '@/components/admin/AdminPagination';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { adminApiClient } from '@/lib/api';
import { getApiErrorMessage } from '@/lib/api-error';
import { unwrapApiData } from '@/lib/api-response';
import { useAuthorizedQuery } from '@/lib/authorization';
import { locale, t } from '@/lib/i18n';

interface OperationAudit {
  event_id: string;
  actor_user_id: number;
  actor_username?: string;
  operation: string;
  resource_id: string;
  decision_versions: string;
  reason: string;
  occurred_at: number;
  result: 'success' | 'failure' | string;
}

interface OperationAuditPage {
  items?: OperationAudit[];
  total?: number;
  page?: number;
  page_size?: number;
}

const MINUTE_MS = 60_000;

function datetimeLocalToMillis(value: string) {
  if (!value) return 0;
  const millis = new Date(value).getTime();
  return Number.isFinite(millis) ? millis : 0;
}

// A datetime-local value without seconds resolves to :00.000, but the backend
// filters with an inclusive `occurred_at <= end_time_ms`. Extend it to the end
// of that minute so selecting 10:00 does not silently drop 10:00:30 records.
function datetimeLocalEndMillis(value: string) {
  const millis = datetimeLocalToMillis(value);
  if (millis <= 0) return 0;
  return value.length === 16 ? millis + MINUTE_MS - 1 : millis + 999;
}

function formatTime(value: number) {
  return value > 0 ? new Date(value).toLocaleString(locale()) : '-';
}

// Username is resolved server-side on a best-effort basis; keep the numeric id
// visible as a fallback for deleted actors or callers without user-read access.
function actorLabel(item: OperationAudit) {
  return item.actor_username || `#${item.actor_user_id}`;
}

function prettyVersions(value: string) {
  if (!value) return '-';
  try {
    return JSON.stringify(JSON.parse(value), null, 2);
  } catch {
    return value;
  }
}

export function OperationAuditsPage() {
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [actorUserID, setActorUserID] = useState('');
  const [operation, setOperation] = useState('');
  const [resourceID, setResourceID] = useState('');
  const [result, setResult] = useState('');
  const [startTime, setStartTime] = useState('');
  const [endTime, setEndTime] = useState('');
  const [selected, setSelected] = useState<OperationAudit>();
  const startTimeMS = datetimeLocalToMillis(startTime);
  const rawEndTimeMS = datetimeLocalToMillis(endTime);
  const endTimeMS = datetimeLocalEndMillis(endTime);
  const invalidRange = startTimeMS > 0 && rawEndTimeMS > 0 && startTimeMS > rawEndTimeMS;
  const invalidActor = actorUserID !== '' && (!/^[1-9]\d*$/.test(actorUserID) || !Number.isSafeInteger(Number(actorUserID)));

  const query = useAuthorizedQuery<OperationAuditPage>({
    permission: 'iam.audit.read',
    queryKey: ['admin-operation-audits', page, pageSize, actorUserID, operation, resourceID, result, startTimeMS, endTimeMS],
    enabled: !invalidRange && !invalidActor,
    queryFn: async ({ signal }) => {
      const params: Record<string, string | number> = { page, page_size: pageSize };
      if (actorUserID) params.actor_user_id = actorUserID;
      if (operation.trim()) params.operation = operation.trim();
      if (resourceID.trim()) params.resource_id = resourceID.trim();
      if (result) params.result = result;
      if (startTimeMS) params.start_time_ms = startTimeMS;
      if (endTimeMS) params.end_time_ms = endTimeMS;
      const response = await adminApiClient.get('/v1/admin/operation-audits', { params, signal });
      return unwrapApiData<OperationAuditPage>(response.data);
    },
  });

  const items = useMemo(() => query.data?.items ?? [], [query.data?.items]);
  const total = query.data?.total ?? 0;
  const hasNextPage = page * pageSize < total;
  const resetFilters = () => {
    setPage(1);
    setActorUserID('');
    setOperation('');
    setResourceID('');
    setResult('');
    setStartTime('');
    setEndTime('');
  };

  return <div className="space-y-6">
    <div className="flex flex-wrap items-start justify-between gap-3">
      <div>
        <h2 className="text-2xl font-bold">{t('操作审计')}</h2>
        <p className="mt-2 text-sm text-muted-foreground">{t('记录管理端业务写入的操作人、资源、结果和决策版本；不保存请求正文或凭据。')}</p>
      </div>
      <Button variant="outline" onClick={() => void query.refetch()} disabled={query.isFetching || invalidRange || invalidActor}>
        <RefreshCw className={query.isFetching ? 'size-4 animate-spin' : 'size-4'} />{t('刷新')}
      </Button>
    </div>

    <Card className="grid gap-4 p-4 sm:grid-cols-2 xl:grid-cols-4">
      <label className="space-y-2 text-sm"><span className="font-medium">{t('操作人用户 ID')}</span><Input inputMode="numeric" value={actorUserID} onChange={event => { setActorUserID(event.target.value.trim()); setPage(1); }} placeholder={t('全部操作人')} /></label>
      <label className="space-y-2 text-sm"><span className="font-medium">{t('操作代码')}</span><Input value={operation} onChange={event => { setOperation(event.target.value); setPage(1); }} placeholder={t('例如 billing.pricing.update')} maxLength={160} /></label>
      <label className="space-y-2 text-sm"><span className="font-medium">{t('资源 ID')}</span><Input value={resourceID} onChange={event => { setResourceID(event.target.value); setPage(1); }} placeholder={t('精确匹配')} maxLength={128} /></label>
      <label className="space-y-2 text-sm"><span className="font-medium">{t('结果')}</span><select className="h-10 w-full rounded-md border bg-background px-3 text-sm" value={result} onChange={event => { setResult(event.target.value); setPage(1); }}><option value="">{t('全部结果')}</option><option value="success">{t('成功')}</option><option value="failure">{t('失败')}</option></select></label>
      <label className="space-y-2 text-sm"><span className="font-medium">{t('开始时间')}</span><Input type="datetime-local" value={startTime} onChange={event => { setStartTime(event.target.value); setPage(1); }} /></label>
      <label className="space-y-2 text-sm"><span className="font-medium">{t('结束时间')}</span><Input type="datetime-local" value={endTime} onChange={event => { setEndTime(event.target.value); setPage(1); }} /></label>
      <div className="flex items-end xl:col-span-2"><Button variant="outline" onClick={resetFilters}>{t('重置筛选')}</Button></div>
    </Card>

    {invalidRange && <p role="alert" className="text-sm text-destructive">{t('开始时间不能晚于结束时间。')}</p>}
    {invalidActor && <p role="alert" className="text-sm text-destructive">{t('操作人用户 ID 必须是正整数。')}</p>}
    {query.isRestricted && <p role="alert" className="text-sm text-destructive">{t('没有操作审计查看权限。')}</p>}
    {query.isError && !query.isRestricted && <div role="alert" className="flex flex-wrap items-center gap-3 text-sm text-destructive"><span>{getApiErrorMessage(query.error, t('操作审计加载失败。'))}</span><Button variant="outline" size="sm" onClick={() => void query.refetch()}>{t('重试')}</Button></div>}
    {query.isLoading ? <div role="status" aria-label={t('加载中...')}><TableSkeleton columns={[t('时间'), t('操作人'), t('操作代码'), t('资源 ID'), t('结果'), t('详情')]} /></div> : query.data && items.length === 0 ? <EmptyState title={t('暂无操作审计记录')} /> : query.data && <div className="overflow-x-auto rounded-lg border">
      <Table>
        <TableHeader><TableRow><TableHead>{t('时间')}</TableHead><TableHead>{t('操作人')}</TableHead><TableHead>{t('操作代码')}</TableHead><TableHead>{t('资源 ID')}</TableHead><TableHead>{t('结果')}</TableHead><TableHead className="text-right">{t('详情')}</TableHead></TableRow></TableHeader>
        <TableBody>{items.map(item => <TableRow key={item.event_id}>
          <TableCell className="whitespace-nowrap text-muted-foreground">{formatTime(item.occurred_at)}</TableCell>
          <TableCell className="font-mono" title={`#${item.actor_user_id}`}>{actorLabel(item)}</TableCell>
          <TableCell className="font-mono text-xs sm:text-sm">{item.operation}</TableCell>
          <TableCell className="font-mono">{item.resource_id}</TableCell>
          <TableCell><span className={item.result === 'success' ? 'rounded-full bg-emerald-50 px-2 py-1 text-xs font-medium text-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-300' : 'rounded-full bg-red-50 px-2 py-1 text-xs font-medium text-red-700 dark:bg-red-500/10 dark:text-red-300'}>{item.result === 'success' ? t('成功') : t('失败')}</span></TableCell>
          <TableCell className="text-right"><Button variant="outline" size="sm" onClick={() => setSelected(item)}>{t('查看')}</Button></TableCell>
        </TableRow>)}</TableBody>
      </Table>
    </div>}
    {query.data && <div className="flex flex-wrap items-center justify-between gap-3 text-sm text-muted-foreground"><span>{t('共 {count} 条', { count: total })}</span></div>}
    {query.data && <AdminPagination page={page} pageSize={pageSize} hasNextPage={hasNextPage} onPageChange={setPage} onPageSizeChange={size => { setPageSize(size); setPage(1); }} />}

    <Dialog open={!!selected} onOpenChange={open => { if (!open) setSelected(undefined); }}>
      <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader><DialogTitle>{t('操作审计详情')}</DialogTitle></DialogHeader>
        {selected && <div className="space-y-4 text-sm">
          <dl className="grid gap-3 sm:grid-cols-2">
            <div><dt className="text-muted-foreground">{t('事件 ID')}</dt><dd className="break-all font-mono">{selected.event_id}</dd></div>
            <div><dt className="text-muted-foreground">{t('时间')}</dt><dd>{formatTime(selected.occurred_at)}</dd></div>
            <div><dt className="text-muted-foreground">{t('操作人')}</dt><dd className="font-mono" title={`#${selected.actor_user_id}`}>{actorLabel(selected)}</dd></div>
            <div><dt className="text-muted-foreground">{t('结果')}</dt><dd>{selected.result === 'success' ? t('成功') : t('失败')}</dd></div>
            <div><dt className="text-muted-foreground">{t('操作代码')}</dt><dd className="break-all font-mono">{selected.operation}</dd></div>
            <div><dt className="text-muted-foreground">{t('资源 ID')}</dt><dd className="break-all font-mono">{selected.resource_id}</dd></div>
          </dl>
          <div><p className="mb-2 text-muted-foreground">{t('操作原因')}</p><p className="rounded-lg border bg-muted/30 p-3 whitespace-pre-wrap">{selected.reason || t('未填写')}</p></div>
          <div><p className="mb-2 text-muted-foreground">{t('决策版本')}</p><pre className="max-h-64 overflow-auto rounded-lg border bg-muted p-3 text-xs">{prettyVersions(selected.decision_versions)}</pre></div>
        </div>}
      </DialogContent>
    </Dialog>
  </div>;
}
