import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { adminApiClient } from '@/lib/api';
import { unwrapApiData } from '@/lib/api-response';
import { t } from '@/lib/i18n';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { EmptyState } from '@/components/EmptyState';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';

interface RoutingGroup {
  id: number; key: string; display_name: string; description: string;
  status: string; access_mode: string; model_access_mode: string; revision: number;
}
interface GroupList { groups: RoutingGroup[]; next_page_token: string }
interface GroupDetail {
  group: RoutingGroup;
  resources: { source_kind: string; source_id: number; priority: number; weight: number }[];
  model_grants: { mapping_id: number; account_id: number; model: string; upstream_model_id: string; enabled: boolean; priority: number; extra_authorization: boolean }[];
}
const statusLabel = (value: string) => ({ enabled: t('启用'), disabled: t('停用'), archived: t('已归档') }[value] ?? value);

export function AdminRoutingGroupsPage() {
  const [search, setSearch] = useState('');
  const [key, setKey] = useState('');
  const [pages, setPages] = useState(['']);
  const [selected, setSelected] = useState<number | null>(null);
  const token = pages[pages.length - 1];
  const groups = useQuery({
    queryKey: ['admin-routing-groups', key, token],
    queryFn: async () => {
      const res = await adminApiClient.get('/v1/admin/routing-groups', { params: {
        page_size: 50, page_token: token, order_by: 'sort_order asc,key asc',
        filter: key ? `key = ${JSON.stringify(key)}` : '',
      } });
      return unwrapApiData<GroupList>(res.data, t('分组加载失败'));
    },
  });
  const detail = useQuery({
    queryKey: ['admin-routing-group', selected], enabled: selected !== null,
    queryFn: async () => {
      const res = await adminApiClient.get(`/v1/admin/routing-groups/${selected}`);
      return unwrapApiData<GroupDetail>(res.data, t('分组详情加载失败'));
    },
  });
  return <div className="space-y-6">
    <div className="space-y-2">
      <h2 className="text-2xl font-semibold">{t('分组')}</h2>
      <p className="text-sm text-muted-foreground">{t('查看路由分组的资源成员和模型授权。分组状态与上游资源健康状态分别管理。')}</p>
    </div>
    <form className="flex flex-wrap items-end gap-2" onSubmit={(event) => { event.preventDefault(); setKey(search); setPages(['']); setSelected(null); }}>
      <div className="space-y-2"><Label htmlFor="routing-group-key">{t('分组键（精确匹配）')}</Label>
        <Input id="routing-group-key" value={search} onChange={(e) => setSearch(e.target.value)} placeholder="default" /></div>
      <Button type="submit" variant="outline">{t('查询')}</Button>
      <Button type="button" variant="ghost" onClick={() => { setSearch(''); setKey(''); setPages(['']); setSelected(null); }}>{t('清除')}</Button>
    </form>
    {groups.isPending ? <p role="status">{t('加载中...')}</p> : groups.isError ?
      <div role="alert" className="space-y-2"><p>{t('分组加载失败，请稍后重试。')}</p><Button variant="outline" onClick={() => void groups.refetch()}>{t('重试')}</Button></div> :
      !groups.data?.groups.length ? <EmptyState title={t('暂无路由分组')} description={t('没有符合查询条件的分组。')} /> :
      <div className="overflow-x-auto rounded-lg border"><Table><TableHeader><TableRow>
        <TableHead>{t('名称 / 键')}</TableHead><TableHead>{t('状态')}</TableHead><TableHead>{t('使用资格')}</TableHead><TableHead>{t('操作')}</TableHead>
      </TableRow></TableHeader><TableBody>{groups.data.groups.map((group) => <TableRow key={group.id}>
        <TableCell><div className="font-medium">{group.display_name || group.key}</div><code>{group.key}</code></TableCell>
        <TableCell>{statusLabel(group.status)}</TableCell><TableCell>{group.access_mode === 'public' ? t('公开') : t('需授权')}</TableCell>
        <TableCell><Button size="sm" variant="outline" onClick={() => setSelected(group.id)} aria-label={t('查看分组 {name}', { name: group.key })}>{t('查看详情')}</Button></TableCell>
      </TableRow>)}</TableBody></Table></div>}
    <div className="flex gap-2">
      <Button variant="outline" disabled={pages.length === 1 || groups.isFetching} onClick={() => { setPages((p) => p.slice(0, -1)); setSelected(null); }}>{t('上一页')}</Button>
      <Button variant="outline" disabled={!groups.data?.next_page_token || groups.isFetching || groups.isError} onClick={() => { if (groups.data?.next_page_token) setPages((p) => [...p, groups.data.next_page_token]); setSelected(null); }}>{t('下一页')}</Button>
    </div>
    {selected !== null && <section className="space-y-4 rounded-lg border p-4" aria-label={t('分组详情')}>
      {detail.isPending ? <p role="status">{t('加载中...')}</p> : detail.isError ? <div role="alert"><p>{t('分组详情加载失败。')}</p><Button variant="outline" onClick={() => void detail.refetch()}>{t('重试')}</Button></div> : detail.data && <>
        <div><h3 className="text-lg font-semibold">{detail.data.group.display_name || detail.data.group.key}</h3><p className="text-sm text-muted-foreground">ID {detail.data.group.id} · {detail.data.group.key} · {statusLabel(detail.data.group.status)}</p>
          {detail.data.group.description && <p className="mt-2 text-sm">{detail.data.group.description}</p>}</div>
        <h4 className="font-medium">{t('资源成员')}</h4>
        <p className="text-sm text-muted-foreground">{t('优先级和权重继承资源配置；具体模型仍需通过模型授权检查。')}</p>
        {!detail.data.resources.length ? <p>{t('暂无资源成员')}</p> : <div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>{t('资源类型')}</TableHead><TableHead>ID</TableHead><TableHead>{t('优先级')}</TableHead><TableHead>{t('权重')}</TableHead></TableRow></TableHeader><TableBody>
          {detail.data.resources.map((r) => <TableRow key={`${r.source_kind}:${r.source_id}`}><TableCell>{r.source_kind === 'channel' ? t('API 渠道') : t('上游订阅账号')}</TableCell><TableCell>{r.source_id}</TableCell><TableCell>{r.priority}</TableCell><TableCell>{r.weight}</TableCell></TableRow>)}
        </TableBody></Table></div>}
        <h4 className="font-medium">{t('账号模型授权')}</h4>
        <p className="text-sm text-muted-foreground">{t('模型级额外授权仅覆盖列出的模型，不授予该账号的其他模型。')}</p>
        {!detail.data.model_grants.length ? <p>{t('暂无账号模型授权')}</p> : <div className="overflow-x-auto"><Table><TableHeader><TableRow><TableHead>{t('模型')}</TableHead><TableHead>{t('账号 ID')}</TableHead><TableHead>{t('上游模型')}</TableHead><TableHead>{t('优先级')}</TableHead><TableHead>{t('状态')}</TableHead><TableHead>{t('授权来源')}</TableHead></TableRow></TableHeader><TableBody>
          {detail.data.model_grants.map((m) => <TableRow key={m.mapping_id}><TableCell>{m.model}</TableCell><TableCell>{m.account_id}</TableCell><TableCell>{m.upstream_model_id || m.model}</TableCell><TableCell>{m.priority}</TableCell><TableCell>{m.enabled ? t('启用') : t('停用')}</TableCell><TableCell>{m.extra_authorization ? t('模型级额外授权') : t('组成员模型映射')}</TableCell></TableRow>)}
        </TableBody></Table></div>}
      </>}
    </section>}
  </div>;
}
