import { useQuery } from '@tanstack/react-query';
import { GitBranch, Search } from 'lucide-react';
import { useState } from 'react';
import type { FormEvent } from 'react';
import { adminApiClient } from '@/lib/api';
import { unwrapApiData } from '@/lib/api-response';
import { locale, t } from '@/lib/i18n';
import { formatUSD } from '@/lib/amount';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';

interface SelectionEvent {
  id: number;
  planned?: boolean;
  result?: string;
  model?: string;
  source_kind?: string;
  source_id?: number;
  candidate_kinds?: string[];
  selection_reason?: string;
  fallback?: boolean;
  fallback_reason?: string;
  elapsed_ms?: number;
  created_at?: number;
}

interface RequestAttempt {
  requestId?: string;
  request_id?: string;
  attemptNumber?: number;
  attempt_number?: number;
  reservationId?: string;
  reservation_id?: string;
  status?: string;
  sourceKind?: string;
  source_kind?: string;
  upstreamModelId?: string;
  upstream_model_id?: string;
  actualCost?: number;
  actual_cost?: number;
}

interface RoutingAudit {
  events?: SelectionEvent[];
  attempts?: RequestAttempt[];
  attempt_total?: number;
  retention?: string;
}

function unixTime(value?: number) {
  return value ? new Date(value * 1000).toLocaleString(locale()) : '-';
}

