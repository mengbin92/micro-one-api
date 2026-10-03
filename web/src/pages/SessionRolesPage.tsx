import { useState } from 'react';
import { Link } from 'react-router';
import { Button } from '@/components/ui/button';
import { PageLoading } from '@/components/PageLoading';
import { SourcesView } from '@/components/admin/IAMDetails';
import { activateRoles } from '@/lib/iam-api';
import { useAuthorization, suspendProtectedQueries } from '@/lib/authorization';
import { useQueryClient } from '@tanstack/react-query';
import { firstAdminPage } from '@/lib/admin-permissions';
import { getApiErrorMessage } from '@/lib/api-error';
import { t } from '@/lib/i18n';
export function SessionRolesPage() {
  const auth = useAuthorization();
  const client = useQueryClient();
  const [selection, setSelection] = useState<string[] | null>(null);
  const [reason, setReason] = useState('');
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);
  const snapshot = auth.snapshot;
  const ids = selection ?? snapshot?.active_role_ids ?? [];
  const activate = async () => {
    setSaving(true); setError(''); suspendProtectedQueries(client);
    try { await activateRoles(ids, snapshot?.session?.revision ?? '0', reason); setSelection(null); await auth.refresh(); }
    catch (err) { setError(getApiErrorMessage(err)); await auth.refresh(); }
    finally { setSaving(false); }
  };
  if (auth.isPending) return <PageLoading />;
  return <div className="mx-auto max-w-4xl space-y-5"><h2 className="text-2xl font-semibold">{t('我的会话角色')}</h2>
    {auth.isError ? <p role="alert">{t('授权状态不可用')} <Button onClick={() => void auth.refresh()}>{t('重试')}</Button></p> : snapshot?.authorization_mode === 'legacy' ? <p>{t('当前使用 legacy 授权，角色激活将在 IAM 启用后开放。')}</p> : <>
    <p>{t('仅能激活本人已分配角色。职责分离冲突时，请重新选择角色。')}</p>
    <p>{t('激活状态')} {snapshot?.session?.activation_state} · {t('会话版本')} {snapshot?.session?.revision}</p>
    {(snapshot?.authorized_role_ids ?? []).map(id => <label key={id} className="flex items-center gap-3 rounded border p-3"><input type="checkbox" checked={ids.includes(id)} onChange={e => setSelection(e.target.checked ? [...ids, id] : ids.filter(value => value !== id))} />{snapshot?.roles?.find(role => role.id === id)?.name ?? `#${id}`}</label>)}
    <label className="block">{t('变更原因')}<input className="mt-2 w-full rounded border p-2" value={reason} onChange={e => setReason(e.target.value)} /></label>
    <Button disabled={saving || !reason.trim() || !snapshot || auth.isFetching} onClick={() => void activate()}>{t('激活所选角色')}</Button>
    {error && <p role="alert">{error}</p>}
    {auth.can('admin.console.enter') && <Link className="ml-4 underline" to={firstAdminPage(auth.can)}>{t('进入管理')}</Link>}
    <details><summary>{t('授权来源与强制拒绝')}</summary><SourcesView sources={snapshot?.sources} /></details>
    </>}
  </div>;
}
