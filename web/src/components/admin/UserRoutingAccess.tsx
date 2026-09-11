import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { adminApiClient } from '@/lib/api';
import { unwrapApiData, ensureApiSuccess } from '@/lib/api-response';
import type { RoutingFacts } from '@/lib/routing-groups';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from '@/components/ui/dialog';

export function UserRoutingAccess({ userId }: { userId: string }) {
  const [open, setOpen] = useState(false);
  const [groupId, setGroupId] = useState('');
  const [expires, setExpires] = useState('');
  const [saving, setSaving] = useState(false);
  const queryClient = useQueryClient();
  const facts = useQuery({ queryKey: ['user-routing-access', userId], enabled: open, retry: false, queryFn: async () => unwrapApiData<RoutingFacts>((await adminApiClient.get(`/v1/admin/routing-access/${userId}`)).data) });
  const groups = useQuery({ queryKey: ['admin-routing-access-groups'], enabled: open, queryFn: async () => {
    const all: { id: number; key: string; display_name: string; status: string }[] = [];
    let token = '';
    do { const page = unwrapApiData<{ groups: typeof all; next_page_token: string }>((await adminApiClient.get('/v1/admin/routing-groups', { params: { page_size: 200, page_token: token } })).data); all.push(...page.groups); token = page.next_page_token; } while (token);
    return all;
  } });
  const change = async (body: object) => {
    if (!facts.data) return;
    setSaving(true);
    try { const res = await adminApiClient.patch(`/v1/admin/routing-access/${userId}`, { ...body, expected_revision: facts.data.revision }); ensureApiSuccess(res.data, '修改授权失败'); await facts.refetch(); await queryClient.invalidateQueries({ queryKey: ['available-routing-groups'] }); toast.success('分组设置已更新'); }
    catch (error) { toast.error(error instanceof Error ? error.message : '修改授权失败'); await facts.refetch(); }
    finally { setSaving(false); }
  };
  return <>
    <Button variant="outline" size="sm" onClick={() => setOpen(true)}>分组授权</Button>
    <Dialog open={open} onOpenChange={setOpen}><DialogContent className="max-h-[85vh] overflow-y-auto"><DialogHeader><DialogTitle>默认分组与分组授权</DialogTitle><DialogDescription>默认分组只影响跟随默认的 Key；撤销一项授权后，其他有效来源仍可提供访问资格。</DialogDescription></DialogHeader>
      {(facts.isError || groups.isError) && <p role="alert">分组设置暂不可用</p>}
      {facts.data && <div className="space-y-4">
        <p>默认分组：{groups.data?.find((g) => g.id === facts.data.default_routing_group_id)?.key || `#${facts.data.default_routing_group_id}`}</p>
        <label className="block">公开组访问<select aria-label="公开组访问" disabled={saving} className="ml-2 rounded border p-1" value={facts.data.public_group_access} onChange={(e) => change({ operation: 'public_access', public_group_access: e.target.value })}><option value="explicit_only">仅显式授权</option><option value="all">允许所有公开组</option></select></label>
        <label className="block">目标分组<select aria-label="目标分组" className="ml-2 rounded border p-1" value={groupId} onChange={(e) => setGroupId(e.target.value)}><option value="">请选择</option>{groups.data?.filter((g) => g.status === 'enabled').map((g) => <option key={g.id} value={g.id}>{g.display_name || g.key}</option>)}</select></label>
        <label className="block">授权到期时间（留空为永久）<input aria-label="授权到期时间" type="datetime-local" value={expires} onChange={(e) => setExpires(e.target.value)} className="block rounded border p-1" /></label>
        <div className="flex gap-2"><Button disabled={saving || !groupId} onClick={() => change({ operation: 'grant', routing_group_id: Number(groupId), source_type: 'admin', source_ref: 'manual', expires_at: expires ? Math.floor(new Date(expires).getTime() / 1000) : 0 })}>授予访问</Button><Button variant="outline" disabled={saving || !groupId} onClick={() => change({ operation: 'default', routing_group_id: Number(groupId) })}>设为默认</Button></div>
        {facts.data.grants.map((g) => <div key={`${g.routing_group_id}/${g.source_type}/${g.source_ref}`} className="border-t pt-2 text-sm">
          <p>{groups.data?.find((group) => group.id === g.routing_group_id)?.key || `#${g.routing_group_id}`} · {g.source_type}/{g.source_ref} · {g.status}</p>
          <p>{g.expires_at ? `有效至 ${new Date(g.expires_at * 1000).toLocaleString()}` : '永久授权'}</p>
          <p>关联 Key：{facts.data.tokens?.filter((token) => token.mode === 'fixed' ? token.group_id === g.routing_group_id : facts.data.default_routing_group_id === g.routing_group_id).map((token) => token.name).join('、') || '无'}</p>
          {g.source_type === 'subscription' && <p>此来源由订阅管理；撤销订阅或到期后自动失效。</p>}
          {g.status === 'active' && g.source_type !== 'subscription' && <Button size="sm" variant="destructive" disabled={saving} onClick={() => change({ operation: 'revoke', ...g })}>撤销此来源</Button>}
        </div>)}
      </div>}
    </DialogContent></Dialog>
  </>;
}
