import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { adminApiClient } from '@/lib/api';
import { unwrapApiData } from '@/lib/api-response';
import { t } from '@/lib/i18n';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { toast } from 'sonner';
interface Policy { version: number; billing_mode: string; price_ratio: number }
export function RoutingBillingEditor({ id }: { id: number }) {
  const [draft, setDraft] = useState<Policy | null>(null);
  const [saving, setSaving] = useState(false);
  const policy = useQuery({ queryKey: ['routing-billing', id], queryFn: async () => unwrapApiData<Policy>((await adminApiClient.get(`/v1/admin/routing-groups/${id}/billing`)).data), retry: false });
  const value = draft ?? policy.data;
  if (policy.isPending) return <p>{t('结算策略加载中…')}</p>;
  if (!value || policy.isError) return <p className="text-sm text-muted-foreground">{t('结算策略暂不可用，请检查服务是否启用订阅权益。')}</p>;
  return <form className="space-y-3 rounded-md border p-3" onSubmit={async (e) => {
    e.preventDefault(); setSaving(true);
    try {
      unwrapApiData((await adminApiClient.patch(`/v1/admin/routing-groups/${id}/billing`, value)).data);
      await policy.refetch(); setDraft(null); toast.success(t('结算策略已发布'));
    } catch (err) { toast.error(err instanceof Error ? err.message : t('发布失败，请刷新后重试')); }
    finally { setSaving(false); }
  }}>
    <p className="font-medium">{t('结算策略 · 版本 {version}', { version: value.version })}</p>
    <label className="block text-sm">{t('结算方式')} <select aria-label={t('结算方式')} value={value.billing_mode} onChange={(e) => setDraft({ ...value, billing_mode: e.target.value })} className="rounded border bg-background p-2">
      <option value="wallet_only">{t('仅钱包')}</option><option value="subscription_only">{t('仅订阅')}</option><option value="subscription_first">{t('订阅优先，余额补足')}</option>
    </select></label>
    <label className="block text-sm">{t('路由价格倍率')}<Input aria-label={t('路由价格倍率')} type="number" min="0.000001" step="any" required value={value.price_ratio} onChange={(e) => setDraft({ ...value, price_ratio: Number(e.target.value) })} /></label>
    <p className="text-xs text-muted-foreground">{t('改价只影响新请求。有订阅、在售套餐或待履约订单引用时禁止切换结算方式。仅订阅目前支持 OpenAI 文本 embeddings 白名单模型；其他请求在调用上游前拒绝。')}</p>
    <Button disabled={saving || !draft} type="submit">{t('发布结算策略')}</Button>
  </form>;
}
