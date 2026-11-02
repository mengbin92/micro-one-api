import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Activity, Bell, RefreshCw } from 'lucide-react';
import { useState } from 'react';
import { apiClient } from '@/lib/api';
import { useAuthorization } from '@/lib/authorization';
import { announcementTime, type AnnouncementList, type ModelAvailability, type UserAnnouncement } from '@/lib/announcements';
import { t } from '@/lib/i18n';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';

const PAGE_SIZE = 20;

export function OperationsPage() {
  const auth = useAuthorization();
  const client = useQueryClient();
  const [page, setPage] = useState(1);
  const [search, setSearch] = useState('');
  const enabled = !!auth.displaySnapshot;
  const models = useQuery({ queryKey: ['my-model-availability', auth.generation], enabled, queryFn: async ({ signal }) => (await apiClient.get<ModelAvailability>('/user/model-availability', { signal })).data, retry: false, staleTime: 15000, refetchInterval: 30000, meta: { protected: true } });
  const notices = useQuery({ queryKey: ['my-announcements', auth.generation, page], enabled, queryFn: async ({ signal }) => (await apiClient.get<AnnouncementList<UserAnnouncement>>('/user/announcements', { params: { page, page_size: PAGE_SIZE }, signal })).data, retry: false, refetchInterval: 30000, meta: { protected: true } });
  const read = useMutation({ mutationFn: async (a: UserAnnouncement) => apiClient.post('/user/announcements/read', { id: a.id, revision: a.revision }), onSuccess: () => client.invalidateQueries({ queryKey: ['my-announcements'] }) });
  const refresh = () => { void models.refetch(); void notices.refetch(); };
  const filteredModels = (models.data?.models ?? []).filter(model => model.toLowerCase().includes(search.toLowerCase()));
  return <div className="space-y-6">
    <div className="flex flex-wrap items-start justify-between gap-3"><div><p className="text-sm text-muted-foreground">{t('服务动态')}</p><h1 className="text-2xl font-semibold tracking-tight">{t('模型与公告')}</h1><p className="mt-2 text-sm text-muted-foreground">{t('查看当前账号的模型可用性与服务公告。')}</p></div><Button variant="outline" onClick={refresh} disabled={models.isFetching || notices.isFetching}><RefreshCw className="size-4" />{t('刷新')}</Button></div>
    {!enabled && <p role="alert">{t('正在确认登录状态，请稍候。')}</p>}
    <Card><CardHeader><CardTitle className="flex items-center gap-2"><Activity className="size-5" />{t('我的可用模型')}</CardTitle><CardDescription>{t('按当前账号路由权限展示；路由配置快照不保证每次请求成功。')}</CardDescription></CardHeader><CardContent className="space-y-4">
      {models.isError ? <p role="alert">{t('模型状态暂不可用，请刷新重试。')}</p> : models.isPending ? <p role="status">{t('加载中')}</p> : <>
        <div className="flex flex-wrap items-center justify-between gap-2"><span className="rounded-full bg-muted px-3 py-1 text-sm font-medium">{t(models.data.status === 'available' ? '有可用路由' : '暂无可用路由')}</span><span className="text-xs text-muted-foreground">{t('更新时间')} · {announcementTime(models.data.checked_at)} {t('北京时间')}</span></div>
        <Input aria-label={t('搜索模型')} placeholder={t('搜索模型')} value={search} onChange={e => setSearch(e.target.value)} />
        <ul className="grid gap-2 sm:grid-cols-2 xl:grid-cols-3">{filteredModels.map(model => <li key={model} className="break-all rounded-xl border px-4 py-3 font-mono text-sm">{model}</li>)}</ul>
        {!filteredModels.length && <p className="text-sm text-muted-foreground">{t('没有匹配的可用模型')}</p>}
      </>}
    </CardContent></Card>
    <Card><CardHeader><CardTitle className="flex items-center gap-2"><Bell className="size-5" />{t('服务公告')}</CardTitle><CardDescription>{t('仅显示面向当前账号且已发布的公告。')}</CardDescription></CardHeader><CardContent className="space-y-4">
      {notices.isError ? <p role="alert">{t('公告暂不可用，请刷新重试。')}</p> : notices.isPending ? <p role="status">{t('加载中')}</p> : <>
        {!(notices.data.items?.length) && <p className="py-6 text-center text-sm text-muted-foreground">{t(page === 1 ? '暂无公告' : '本页暂无公告，请返回上一页。')}</p>}
        {notices.data.items?.map(a => <article key={a.id} className="space-y-3 rounded-xl border p-4"><div className="flex flex-wrap justify-between gap-2"><h2 className="break-all font-semibold">{a.title}</h2><span className="text-xs text-muted-foreground">{t(a.read ? '已读' : '未读')}</span></div><p className="text-xs text-muted-foreground">{announcementTime(a.publish_at)} {t('北京时间')}</p><p className="whitespace-pre-wrap break-words text-sm leading-7">{a.body}</p>{!a.read && <Button size="sm" variant="outline" disabled={read.isPending} onClick={() => read.mutate(a)}>{t('标为已读')}</Button>}</article>)}
        <div className="flex items-center justify-between gap-2"><Button variant="outline" disabled={page === 1} onClick={() => setPage(page - 1)}>{t('上一页')}</Button><span className="text-sm">{t('第 {page} 页', { page })}</span><Button variant="outline" disabled={page * PAGE_SIZE >= Number(notices.data.total ?? 0)} onClick={() => setPage(page + 1)}>{t('下一页')}</Button></div>
      </>}
      {read.isError && <p role="alert">{t('已读保存失败，公告可能已更新或下架，请刷新。')}</p>}
    </CardContent></Card>
  </div>;
}
