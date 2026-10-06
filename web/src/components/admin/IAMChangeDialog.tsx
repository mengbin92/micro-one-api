import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { PreviewView } from '@/components/admin/IAMDetails';
import { writeIAM } from '@/lib/iam-api';
import { useAuthorization } from '@/lib/authorization';
import type { IAMReply, IAMRequest } from '@/lib/iam-types';
import { t } from '@/lib/i18n';
import { getApiErrorMessage } from '@/lib/api-error';
export interface IAMChange {
  title: string; path: string; method: 'post' | 'put' | 'patch' | 'delete'; rpc: string; permission: string; request: IAMRequest;
}
export function IAMChangeDialog({ change, onClose }: { change: IAMChange; onClose: () => void }) {
  const auth = useAuthorization();
  const client = useQueryClient();
  const [reason, setReason] = useState('');
  const [preview, setPreview] = useState<IAMReply>();
  const [request, setRequest] = useState<IAMRequest>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [conflict, setConflict] = useState(false);
  const currentVersion = auth.snapshot?.versions?.policy_revision;
  const prepare = async () => {
    setBusy(true); setError(''); setPreview(undefined);
    const body: IAMRequest = { ...change.request, operation: change.rpc, reason, request_id: crypto.randomUUID(), expected_policy_revision: currentVersion };
    try {
      const result = await writeIAM('/authorization:simulate', 'post', body);
      setRequest(body); setPreview(result);
    } catch (err) { setError(getApiErrorMessage(err)); }
    finally { setBusy(false); }
  };
  const commit = async () => {
    if (!preview || !request) return;
    setBusy(true); setError('');
    try {
      await writeIAM(change.path, change.method, { ...request, base_policy_revision: preview.base_policy_revision, content_digest: preview.content_digest });
      await auth.refresh(); await client.invalidateQueries({ predicate: q => q.queryKey.includes('iam') }); onClose();
    } catch (err) {
      const stale = (err as { response?: { status?: number } }).response?.status === 409;
      setConflict(stale);
      setError(stale ? t('版本冲突，请刷新数据并重新预检。') : getApiErrorMessage(err));
      setPreview(undefined); setRequest(undefined); await auth.refresh();
    } finally { setBusy(false); }
  };
  return <Dialog open onOpenChange={open => { if (!open && !busy) onClose(); }}><DialogContent className="max-h-[85vh] max-w-3xl overflow-y-auto"><DialogHeader><DialogTitle>{change.title}</DialogTitle></DialogHeader>
    <details open><summary>{t('拟提交变更')}</summary><pre className="overflow-auto rounded bg-muted p-3 text-xs">{JSON.stringify(change.request, null, 2)}</pre></details>
    <label>{t('变更原因')}<input aria-label={t('变更原因')} className="mt-2 w-full rounded border p-2" value={reason} onChange={e => { setReason(e.target.value); setPreview(undefined); setRequest(undefined); }} /></label>
    {error && <p role="alert">{error}</p>}{conflict && <Button onClick={onClose}>{t('关闭并重新加载后预检')}</Button>}{preview && <PreviewView reply={preview} />}
    <div className="flex gap-3"><Button disabled={busy || conflict || !reason.trim() || !currentVersion || !auth.can(change.permission) || !auth.can('iam.authorization.simulate')} onClick={() => void prepare()}>{t('预检变更')}</Button><Button disabled={busy || !preview?.content_digest || !!preview.conflicts?.length || preview.base_policy_revision !== currentVersion || !auth.can(change.permission)} onClick={() => void commit()}>{t('确认提交')}</Button></div>
  </DialogContent></Dialog>;
}
