import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { adminApiClient } from '@/lib/api';
import { unwrapApiData, ensureApiSuccess } from '@/lib/api-response';
import { t } from '@/lib/i18n';
import type { AvailableGroup, AvailableGroups, RoutingFacts, RoutingGrant } from '@/lib/routing-groups';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from '@/components/ui/dialog';

const sourceLabel = (source: RoutingGrant) => {
  if (source.source_type === 'admin') return t('管理员手动授权');
  if (source.source_type === 'migration') return t('迁移继承');
  if (source.source_type === 'subscription') return t('订阅权益 #{id}', { id: source.source_ref });
  if (source.source_type === 'public') return t('公开分组');
  return `${source.source_type}/${source.source_ref || '—'}`;
};
const priceSourceLabel = (source: string) => ({
  billing_default: t('Billing 默认倍率'),
  GroupRatio: t('基础倍率配置'),
  routing_billing_policy: t('已发布结算策略'),
  user_routing_price_override: t('用户专属倍率（替换基础倍率）'),
}[source] ?? source);
const billingModeLabel = (mode: string) => ({
  wallet_only: t('仅钱包'),
  subscription_only: t('仅订阅'),
  subscription_first: t('订阅优先，余额补足'),
}[mode] ?? mode);

export function UserRoutingAccess({ userId }: { userId: string }) {
  const [open, setOpen] = useState(false);
  const [groupId, setGroupId] = useState('');
  const [expires, setExpires] = useState('');
  const [priceGroupId, setPriceGroupId] = useState('');
  const [priceRatio, setPriceRatio] = useState('');
  const [saving, setSaving] = useState(false);
  const queryClient = useQueryClient();
  const facts = useQuery({ queryKey: ['user-routing-access', userId], enabled: open, retry: false, queryFn: async () => unwrapApiData<RoutingFacts>((await adminApiClient.get(`/v1/admin/routing-access/${userId}`)).data) });
  const groups = useQuery({ queryKey: ['admin-routing-access-groups'], enabled: open, queryFn: async () => {
    const all: { id: number; key: string; display_name: string; status: string }[] = [];
    let token = '';
    do { const page = unwrapApiData<{ groups: typeof all; next_page_token: string }>((await adminApiClient.get('/v1/admin/routing-groups', { params: { page_size: 200, page_token: token } })).data); all.push(...page.groups); token = page.next_page_token; } while (token);
    return all;
  } });
  const available = useQuery({ queryKey: ['admin-user-routing-available', userId], enabled: open, retry: false, queryFn: async () => {
    const all: AvailableGroup[] = [];
    let token = '';
    do {
      const page = unwrapApiData<AvailableGroups>((await adminApiClient.get(`/v1/admin/routing-access/${userId}/available`, { params: { page_size: 200, page_token: token } })).data);
      all.push(...page.groups); token = page.next_page_token;
    } while (token);
    return all;
  } });
  const change = async (body: object) => {
    if (!facts.data) return;
    setSaving(true);
    try {
      const res = await adminApiClient.patch(`/v1/admin/routing-access/${userId}`, { ...body, expected_revision: facts.data.revision });
      ensureApiSuccess(res.data, t('修改授权失败'));
      await Promise.all([facts.refetch(), available.refetch()]);
      await queryClient.invalidateQueries({ queryKey: ['available-routing-groups'] });
      toast.success(t('分组设置已更新'));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('修改授权失败'));
      await facts.refetch();
    } finally { setSaving(false); }
  };
  const setUserPrice = async () => {
    const ratio = Number(priceRatio);
    if (!priceGroupId || !Number.isFinite(ratio) || ratio <= 0) {
      toast.error(t('用户倍率必须是大于 0 的数字'));
      return;
    }
    setSaving(true);
    try {
      const res = await adminApiClient.put(`/v1/admin/routing-groups/${priceGroupId}/user-price/${userId}`, { price_ratio: ratio });
      ensureApiSuccess(res.data, t('设置用户倍率失败'));
      await available.refetch();
      toast.success(t('用户倍率已更新，仅影响之后建立的预扣'));
      setPriceRatio('');
    } catch (error) { toast.error(error instanceof Error ? error.message : t('设置用户倍率失败')); }
    finally { setSaving(false); }
  };
  const clearUserPrice = async () => {
    if (!priceGroupId) return;
    setSaving(true);
    try {
      const res = await adminApiClient.delete(`/v1/admin/routing-groups/${priceGroupId}/user-price/${userId}`);
      ensureApiSuccess(res.data, t('清除用户倍率失败'));
      await available.refetch();
      toast.success(t('用户倍率已清除'));
    } catch (error) { toast.error(error instanceof Error ? error.message : t('清除用户倍率失败')); }
    finally { setSaving(false); }
  };
  return <>
    <Button variant="outline" size="sm" onClick={() => setOpen(true)}>{t('分组授权')}</Button>
    <Dialog open={open} onOpenChange={setOpen}><DialogContent className="max-h-[85vh] overflow-y-auto"><DialogHeader><DialogTitle>{t('默认分组与分组授权')}</DialogTitle><DialogDescription>{t('默认分组只影响跟随默认的 Key；撤销一项授权后，其他有效来源仍可提供访问资格。')}</DialogDescription></DialogHeader>
      {(facts.isError || groups.isError || available.isError) && <p role="alert">{t('分组设置暂不可用')}</p>}
      {facts.data && <div className="space-y-4">
        <p>{t('默认分组：')}{groups.data?.find((g) => g.id === facts.data.default_routing_group_id)?.key || `#${facts.data.default_routing_group_id}`}</p>
        <label className="block">{t('公开组访问')}<select aria-label={t('公开组访问')} disabled={saving} className="ml-2 rounded border p-1" value={facts.data.public_group_access} onChange={(e) => change({ operation: 'public_access', public_group_access: e.target.value })}><option value="explicit_only">{t('仅显式授权')}</option><option value="all">{t('允许所有公开组')}</option></select></label>
        <label className="block">{t('目标分组')}<select aria-label={t('目标分组')} className="ml-2 rounded border p-1" value={groupId} onChange={(e) => setGroupId(e.target.value)}><option value="">{t('请选择')}</option>{groups.data?.filter((g) => g.status === 'enabled').map((g) => <option key={g.id} value={g.id}>{g.display_name || g.key}</option>)}</select></label>
        <label className="block">{t('授权到期时间（留空为永久）')}<input aria-label={t('授权到期时间（留空为永久）')} type="datetime-local" value={expires} onChange={(e) => setExpires(e.target.value)} className="block rounded border p-1" /></label>
        <div className="flex gap-2"><Button disabled={saving || !groupId} onClick={() => change({ operation: 'grant', routing_group_id: Number(groupId), source_type: 'admin', source_ref: 'manual', expires_at: expires ? Math.floor(new Date(expires).getTime() / 1000) : 0 })}>{t('授予访问')}</Button><Button variant="outline" disabled={saving || !groupId} onClick={() => change({ operation: 'default', routing_group_id: Number(groupId) })}>{t('设为默认')}</Button></div>
        <section className="space-y-3 border-t pt-3" aria-label={t('当前可用分组与有效价格')}>
          <h3 className="text-sm font-semibold">{t('当前可用分组与有效价格')}</h3>
          <p className="text-sm text-muted-foreground">{t('报价是读取时结果；实际扣费以请求预扣时冻结的快照为准。')}</p>
          {available.isPending ? <p role="status">{t('加载中...')}</p> : !available.data?.length ? <p>{t('暂无可用分组')}</p> : available.data.map((group) => <div key={group.id} className="rounded-md border p-3 text-sm">
            <p className="font-medium">{group.display_name || group.key}<span className="ml-2 font-mono text-xs text-muted-foreground">{group.key}</span></p>
            <p>{t('资格来源：')}{group.sources.map(sourceLabel).join('、') || t('未知')}</p>
            <p>{t('有效倍率：')}×{group.price_ratio} · {priceSourceLabel(group.price_source)}{group.price_version ? ` · ${group.price_version}` : ''}</p>
            <p>{t('结算：')}{billingModeLabel(group.billing_mode)}{group.subscription_covered ? ` · ${t('当前订阅覆盖')}` : ''}</p>
            <p>{t('模型：')}{group.models.join('、') || t('暂无可用模型')}</p>
          </div>)}
        </section>
        <div className="space-y-2 border-t pt-3">
          <p className="text-sm text-muted-foreground">{t('用户专属倍率：替换该组对该用户的计费倍率（不与其他倍率相乘），在途请求按既有快照结算。')}</p>
          <div className="flex flex-wrap items-center gap-2">
            <select aria-label={t('倍率目标分组')} className="rounded border p-1" value={priceGroupId} onChange={(e) => setPriceGroupId(e.target.value)}><option value="">{t('请选择分组')}</option>{groups.data?.filter((g) => g.status !== 'archived').map((g) => <option key={g.id} value={g.id}>{g.display_name || g.key}</option>)}</select>
            <input aria-label={t('用户倍率')} className="w-24 rounded border p-1" placeholder={t('如 0.8')} value={priceRatio} onChange={(e) => setPriceRatio(e.target.value)} />
            <Button size="sm" disabled={saving || !priceGroupId} onClick={setUserPrice}>{t('设置倍率')}</Button>
            <Button size="sm" variant="outline" disabled={saving || !priceGroupId} onClick={clearUserPrice}>{t('清除倍率')}</Button>
          </div>
        </div>
        {facts.data.grants.map((g) => <div key={`${g.routing_group_id}/${g.source_type}/${g.source_ref}`} className="border-t pt-2 text-sm">
          <p>{groups.data?.find((group) => group.id === g.routing_group_id)?.key || `#${g.routing_group_id}`} · {sourceLabel(g)} · {g.status}</p>
          <p>{g.expires_at ? t('有效至 {time}', { time: new Date(g.expires_at * 1000).toLocaleString() }) : t('永久授权')}</p>
          <p>{t('关联 Key：')}{facts.data.tokens?.filter((token) => token.mode === 'fixed' ? token.group_id === g.routing_group_id : facts.data.default_routing_group_id === g.routing_group_id).map((token) => token.name).join('、') || t('无')}</p>
          {g.source_type === 'subscription' && <p>{t('此来源由订阅管理；撤销订阅或到期后自动失效。')}</p>}
          {g.status === 'active' && g.source_type !== 'subscription' && <Button size="sm" variant="destructive" disabled={saving} onClick={() => change({ operation: 'revoke', ...g })}>{t('撤销此来源')}</Button>}
        </div>)}
      </div>}
    </DialogContent></Dialog>
  </>;
}
