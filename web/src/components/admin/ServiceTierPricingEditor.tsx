import { useState } from 'react';
import { toast } from 'sonner';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { PermissionButton as Button } from '@/components/admin/PermissionButton';
import { useAuthorization } from '@/lib/authorization';
import { adminApiClient } from '@/lib/api';
import { ensureApiSuccess } from '@/lib/api-response';
import { t } from '@/lib/i18n';
import { LONG_CONTEXT_PRICING_EXAMPLE, prettyServiceTierPricing, SERVICE_TIER_PRICING_EXAMPLE, SERVICE_TIER_PRICING_KEY, TIME_PRICING_EXAMPLE, validateServiceTierPricing } from '@/lib/service-tier-pricing';

interface Option { key: string; value: string; revision?: string; restricted?: boolean }
interface Props { options?: Option[]; ready: boolean; refresh: () => Promise<Option[] | undefined> }

const READ_PERMISSIONS = ['billing.pricing.read', 'billing.upstream_cost.read'];
const WRITE_PERMISSIONS = ['system.option.update', 'system.option.pricing.update', 'billing.pricing.update', 'billing.upstream_cost.update'];

export function ServiceTierPricingEditor({ options, ready, refresh }: Props) {
  const auth = useAuthorization();
  const [draft, setDraft] = useState<{ raw: string; revision: string; generation: string } | null>(null);
  const [saving, setSaving] = useState(false);
  const [needsReadback, setNeedsReadback] = useState(false);
  const saved = options?.find(option => option.key === SERVICE_TIER_PRICING_KEY);
  const canRead = auth.canAll(READ_PERMISSIONS) && !saved?.restricted;
  const activeDraft = canRead && draft?.generation === auth.generation ? draft : null;
  const raw = canRead ? activeDraft?.raw ?? prettyServiceTierPricing(saved?.value || '{"enabled":false}') : '';
  const editable = ready && canRead && auth.canAll(WRITE_PERMISSIONS) && !saving && !needsReadback;
  const replaceDraft = (value: string) => {
    if (!editable) return;
    setDraft({ raw: value, revision: activeDraft?.revision ?? saved?.revision ?? '0', generation: auth.generation });
  };
  const refreshConfig = async () => {
    const result = await refresh();
    if (result) { setDraft(null); setNeedsReadback(false); }
  };
  const save = async () => {
    if (!editable) return;
    let value: string;
    try { value = validateServiceTierPricing(raw); } catch (error) { toast.error(error instanceof Error ? error.message : t('档位配置无效')); return; }
    const reason = auth.snapshot?.authorization_mode === 'iam' ? window.prompt(t('请输入此次变更的原因'))?.trim() : undefined;
    if (auth.snapshot?.authorization_mode === 'iam' && !reason) { toast.error(t('变更原因不能为空')); return; }
    setSaving(true);
    setNeedsReadback(true);
    try {
      const response = await adminApiClient.put('/option/', {
        values: { [SERVICE_TIER_PRICING_KEY]: value },
        expected_revisions: { [SERVICE_TIER_PRICING_KEY]: activeDraft?.revision ?? saved?.revision ?? '0' },
        reason,
      });
      ensureApiSuccess(response.data, t('保存档位价格失败'));
      const result = await refresh();
      if (result?.find(option => option.key === SERVICE_TIER_PRICING_KEY)?.value !== value) {
        toast.error(t('请刷新档位配置后再继续编辑。'));
        return;
      }
      setDraft(null);
      setNeedsReadback(false);
      toast.success(t('档位价格已保存'));
    } catch (error) { toast.error(error instanceof Error ? error.message : t('保存档位价格失败')); }
    finally { setSaving(false); }
  };

  return <Card>
    <CardHeader>
      <CardTitle>{t('服务档位计价')}</CardTitle>
      <CardDescription>{t('首期仅支持一个 channel 来源与模型。默认关闭；启用前核对真实上游语义与价格。')}</CardDescription>
    </CardHeader>
    <CardContent className="space-y-3">
      <p className="text-sm text-muted-foreground">{t('prices 为售价，upstream_prices 为可选采购估价；每组需完整保存 default、priority、flex。全部单价单位为 USD/token（每 1M tokens 价格除以 1000000）。')}</p>
      <p className="text-sm text-muted-foreground">{t('每档必填 input_price、output_price、cache_read_price；可选 cache_creation_5m_price、cache_creation_1h_price。采购估价不代表供应商实扣。')}</p>
      <p className="text-sm text-muted-foreground">{t('请求与实际档位分别记录；实际档位未知时保持未知，按冻结的逐桶最高价结算。预扣覆盖最高允许档位，改价不重算历史账单。')}</p>
      <p className="text-sm text-muted-foreground">{t('长上下文使用 long_context（version=2；叠加分时计价时为 version=3）。实际输入总量包含非缓存输入、缓存读取及两类缓存创建；仅当总量严格大于 threshold_tokens 时，整单五桶（含输出）使用长上下文价格，等于阈值仍使用基础价格。')}</p>
      <p className="text-sm text-muted-foreground">{t('长上下文三档均需完整填写五桶单价，售价不得低于基础价格。预扣覆盖基础与长上下文的最高价；长档采购估价缺失时保持未知。示例默认关闭，阈值与真实价格确认后再启用。')}</p>
      <p className="text-sm text-muted-foreground">{t('长上下文示例指向 channel 11 / deepseek-flash；128000 仅为未确认的示例阈值，未填写真实价格。')}</p>
      <p className="text-sm text-muted-foreground">{t('分时计价使用 version=3 和 time_pricing，按预扣时刻及指定 IANA 时区冻结忙时或闲时，结算跨时段仍沿用冻结价格。根 prices 和 long_context 为闲时价格；peak_prices 为忙时价格，叠加长上下文时还须填写 peak_long_context_prices。')}</p>
      <p className="text-sm text-muted-foreground">{t('peak_windows 使用 ISO 星期（周一为 1）及日内分钟，起点包含、终点不包含；跨日须拆开。holidays 日期按闲时处理，须维护 calendar_valid_through；日历过期需更新后才能继续使用分时计价。忙闲时段示例均未确认，真实价格留空且默认关闭。')}</p>
      {!canRead ? <p>{t('查看档位配置需要售价与采购成本的读取权限。')}</p> : <>
        <label className="block text-sm font-medium" htmlFor="service-tier-pricing-json">{t('档位价格 JSON')}</label>
        <textarea id="service-tier-pricing-json" className="min-h-64 w-full rounded-md border bg-background p-3 font-mono text-xs" spellCheck={false} disabled={!editable} value={raw} onChange={event => replaceDraft(event.target.value)} />
        {needsReadback && <p role="alert" className="text-sm">{t('请刷新档位配置后再继续编辑。')}</p>}
        <div className="flex flex-wrap gap-2">
          <Button permission={WRITE_PERMISSIONS} variant="outline" disabled={!editable} onClick={() => replaceDraft(SERVICE_TIER_PRICING_EXAMPLE)}>{t('填入关闭状态示例')}</Button>
          <Button permission={WRITE_PERMISSIONS} variant="outline" disabled={!editable} onClick={() => replaceDraft(LONG_CONTEXT_PRICING_EXAMPLE)}>{t('填入长上下文关闭示例')}</Button>
          <Button permission={WRITE_PERMISSIONS} variant="outline" disabled={!editable} onClick={() => replaceDraft(TIME_PRICING_EXAMPLE)}>{t('填入忙闲分时关闭示例')}</Button>
          <Button permission={WRITE_PERMISSIONS} disabled={!editable} onClick={save}>{saving ? t('保存中...') : t('保存档位价格')}</Button>
          {needsReadback && <Button variant="outline" disabled={saving || !ready} onClick={refreshConfig}>{t('刷新档位配置')}</Button>}
        </div>
      </>}
    </CardContent>
  </Card>;
}
