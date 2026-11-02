import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { useAuthorizedQuery } from '@/lib/authorization';
import { adminApiClient } from '@/lib/api';
import { announcementState, announcementTime, fromBeijingInput, toBeijingInput, validAnnouncementSchedule, type Announcement, type AnnouncementList } from '@/lib/announcements';
import { t } from '@/lib/i18n';
import { PermissionButton as Button } from '@/components/admin/PermissionButton';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';

const PAGE_SIZE = 20;
const blank = (): Announcement => ({ id: '', revision: 0, title: '', body: '', audience: 'all', targets: [], status: 'draft', publish_at: 0, expires_at: 0 });
export function AnnouncementsPage() {
  const client = useQueryClient();
  const [page, setPage] = useState(1);
  const [draft, setDraft] = useState<Announcement | null>(null);
  const [targets, setTargets] = useState('');
  const [reason, setReason] = useState('');
  const list = useAuthorizedQuery({ permission: 'system.content.notice.update', queryKey: ['admin-announcements', page], queryFn: async ({ signal }) => (await adminApiClient.get<AnnouncementList<Announcement>>('/admin/announcements', { params: { page, page_size: PAGE_SIZE }, signal })).data });
  const save = useMutation({ mutationFn: async () => {
    if (!draft) throw new Error(t('请选择公告'));
    const announcement = { ...draft, targets: draft.audience === 'all' ? [] : targets.split(/[,，\s]+/).filter(Boolean) };
    return adminApiClient.put<Announcement>('/admin/announcements', { announcement, reason });
  }, onSuccess: async () => { setDraft(null); setReason(''); await client.invalidateQueries({ queryKey: ['admin-announcements'] }); } });
  const edit = (a: Announcement) => { setDraft({ ...a }); setTargets(a.targets?.join(', ') ?? ''); setReason(''); save.reset(); };
  const change = (patch: Partial<Announcement>) => setDraft(previous => previous ? { ...previous, ...patch } : previous);
  const invalidSchedule = !!draft && !validAnnouncementSchedule(draft);
  return <div className="space-y-6">
    <div className="flex flex-wrap justify-between gap-3"><div><h1 className="text-2xl font-semibold tracking-tight">{t('公告管理')}</h1><p className="mt-2 text-sm text-muted-foreground">{t('定向通知用户，按北京时间安排发布与下架。')}</p></div><Button permission="system.content.notice.update" disabled={save.isPending} onClick={() => edit(blank())}>{t('新建公告')}</Button></div>
    {list.isError && <p role="alert">{t('公告加载失败，请重试。')} <Button variant="outline" onClick={() => void list.refetch()}>{t('重试')}</Button></p>}
    {list.isRestricted && <p role="alert">{t('没有公告管理权限')}</p>}
    {list.isPending && <p role="status">{t('加载中')}</p>}
    {draft && <Card><CardHeader><CardTitle>{t(draft.id ? '编辑公告' : '新建公告')}</CardTitle><CardDescription>{t('保存后增加版本，用户需重新确认已读。正文按纯文本展示。')}</CardDescription></CardHeader><CardContent>
      <form aria-busy={save.isPending} onSubmit={e => { e.preventDefault(); if (validAnnouncementSchedule(draft)) save.mutate(); else setDraft({ ...draft }); }}><fieldset disabled={save.isPending} className="min-w-0 space-y-4">
        <div className="space-y-2"><Label htmlFor="announcement-title">{t('公告标题')}</Label><Input id="announcement-title" required maxLength={120} value={draft.title} onChange={e => change({ title: e.target.value })} /></div>
        <div className="space-y-2"><Label htmlFor="announcement-body">{t('公告正文')}</Label><textarea className="min-h-32 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" id="announcement-body" required maxLength={8000} rows={6} value={draft.body} onChange={e => change({ body: e.target.value })} /></div>
        <div className="grid gap-4 sm:grid-cols-2"><div className="space-y-2"><Label htmlFor="announcement-audience">{t('公告受众')}</Label><select id="announcement-audience" className="h-10 w-full rounded-md border bg-background px-3 text-sm" value={draft.audience} onChange={e => { change({ audience: e.target.value as Announcement['audience'] }); setTargets(''); }}><option value="all">{t('全体用户')}</option><option value="groups">{t('指定用户分组')}</option><option value="users">{t('指定用户')}</option></select></div><div className="space-y-2"><Label htmlFor="announcement-status">{t('发布状态')}</Label><select id="announcement-status" className="h-10 w-full rounded-md border bg-background px-3 text-sm" value={draft.status} onChange={e => change({ status: e.target.value as Announcement['status'] })}><option value="draft">{t('草稿')}</option><option value="published">{t('发布')}</option><option value="archived">{t('下架')}</option></select></div></div>
        {draft.audience !== 'all' && <div className="space-y-2"><Label htmlFor="announcement-targets">{t(draft.audience === 'groups' ? '用户默认路由组 ID（逗号分隔）' : '用户 ID（逗号分隔）')}</Label><Input id="announcement-targets" required value={targets} onChange={e => setTargets(e.target.value)} /><p className="text-xs text-muted-foreground">{t('最多 200 个，按用户当前归属判断。')}</p></div>}
        <div className="grid gap-4 sm:grid-cols-2"><div className="space-y-2"><Label htmlFor="announcement-publish">{t('发布时间（北京时间，留空立即）')}</Label><Input id="announcement-publish" type="datetime-local" value={toBeijingInput(draft.publish_at)} onChange={e => change({ publish_at: fromBeijingInput(e.target.value) })} /></div><div className="space-y-2"><Label htmlFor="announcement-expires">{t('下架时间（北京时间，留空不限）')}</Label><Input id="announcement-expires" type="datetime-local" value={toBeijingInput(draft.expires_at)} onChange={e => change({ expires_at: fromBeijingInput(e.target.value) })} /></div></div>
        <div className="space-y-2"><Label htmlFor="announcement-reason">{t('操作原因')}</Label><Input id="announcement-reason" required minLength={3} maxLength={240} value={reason} onChange={e => setReason(e.target.value)} /></div>
        {invalidSchedule && <p role="alert">{t('下架时间必须晚于发布时间，立即发布时须晚于当前时间。')}</p>}
        {save.isError && <p role="alert">{t('保存失败。请保留草稿；如有版本冲突，重新加载后再编辑。')}</p>}
        <div className="flex flex-wrap gap-2"><Button permission="system.content.notice.update" type="submit" disabled={save.isPending || invalidSchedule || reason.trim().length < 3}>{t(save.isPending ? '保存中...' : '保存公告')}</Button><Button type="button" variant="outline" disabled={save.isPending} onClick={() => setDraft(null)}>{t('取消')}</Button></div>
      </fieldset></form>
    </CardContent></Card>}
    {save.isSuccess && <p role="status">{t('公告已保存')}</p>}
    {!list.isError && list.data && <div className="space-y-3">
      {!(list.data.items?.length) && <p className="py-10 text-center text-sm text-muted-foreground">{t('暂无公告')}</p>}
      {list.data.items?.map(a => <Card key={a.id}><CardContent className="flex flex-wrap items-center justify-between gap-4 pt-6"><div className="min-w-0 space-y-2"><h2 className="break-all font-semibold">{a.title}</h2><p className="text-sm text-muted-foreground">{announcementState(a)} · {t(a.audience === 'all' ? '全体用户' : a.audience === 'groups' ? '指定用户分组' : '指定用户')} · {announcementTime(a.publish_at)} {t('北京时间')}</p></div><Button permission="system.content.notice.update" variant="outline" onClick={() => edit(a)} disabled={save.isPending}>{t('编辑')}</Button></CardContent></Card>)}
      <div className="flex items-center justify-between"><Button variant="outline" disabled={page === 1} onClick={() => setPage(page - 1)}>{t('上一页')}</Button><span className="text-sm">{t('第 {page} 页', { page })}</span><Button variant="outline" disabled={page * PAGE_SIZE >= Number(list.data.total ?? 0)} onClick={() => setPage(page + 1)}>{t('下一页')}</Button></div>
    </div>}
  </div>;
}
