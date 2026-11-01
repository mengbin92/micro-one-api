import { useRef, useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useAuthorizedQuery, useAuthorization } from '@/lib/authorization';
import { adminApiClient } from '@/lib/api';
import { t } from '@/lib/i18n';
import { PermissionButton as Button } from '@/components/admin/PermissionButton';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';

type Policy = { revision: number; enabled?: boolean; interval_hours: number; retain: number; rpo_hours: number; rto_minutes: number };
type Job = { id: string; status: string; trigger: string; created_at: number; finished_at?: number; error_code?: string; bytes?: number; sha256?: string };
type Event = { id: string; action: string; actor: string; reason: string; at: number };
const states: Record<string, string> = { queued: '排队中', running: '备份中', succeeded: '备份成功', failed: '备份失败', uncertain: '结果不确定', deleting: '清理中', expired: '已按保留策略清理' };
function errorText(error: unknown) { const e = error as { response?: { data?: { message?: string } }; message?: string }; return e?.response?.data?.message ?? e?.message ?? t('备份操作失败'); }

export function BackupPanel() {
  const client = useQueryClient();
  const auth = useAuthorization();
  const canWrite = auth.displayCan('system.option.security.update');
  const [draft, setDraft] = useState<Policy | null>(null);
  const [reason, setReason] = useState('');
  const requestId = useRef<string | null>(null);
  const policy = useAuthorizedQuery({ permission: 'system.option.read', queryKey: ['backup-policy'], queryFn: async () => (await adminApiClient.get<Policy>('/admin/backup-policy')).data });
  const receipts = useAuthorizedQuery({ permission: 'system.option.read', queryKey: ['backup-jobs'], refetchInterval: 5000, queryFn: async () => (await adminApiClient.get<{ jobs?: Job[]; events?: Event[]; worker_last_seen?: number; worker_phase?: string; worker_error_code?: string; worker_timeout_minutes?: number }>('/admin/backups')).data });
  const save = useMutation({ mutationFn: async () => adminApiClient.put('/admin/backup-policy', { policy: draft ?? policy.data, reason }), onSuccess: async () => { setDraft(null); await client.invalidateQueries({ queryKey: ['backup-policy'] }); await client.invalidateQueries({ queryKey: ['backup-jobs'] }); } });
  const create = useMutation({ mutationFn: async () => { requestId.current ??= crypto.randomUUID(); return adminApiClient.post('/admin/backups', { request_id: requestId.current, reason }); }, onSuccess: async () => { requestId.current = null; await client.invalidateQueries({ queryKey: ['backup-jobs'] }); } });
  const current = draft ?? policy.data;
  const worker = receipts.data;
  const workerFresh = !receipts.isError && !!worker?.worker_last_seen && receipts.dataUpdatedAt / 1000 - worker.worker_last_seen < Math.max(120, (worker.worker_timeout_minutes ?? 0) * 60 + 30);
  const pending = save.isPending || create.isPending;
  const change = (patch: Partial<Policy>) => { if (current) setDraft({ ...current, ...patch }); };
  return <Card>
    <CardHeader><CardTitle>{t('管理化备份')}</CardTitle><CardDescription>{t('备份会短暂停止业务写入。恢复演练须在隔离空环境完成。')}</CardDescription></CardHeader>
    <CardContent className="space-y-5">
      {policy.isLoading && <p role="status">{t('加载中…')}</p>}
      {(policy.error || receipts.error) && <p role="alert">{t('备份管理未就绪，请检查 worker 和存储配置。')}</p>}
      {current && <>
        <Label className="flex items-center gap-2"><input type="checkbox" aria-label={t('启用定时备份')} disabled={!canWrite} checked={!!current.enabled} onChange={e => change({ enabled: e.target.checked })} />{t('启用定时备份')}</Label>
        <div className="grid gap-4 sm:grid-cols-2">
          {([['interval_hours', '备份间隔（小时）', 1, 168], ['retain', '保留成功备份数', 2, 90], ['rpo_hours', '可接受数据丢失（小时）', 1, 168], ['rto_minutes', '恢复时长目标（分钟）', 1, 1440]] as const).map(([key, label, min, max]) => <div className="space-y-2" key={key}><Label htmlFor={`backup-${key}`}>{t(label)}</Label><Input id={`backup-${key}`} type="number" disabled={!canWrite} min={min} max={max} value={current[key]} onChange={e => change({ [key]: Number(e.target.value) })} /></div>)}
        </div>
        <div className="space-y-2"><Label htmlFor="backup-reason">{t('操作原因')}</Label><Input id="backup-reason" value={reason} maxLength={240} onChange={e => setReason(e.target.value)} /></div>
        <div className="flex flex-wrap gap-2"><Button permission="system.option.security.update" disabled={pending || reason.trim().length < 3 || !draft} onClick={() => save.mutate()}>{t('保存备份策略')}</Button><Button permission="system.option.security.update" variant="outline" disabled={pending || reason.trim().length < 3} onClick={() => create.mutate()}>{t('立即备份')}</Button>{draft && <Button variant="ghost" onClick={() => setDraft(null)}>{t('放弃修改')}</Button>}</div>
        {(save.error || create.error) && <p role="alert">{errorText(save.error ?? create.error)}</p>}
        {save.isSuccess && !draft && <p role="status">{t('备份策略已保存')}</p>}
        {create.isSuccess && <p role="status">{t('任务已排队，备份完成后可查看结果。')}</p>}
      </>}
      {worker && <p role="status">{t(workerFresh ? '备份 worker 已连接' : '备份 worker 未连接')}{worker.worker_phase === 'attention' && ` · ${t('最近一次备份检查失败')}`}</p>}
      <div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>{t('任务')}</TableHead><TableHead>{t('状态')}</TableHead><TableHead>{t('创建时间')}</TableHead><TableHead>{t('大小')}</TableHead></TableRow></TableHeader><TableBody>{(receipts.data?.jobs ?? []).map(job => <TableRow key={job.id}><TableCell className="max-w-48 break-all">{job.id}</TableCell><TableCell>{t(states[job.status] ?? job.status)}{job.error_code && <p className="text-xs text-muted-foreground">{job.error_code}</p>}</TableCell><TableCell>{new Date(job.created_at * 1000).toLocaleString()}</TableCell><TableCell>{job.bytes ? `${(job.bytes / 1024 / 1024).toFixed(2)} MB` : '—'}</TableCell></TableRow>)}</TableBody></Table></div>
      {!receipts.isLoading && !receipts.isError && !receipts.data?.jobs?.length && <p>{t('暂无备份任务')}</p>}
      <details><summary>{t('备份审计')}</summary><ul className="mt-2 space-y-2 text-sm">{(receipts.data?.events ?? []).map(event => <li className="break-words" key={event.id}>{new Date(event.at * 1000).toLocaleString()} · {t(states[event.action] ?? event.action)} · {event.actor} · {event.reason}</li>)}</ul></details>
    </CardContent>
  </Card>;
}
