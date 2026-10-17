import { useState, type FormEvent } from 'react';
import { useAuthorizedQuery } from '@/lib/authorization';
import { adminApiClient } from '@/lib/api';
import { t, locale } from '@/lib/i18n';
import { PermissionButton as Button } from '@/components/admin/PermissionButton';
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { RequestTracePanel, type TraceList } from './RequestTracePanel';

function localTime(ms: number) { const date = new Date(ms); return new Date(ms - date.getTimezoneOffset() * 60000).toISOString().slice(0, 16); }

export function RequestTraceSearchDialog() {
  const [open, setOpen] = useState(false);
  const [start, setStart] = useState(() => localTime(Date.now() - 86400000));
  const [end, setEnd] = useState(() => localTime(Date.now() + 60000));
  const [user, setUser] = useState(''); const [root, setRoot] = useState(''); const [outcome, setOutcome] = useState('');
  const [filters, setFilters] = useState<Record<string, string | number> | null>(null);
  const [tokens, setTokens] = useState(['']); const [selected, setSelected] = useState(''); const [error, setError] = useState('');
  const token = tokens[tokens.length - 1];
  const query = useAuthorizedQuery({ permission: 'log.request.list', queryKey: ['request-trace-search', filters, token], enabled: open && !!filters,
    queryFn: async () => (await adminApiClient.get<TraceList>('/log/request-traces', { params: { ...filters, page_token: token, page_size: 50 } })).data, retry: false });
  const submit = (event: FormEvent) => {
    event.preventDefault(); const from = Date.parse(start); const to = Date.parse(end);
    if (!Number.isFinite(from) || !Number.isFinite(to) || from >= to || to - from > 7 * 86400000 || (user && !/^\d+$/.test(user))) { setError(t('请输入有效的时间范围，单次最多七天。')); return; }
    setError(''); setTokens(['']); setSelected(''); setFilters({ start_time_ms: from, end_time_ms: to, user_id: user, root_request_id: root.trim(), outcome });
  };
  return <><Button permission="log.request.list" variant="outline" onClick={() => setOpen(true)}>{t('请求链路查询')}</Button>
    <Dialog open={open} onOpenChange={setOpen}><DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-5xl">
      <DialogHeader><DialogTitle>{t('请求链路查询')}</DialogTitle><DialogDescription>{t('查找失败或无账单的模型请求；空用户筛选仍受当前授权范围限制。')}</DialogDescription></DialogHeader>
      <form onSubmit={submit} className="grid gap-3 sm:grid-cols-3">
        <Label>{t('开始时间')}<Input type="datetime-local" value={start} onChange={e => setStart(e.target.value)} /></Label>
        <Label>{t('结束时间')}<Input type="datetime-local" value={end} onChange={e => setEnd(e.target.value)} /></Label>
        <Label>{t('用户 ID（可选）')}<Input value={user} onChange={e => setUser(e.target.value)} /></Label>
        <Label>{t('根请求 ID（可选）')}<Input value={root} maxLength={128} onChange={e => setRoot(e.target.value)} /></Label>
        <Label>{t('请求结果')}<select className="mt-1 h-9 w-full rounded border bg-background px-2 text-sm" value={outcome} onChange={e => setOutcome(e.target.value)}>
          {Object.entries({ '': '全部', success: '成功', rejected: '已拒绝', failed: '失败', cancelled: '已取消', incomplete: '未完整输出', unknown: '未知' }).map(([value, label]) => <option key={value} value={value}>{t(label)}</option>)}
        </select></Label><Button permission="log.request.list" type="submit" className="self-end">{t('查询')}</Button>
      </form>
      {error && <p role="alert">{error}</p>}
      {query.isError && <p role="alert">{t('调用链暂不可用或无权读取。')}</p>}
      {query.isLoading && <p>{t('正在读取调用链…')}</p>}
      {query.data && !selected && <div className="space-y-2">
        {query.data.items.length === 0 && <p>{t('此范围没有匹配的请求。')}</p>}
        {query.data.items.map(summary => <Button permission="log.request.read" key={summary.observation_id} variant="outline" className="h-auto w-full justify-start whitespace-normal text-left" onClick={() => setSelected(summary.observation_id)}>{new Date(summary.started_at_ms).toLocaleString(locale())} · {summary.endpoint} · {summary.outcome} · {summary.user_id || t('未知用户')} · {summary.observation_id}</Button>)}
        <div className="flex gap-2"><Button variant="outline" disabled={tokens.length < 2} onClick={() => setTokens(tokens.slice(0, -1))}>{t('上一页')}</Button><Button variant="outline" disabled={!query.data.next_page_token} onClick={() => setTokens([...tokens, query.data!.next_page_token!])}>{t('下一页')}</Button></div>
      </div>}
      {selected && <><Button variant="outline" onClick={() => setSelected('')}>{t('返回查询结果')}</Button><RequestTracePanel key={selected} observationID={selected} /></>}
    </DialogContent></Dialog></>;
}
