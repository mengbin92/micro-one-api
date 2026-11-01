import { Fragment } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Database } from 'lucide-react';
import { EmptyState } from '@/components/EmptyState';
import { TableSkeleton } from '@/components/LoadingStates';
import { ModalityFlow } from '@/components/ModalityIcons';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { apiClient } from '@/lib/api';
import { unwrapApiData } from '@/lib/api-response';
import { locale, t } from '@/lib/i18n';
import { PRICE_FIELDS, SERVICE_TIERS } from '@/lib/service-tier-pricing';

interface ModelPrice {
  input_price?: number;
  output_price?: number;
  cache_read_price?: number | null;
  cache_creation_5m_price?: number | null;
  cache_creation_1h_price?: number | null;
}

type TierPrices = Record<(typeof SERVICE_TIERS)[number], ModelPrice>;

interface PricingRow extends ModelPrice {
  model: string;
  service_tier_prices?: TierPrices;
  long_context?: { threshold_tokens: number; prices: TierPrices };
  time_pricing?: {
    timezone: string;
    calendar_valid_through: string;
    holidays: string[];
    peak_windows: { weekdays: number[]; start_minute: number; end_minute: number }[];
    peak_prices: TierPrices;
    peak_long_context_prices?: TierPrices;
  };
  input_modalities?: string[];
  output_modalities?: string[];
}

interface PricingPayload {
  prices?: PricingRow[];
  unit?: string;
}

function normalizePrice(value: unknown) {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : undefined;
}

function normalizeRows(rows: PricingRow[] | undefined) {
  if (!Array.isArray(rows)) return [];
  return rows
    .map((row) => ({
      ...row,
      model: String(row.model || '').trim(),
      input_price: normalizePrice(row.input_price),
      output_price: normalizePrice(row.output_price),
      cache_read_price: normalizePrice(row.cache_read_price),
      input_modalities: Array.isArray(row.input_modalities) ? row.input_modalities : [],
      output_modalities: Array.isArray(row.output_modalities) ? row.output_modalities : [],
    }))
    .filter((row) => row.model)
    .sort((a, b) => a.model.localeCompare(b.model));
}

function formatPrice(value: number | undefined, unit: string) {
  if (value === undefined) return '-';
  return `$${value.toLocaleString(locale(), {
    minimumFractionDigits: 2,
    maximumFractionDigits: 6,
  })} / ${unit}`;
}

function priceVariants(row: PricingRow) {
  if (!row.service_tier_prices) return [{ key: 'base', band: '', context: '', tiers: '', price: row }];
  const bands = row.time_pricing
    ? [{ key: 'peak', label: t('忙时'), prices: row.time_pricing.peak_prices, long: row.time_pricing.peak_long_context_prices },
      { key: 'off_peak', label: t('闲时'), prices: row.service_tier_prices, long: row.long_context?.prices }]
    : [{ key: 'base', label: '', prices: row.service_tier_prices, long: row.long_context?.prices }];
  return bands.flatMap(band => [
    { key: 'base', label: row.long_context ? t('输入 ≤ {tokens} tokens', { tokens: row.long_context.threshold_tokens.toLocaleString(locale()) }) : '', prices: band.prices },
    ...(row.long_context ? [{ key: 'long', label: t('输入 > {tokens} tokens（整单）', { tokens: row.long_context.threshold_tokens.toLocaleString(locale()) }), prices: band.long }] : []),
  ].flatMap(context => {
    // Include undisplayed cache-creation buckets when deciding which tiers can share a row.
    const groups = new Map<string, { tiers: string[]; price: ModelPrice }>();
    for (const tier of SERVICE_TIERS) {
      const price = context.prices?.[tier];
      if (!price) continue;
      const key = PRICE_FIELDS.map(field => normalizePrice(price[field])).join('|');
      const group = groups.get(key);
      if (group) group.tiers.push(tier);
      else groups.set(key, { tiers: [tier], price });
    }
    return [...groups.values()].map(group => ({ key: `${band.key}-${context.key}-${group.tiers.join('-')}`, band: band.label, context: context.label, tiers: group.tiers.length === SERVICE_TIERS.length ? t('全部档位') : group.tiers.join(' / '), price: group.price }));
  }));
}

function formatMinute(minute: number) {
  return `${String(Math.floor(minute / 60)).padStart(2, '0')}:${String(minute % 60).padStart(2, '0')}`;
}