export function RoutingAuditDialog({ defaultUserID = '' }: { defaultUserID?: string }) {
  const [open, setOpen] = useState(false);
  const [userID, setUserID] = useState(defaultUserID);
  const [rootID, setRootID] = useState('');
  const [query, setQuery] = useState<{ userID: string; rootID: string } | null>(null);
  const [page, setPage] = useState(1);

  const audit = useQuery({
    queryKey: ['routing-audit', query?.userID, query?.rootID, page],
    enabled: open && query !== null,
    queryFn: async () => unwrapApiData<RoutingAudit>((await adminApiClient.get('/log/routing-audit', {
      params: { user_id: query?.userID, root_request_id: query?.rootID, page, page_size: 100 },
    })).data),
  });

  const submit = (event: FormEvent) => {
    event.preventDefault();
    const nextUserID = userID.trim();
    const nextRootID = rootID.trim();
    if (!/^[1-9]\d*$/.test(nextUserID) || !nextRootID || nextRootID.length > 128) return;
    setPage(1);
    if (query?.userID === nextUserID && query.rootID === nextRootID && page === 1) {
      void audit.refetch();
      return;
    }
    setQuery({ userID: nextUserID, rootID: nextRootID });
  };

  const events = audit.data?.events ?? [];
  const attempts = audit.data?.attempts ?? [];

  return (
    <>
      <Button type="button" variant="outline" onClick={() => { if (defaultUserID) setUserID(defaultUserID); setOpen(true); }}>
        <GitBranch className="size-4" />{t('选路审计')}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-5xl">
          <DialogHeader>
            <DialogTitle>{t('选路审计')}</DialogTitle>
            <DialogDescription>{audit.data?.retention ? t(audit.data.retention) : t('选路与结算审计记录')}</DialogDescription>
          </DialogHeader>
          <form className="grid gap-3 sm:grid-cols-[12rem_1fr_auto] sm:items-end" onSubmit={submit}>
            <div className="space-y-1.5">
              <Label htmlFor="routing-audit-user">{t('用户 ID')}</Label>
              <Input id="routing-audit-user" required pattern="[1-9][0-9]*" value={userID} onChange={(event) => setUserID(event.target.value)} />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="routing-audit-root">{t('根请求 ID')}</Label>
              <Input id="routing-audit-root" required maxLength={128} value={rootID} onChange={(event) => setRootID(event.target.value)} />
            </div>
            <Button type="submit" disabled={!userID.trim() || !rootID.trim() || audit.isFetching}>
              <Search className="size-4" />{t('查询')}
            </Button>
          </form>

          {audit.isError && <p role="alert" className="text-sm text-destructive">{t('选路审计查询失败')}</p>}
          {query && !audit.isFetching && !audit.isError && (
            <div className="space-y-5">
              <section className="space-y-2">
                <h3 className="text-sm font-semibold">{t('选路事件')}</h3>
                <div className="overflow-x-auto rounded-lg border">
                  <Table>
                    <TableHeader><TableRow><TableHead>{t('阶段')}</TableHead><TableHead>{t('模型')}</TableHead><TableHead>{t('来源')}</TableHead><TableHead>{t('结果')}</TableHead><TableHead>{t('原因')}</TableHead><TableHead>{t('耗时')}</TableHead><TableHead>{t('时间')}</TableHead></TableRow></TableHeader>
                    <TableBody>{events.length ? events.map((item) => <TableRow key={item.id}><TableCell>{t(item.planned ? '计划' : '终态')}</TableCell><TableCell className="font-mono text-xs">{item.model || '-'}</TableCell><TableCell>{item.source_kind || 'unknown'}{item.source_id ? ` #${item.source_id}` : ''}</TableCell><TableCell>{item.result || (item.planned ? 'pending' : '-')}</TableCell><TableCell>{item.fallback_reason || item.selection_reason || '-'}</TableCell><TableCell>{Number(item.elapsed_ms ?? 0).toLocaleString()} ms</TableCell><TableCell>{unixTime(item.created_at)}</TableCell></TableRow>) : <TableRow><TableCell colSpan={7} className="text-center text-muted-foreground">{t('无选路事件')}</TableCell></TableRow>}</TableBody>
                  </Table>
                </div>
              </section>
              <section className="space-y-2">
                <h3 className="text-sm font-semibold">{t('结算尝试')} ({audit.data?.attempt_total ?? attempts.length})</h3>
                <div className="overflow-x-auto rounded-lg border">
                  <Table>
                    <TableHeader><TableRow><TableHead>#</TableHead><TableHead>{t('请求 ID')}</TableHead><TableHead>{t('预留 ID')}</TableHead><TableHead>{t('来源')}</TableHead><TableHead>{t('上游模型')}</TableHead><TableHead>{t('状态')}</TableHead><TableHead>{t('实际金额')}</TableHead></TableRow></TableHeader>
                    <TableBody>{attempts.length ? attempts.map((item, index) => (
                      <TableRow key={item.reservationId || item.reservation_id || index}>
                        <TableCell>{item.attemptNumber ?? item.attempt_number ?? '-'}</TableCell>
                        <TableCell className="font-mono text-xs">{item.requestId || item.request_id || '-'}</TableCell>
                        <TableCell className="font-mono text-xs">{item.reservationId || item.reservation_id || '-'}</TableCell>
                        <TableCell>{item.sourceKind || item.source_kind || '-'}</TableCell>
                        <TableCell className="font-mono text-xs">{item.upstreamModelId || item.upstream_model_id || '-'}</TableCell>
                        <TableCell>{item.status || '-'}</TableCell>
                        <TableCell>{formatUSD(item.actualCost ?? item.actual_cost ?? 0)}</TableCell>
                      </TableRow>
                    )) : <TableRow><TableCell colSpan={7} className="text-center text-muted-foreground">{t('无结算尝试')}</TableCell></TableRow>}</TableBody>
                  </Table>
                </div>
                {(audit.data?.attempt_total ?? 0) > 100 && <div className="flex items-center justify-end gap-2">
                  <Button variant="outline" size="sm" disabled={page === 1} onClick={() => setPage(page - 1)}>{t('上一页')}</Button>
                  <span>{page}</span>
                  <Button variant="outline" size="sm" disabled={page * 100 >= (audit.data?.attempt_total ?? 0)} onClick={() => setPage(page + 1)}>{t('下一页')}</Button>
                </div>}
              </section>
            </div>
          )}
        </DialogContent>
      </Dialog>
    </>
  );
}