export function PricingPage() {
  const { data, isLoading, isError } = useQuery({
    queryKey: ['readonly-pricing'],
    queryFn: async ({ signal }) => {
      const res = await apiClient.get('/pricing', { signal });
      return unwrapApiData<PricingPayload>(res.data);
    },
  });

  const unit = data?.unit || '1M tokens';
  const rows = normalizeRows(isError ? undefined : data?.prices);
  const hasTariffs = rows.some(row => row.service_tier_prices);

  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h2 className="text-2xl font-black tracking-normal text-slate-950 dark:text-white">{t("模型价格")}</h2>
          <p className="mt-1 text-sm font-medium text-slate-500 dark:text-slate-400">{t("当前可用模型的输入、输出和缓存读取价格。")}</p>
        </div>
        <div className="inline-flex w-fit items-center gap-2 rounded-xl border border-border bg-card px-3 py-2 text-sm font-bold text-foreground shadow-surface-sm">
          <Database className="size-4 text-blue-600" />
          {rows.length.toLocaleString(locale())}{t("个模型")}</div>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>{t("当前价格")}</CardTitle>
          <CardDescription>{t("价格单位为每")}{unit}。</CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <TableSkeleton columns={[t("模型名称"), t("输入/输出模态"), t("输入价格"), t("输出价格"), t("缓存读取")]} rows={8} />
          ) : isError ? (
            <EmptyState title={t("价格加载失败")} description={t("请稍后刷新页面重试。")} />
          ) : rows.length === 0 ? (
            <EmptyState title={t("暂无模型价格")} description={t("管理员配置价格后会显示在这里。")} />
          ) : (
            <div className="overflow-x-auto rounded-lg border">
              <Table>
                <TableHeader>
                  <TableRow className="bg-slate-50 hover:bg-slate-50 dark:bg-white/5">
                    <TableHead>{t("模型名称")}</TableHead>
                    {hasTariffs && <TableHead>{t("计价条件")}</TableHead>}
                    <TableHead>{t("输入/输出模态")}</TableHead>
                    <TableHead>{t("输入价格")}</TableHead>
                    <TableHead>{t("输出价格")}</TableHead>
                    <TableHead>{t("缓存读取")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {rows.map(row => {
                    const variants = priceVariants(row);
                    return <Fragment key={row.model}>{variants.map((variant, index) => (
                      <TableRow key={variant.key}>
                        {index === 0 && <TableCell rowSpan={variants.length} className="min-w-40 font-mono text-sm font-semibold">{row.model}</TableCell>}
                        {hasTariffs && <TableCell className="min-w-32">
                          <div className="font-semibold">{variant.band || variant.context || (variant.tiers ? t('基础价格') : '—')}</div>
                          {variant.tiers && <div className="text-xs text-muted-foreground">{variant.tiers}</div>}
                          {variant.band && variant.context && <div className="text-xs text-muted-foreground">{variant.context}</div>}
                        </TableCell>}
                        {index === 0 && <TableCell rowSpan={variants.length} className="min-w-28">
                          <ModalityFlow inputModalities={row.input_modalities} outputModalities={row.output_modalities} />
                        </TableCell>}
                        <TableCell className="min-w-36 font-semibold">{formatPrice(normalizePrice(variant.price.input_price), unit)}</TableCell>
                        <TableCell className="min-w-36 font-semibold">{formatPrice(normalizePrice(variant.price.output_price), unit)}</TableCell>
                        <TableCell className="min-w-36 font-semibold">{formatPrice(normalizePrice(variant.price.cache_read_price), unit)}</TableCell>
                      </TableRow>
                    ))}</Fragment>;
                  })}
                </TableBody>
              </Table>
            </div>
          )}
        </CardContent>
      </Card>

      {rows.filter(row => row.time_pricing).map(row => {
        const calendar = row.time_pricing!;
        const weekdays = [t('周一'), t('周二'), t('周三'), t('周四'), t('周五'), t('周六'), t('周日')];
        const separator = locale() === 'zh-CN' ? '、' : ', ';
        return <Card key={row.model}>
          <CardHeader>
            <CardTitle>{row.model} · {t('忙闲时规则')}</CardTitle>
            <CardDescription>{t('时区')}：<span>{calendar.timezone}</span> · {t('日历有效至 {date}', { date: calendar.calendar_valid_through })}</CardDescription>
          </CardHeader>
          <CardContent className="space-y-3 text-sm">
            <div className="flex flex-wrap gap-2">{calendar.peak_windows.map((window, index) => (
              <span key={index} className="rounded-lg bg-muted px-3 py-2">{window.weekdays.map(day => weekdays[day - 1]).join(separator)} · <span className="whitespace-nowrap">{formatMinute(window.start_minute)}–{formatMinute(window.end_minute)}</span></span>
            ))}</div>
            <p className="text-muted-foreground">{t('以上窗口为忙时，起点包含、终点不包含；其余时段及配置的节假日为闲时。')}</p>
            {calendar.holidays.length > 0 && <details>
              <summary className="cursor-pointer font-medium">{t('查看闲时节假日（{count} 天）', { count: calendar.holidays.length })}</summary>
              <p className="mt-2 break-words text-muted-foreground">{calendar.holidays.join(separator)}</p>
            </details>}
            <p className="text-muted-foreground">{t('按预扣时刻确定时段；实际服务档位未知时，按该时段各桶最高单价结算。')}</p>
          </CardContent>
        </Card>;
      })}
    </div>
  );
}
