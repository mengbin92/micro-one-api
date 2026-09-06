import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Activity, AlertTriangle, CheckCircle2, CreditCard, Database, KeyRound, LineChart, Scale, TrendingUp, Users } from 'lucide-react';

import { Link } from 'react-router';
import { EmptyState } from '@/components/EmptyState';
import { TableSkeleton } from '@/components/LoadingStates';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { adminApiClient } from '@/lib/api';
import { unwrapApiData } from '@/lib/api-response';
import { quotaPerUnitFromOptions, quotaToCurrencyUnits } from '@/lib/amount';
import { cn } from '@/lib/utils';
import { AccountStatusBadge } from '@/components/admin/AccountStatusBadge';
import {
  normalizeSubscriptionAccount,
  type RawSubscriptionAccount,
  type SubscriptionAccountSummary as AdminSubscriptionAccount,
} from '@/lib/subscription-account';
import { locale, t } from '@/lib/i18n';

interface AdminTotals {
  users?: number;
  active_users?: number;
  channels?: number;
  active_channels?: number;
  configured_models?: number;
  request_count?: number;
  quota_used?: number;
  upstream_cost?: number;
  gross_profit?: number;
  channel_balance?: number;
  stale_balance_channels?: number;
  log_count?: number;
  subscription_accounts?: number;
  active_subscription_accounts?: number;
}

interface AdminUser {
  id: string | number;
  username?: string;
  display_name?: string;
  displayName?: string;
  email?: string;
  group?: string;
  status?: number;
}



interface AdminChannel {
  id: string | number;
  name?: string;
  type?: number;
  group?: string;
  status?: number;
  models?: string;
  balance?: number;
  used_quota?: number;
  usedQuota?: string;
}

interface AdminLog {
  id: string | number;
  userId?: string;
  type?: string;
  amount?: number | string;
  modelName?: string;
  endpoint?: string;
  createdAt?: number;
  channelName?: string;
  channelTypeStr?: string;
  channelId?: number;
}

interface UsageAggregateItem {
  key?: string;
  user_id?: string;
  channel_id?: number;
  subscription_account_id?: number;
  platform?: string;
  model?: string;
  token_name?: string;
  name?: string;
  quota?: number;
  upstream_cost?: number;
  gross_profit?: number;
  prompt_tokens?: number;
  completion_tokens?: number;
  cache_read_tokens?: number;
  count?: number;
  balance?: number;
  status?: number;
}

interface SummaryAlert {
  type?: string;
  severity?: string;
  channel_id?: number;
  run_id?: number;
  message?: string;
}

interface CostAnalysis {
  revenue_quota?: number;
  upstream_cost?: number;
  gross_profit?: number;
  gross_margin?: number;
  profitable?: boolean;
}

interface ReconciliationSummary {
  run_id?: number;
  run_at?: number;
  discrepancy_count?: number;
}

interface AdminSummary {
  totals?: AdminTotals;
  recent_users?: AdminUser[];
  channels?: AdminChannel[];
  subscription_accounts?: RawSubscriptionAccount[];
  recent_logs?: AdminLog[];
  cost_analysis?: CostAnalysis;
  top_models?: UsageAggregateItem[];
  top_channels?: UsageAggregateItem[];
  top_users?: UsageAggregateItem[];
  top_tokens?: UsageAggregateItem[];
  top_subscription_accounts?: UsageAggregateItem[];
  alerts?: SummaryAlert[];
  latest_reconciliation?: ReconciliationSummary;
  model_catalog?: Array<{ id?: string; owned_by?: string }>;
  pricing_options?: Record<string, string>;
  payment_summary?: {
    recent_order_count?: number;
    recent_amount?: number;
    recent_amount_cents?: number;
    recent_amount_money_cents?: number;
  };
}

const PROVIDER_NAMES: Record<number, string> = {
  1: 'OpenAI',
  2: 'Anthropic',
  3: 'Azure',
  4: 'Gemini',
  14: 'DeepSeek',
  23: 'OpenRouter',
  32: 'CodexOAuth',
  33: 'ClaudeOAuth',
  34: 'ZhipuPlan',
  35: 'MinimaxPlan',
  36: 'KimiOAuth',
  37: 'SiliconFlow',
};

const LOG_TYPE_NAMES: Record<string, string> = {
  consume: '调用',
  recharge: '充值',
  redeem: '兑换',
  refund: '退款',
};

const SUBSCRIPTION_PLATFORM_LABELS: Record<string, string> = {
  claude: 'Claude',
  codex: 'Codex',
  zhipu: 'Zhipu GLM',
  minimax: 'MiniMax',
  kimi: 'Kimi',
};

function subscriptionPlatformLabel(platform?: string) {
  if (!platform) return '-';
  return SUBSCRIPTION_PLATFORM_LABELS[platform] ?? platform;
}

// Compact quota summary for the overview card. Mirrors the logic of
// QuotaStatusCell on the full page but condensed to a single row so it fits
// in the overview table.
const HOUR_S_OV = 3600;
const DAY_S_OV = 86400;
const WEEK_S_OV = 7 * DAY_S_OV;
const FIVE_H_S_OV = 5 * HOUR_S_OV;

function effectiveWindowUsedOV(used: number, windowStart: number | undefined, nowUnix: number, windowS: number): number {
  if (!windowStart || windowStart <= 0 || nowUnix - windowStart >= windowS) return 0;
  return used;
}

function SubscriptionQuotaMini({ account, nowUnix }: { account: AdminSubscriptionAccount; nowUnix: number }) {
  const rows: Array<{ label: string; used: number; limit: number }> = [];
  const pairs = [
    { label: t("总额"), used: account.quotaUsedUsd ?? 0, limit: account.quotaLimitUsd ?? 0 },
    { label: '5h', used: effectiveWindowUsedOV(account.quota5hUsedUsd ?? 0, account.quota5hWindowStart, nowUnix, FIVE_H_S_OV), limit: account.quota5hLimitUsd ?? 0 },
    { label: '24h', used: effectiveWindowUsedOV(account.quotaDailyUsedUsd ?? 0, account.quotaDailyWindowStart, nowUnix, DAY_S_OV), limit: account.quotaDailyLimitUsd ?? 0 },
    { label: '7d', used: effectiveWindowUsedOV(account.quotaWeeklyUsedUsd ?? 0, account.quotaWeeklyWindowStart, nowUnix, WEEK_S_OV), limit: account.quotaWeeklyLimitUsd ?? 0 },
  ];
  for (const p of pairs) {
    if (p.limit > 0 || p.used > 0) rows.push({ label: p.label, used: p.used, limit: p.limit });
  }

  // Upstream snapshot percent (single number)
  const upstreamPercent =
    account.primaryQuotaUsedPercent ?? account.secondaryQuotaUsedPercent ?? account.quotaUsedPercent ?? null;

  if (rows.length === 0 && upstreamPercent == null) {
    return <span className="text-xs text-muted-foreground">-</span>;
  }

  // Determine worst ratio for the summary badge color.
  let worstRatio = 0;
  for (const r of rows) {
    if (r.limit > 0) worstRatio = Math.max(worstRatio, r.used / r.limit);
  }
  if (upstreamPercent != null) worstRatio = Math.max(worstRatio, upstreamPercent / 100);
  const badgeClass =
    worstRatio >= 1
      ? 'bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-200'
      : worstRatio >= 0.8
        ? 'bg-amber-100 text-amber-800 dark:bg-amber-900 dark:text-amber-200'
        : 'bg-emerald-100 text-emerald-800 dark:bg-emerald-900 dark:text-emerald-200';

  return (
    <div className="min-w-[150px] space-y-1">
      {rows.map((row) => {
        const ratio = row.limit > 0 ? Math.min(row.used / row.limit, 1) : 0;
        const barColor = ratio >= 1 ? 'bg-red-500' : ratio >= 0.8 ? 'bg-amber-500' : 'bg-emerald-500';
        return (
          <div key={row.label} className="space-y-0.5">
            <div className="flex items-center justify-between gap-2 text-xs">
              <span className="font-medium">{row.label}</span>
              <span className="tabular-nums text-muted-foreground">
                ${row.used.toFixed(2)}
                {row.limit > 0 ? ` / $${row.limit.toFixed(2)}` : ''}
              </span>
            </div>
            {row.limit > 0 && (
              <div className="h-1.5 overflow-hidden rounded-full bg-muted">
                <div className={`${barColor} h-full transition-[width] duration-200 ease-standard motion-reduce:transition-none`} style={{ width: `${ratio * 100}%` }} />
              </div>
            )}
          </div>
        );
      })}
      {upstreamPercent != null && (
        <div className="space-y-0.5">
          <div className="flex items-center justify-between gap-2 text-xs">
            <span className="font-medium">{t("上游")}</span>
            <span className="tabular-nums text-muted-foreground">{upstreamPercent.toFixed(1)}%</span>
          </div>
          <div className="h-1.5 overflow-hidden rounded-full bg-muted">
            <div
              className={`${upstreamPercent >= 100 ? 'bg-red-500' : upstreamPercent >= 80 ? 'bg-amber-500' : 'bg-emerald-500'} h-full transition-[width] duration-200 ease-standard motion-reduce:transition-none`}
              style={{ width: `${Math.min(upstreamPercent, 100)}%` }}
            />
          </div>
        </div>
      )}
      <span className={`inline-flex rounded px-1.5 py-0.5 text-xs font-medium ${badgeClass}`}>
        {worstRatio >= 1 ? t("已耗尽") : worstRatio >= 0.8 ? t("即将耗尽") : t("正常")}
      </span>
    </div>
  );
}

function numberValue(value: unknown): number {
  const parsed = Number(value ?? 0);
  return Number.isFinite(parsed) ? parsed : 0;
}

function formatQuota(value?: number | string, quotaPerUnit?: number) {
  return quotaToCurrencyUnits(value, quotaPerUnit).toFixed(4);
}

function formatInteger(value?: number): string {
  return numberValue(value).toLocaleString(locale());
}

function formatCompactInteger(value?: number): string {
  const n = numberValue(value);
  if (Math.abs(n) >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (Math.abs(n) >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
  return n.toLocaleString(locale());
}

function formatMoneyCents(value?: number | string) {
  return `$${(numberValue(value) / 100).toFixed(2)}`;
}

function formatMargin(value?: number) {
  return `${(numberValue(value) * 100).toFixed(1)}%`;
}

function formatDate(value?: number | string) {
  const timestamp = numberValue(value);
  if (!timestamp) return '-';
  return new Date(timestamp * 1000).toLocaleString(locale());
}

function modelCount(channels: AdminChannel[]) {
  const models = new Set<string>();
  channels.forEach((channel) => {
    String(channel.models || '')
      .split(',')
      .map((item) => item.trim())
      .filter(Boolean)
      .forEach((model) => models.add(model));
  });
  return models.size;
}

function totalTokens(item: UsageAggregateItem) {
  return numberValue(item.prompt_tokens) + numberValue(item.completion_tokens) + numberValue(item.cache_read_tokens);
}

function StatCard({
  title,
  value,
  detail,
  icon: Icon,
}: {
  title: string;
  value: string;
  detail: string;
  icon: typeof Users;
}) {
  return (
    <Card className="min-h-40">
      <CardContent className="flex items-center gap-4 p-5">
        <div className="grid size-11 place-items-center rounded-lg bg-primary text-primary-foreground">
          <Icon className="size-5" />
        </div>
        <div className="min-w-0">
          <div className="text-sm font-medium text-muted-foreground">{title}</div>
          <div className="mt-1 truncate text-2xl font-semibold text-foreground">{value}</div>
          <div className="mt-1 text-xs font-medium text-muted-foreground">{detail}</div>
        </div>
      </CardContent>
    </Card>
  );
}

function CostCard({
  title,
  value,
  detail,
  icon: Icon,
  tone,
}: {
  title: string;
  value: string;
  detail: string;
  icon: typeof Users;
  tone: 'green' | 'red' | 'blue' | 'amber';
}) {
  const styles = {
    green: 'bg-emerald-50 text-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-300',
    red: 'bg-red-50 text-red-700 dark:bg-red-500/10 dark:text-red-300',
    blue: 'bg-blue-50 text-blue-700 dark:bg-blue-500/10 dark:text-blue-300',
    amber: 'bg-amber-50 text-amber-700 dark:bg-amber-500/10 dark:text-amber-300',
  }[tone];

  return (
    <Card className="min-h-40">
      <CardContent className="flex items-center gap-4 p-5">
        <div className={`grid size-11 place-items-center rounded-lg ${styles}`}>
          <Icon className="size-5" />
        </div>
        <div className="min-w-0">
          <div className="text-sm font-medium text-muted-foreground">{title}</div>
          <div className="mt-1 truncate text-2xl font-semibold text-foreground">{value}</div>
          <div className="mt-1 text-xs font-medium text-muted-foreground">{detail}</div>
        </div>
      </CardContent>
    </Card>
  );
}

const managementAreas = [
  {
    title: '资源与路由',
    description: '配置模型、渠道、订阅账号和流量分配。',
    icon: Database,
    links: [
      { label: '渠道', to: '/admin/channels' },
      { label: '模型', to: '/admin/models' },
      { label: '订阅账号', to: '/admin/subscription-accounts' },
      { label: '路由策略', to: '/admin/routing-ops' },
    ],
  },
  {
    title: '监控与分析',
    description: '定位渠道、模型、调用和经营异常。',
    icon: Activity,
    links: [
      { label: '渠道健康', to: '/admin/channel-health' },
      { label: '模型健康', to: '/admin/model-health' },
      { label: '调用日志', to: '/admin/logs' },
      { label: '经营分析', to: '/admin/cost-analysis' },
    ],
  },
  {
    title: '用户与产品',
    description: '管理用户、套餐、订阅和兑换权益。',
    icon: Users,
    links: [
      { label: '用户', to: '/admin/users' },
      { label: '订阅套餐', to: '/admin/subscription-plans' },
      { label: '用户订阅', to: '/admin/subscriptions' },
      { label: '兑换码', to: '/admin/redemptions' },
    ],
  },
  {
    title: '计费与财务',
    description: '维护定价、成本、支付订单和账务核对。',
    icon: CreditCard,
    links: [
      { label: '销售定价', to: '/admin/pricing' },
      { label: '上游成本', to: '/admin/upstream-costs' },
      { label: '支付订单', to: '/admin/payment-orders' },
      { label: '账务对账', to: '/admin/reconciliation' },
    ],
  },
] as const;

function topItemLabel(item: UsageAggregateItem, kind: TopUsageKind) {
  if (kind === 'model') return item.model || item.key || '-';
  if (kind === 'channel') return item.name || (item.channel_id ? `#${item.channel_id}` : item.key || '-');
  if (kind === 'subscription_account') {
    const name = item.name || (item.subscription_account_id ? `#${item.subscription_account_id}` : item.key || '-');
    return item.platform ? `${name} (${subscriptionPlatformLabel(item.platform)})` : name;
  }
  if (kind === 'token') return item.token_name || item.key || '-';
  return item.user_id || item.key || '-';
}

type TopUsageKind = 'model' | 'channel' | 'user' | 'token' | 'subscription_account';

function TopUsageList({
  kind,
  items,
  isLoading,
  emptyTitle,
  emptyDescription,
  quotaPerUnit,
}: {
  kind: TopUsageKind;
  items: UsageAggregateItem[];
  isLoading: boolean;
  emptyTitle: string;
  emptyDescription: string;
  quotaPerUnit: number;
}) {
  const maxQuota = Math.max(1, ...items.map((item) => Math.abs(numberValue(item.quota))));
  const barStyles = {
    model: 'bg-blue-600 dark:bg-blue-400',
    channel: 'bg-emerald-600 dark:bg-emerald-400',
    user: 'bg-orange-600 dark:bg-orange-400',
    token: 'bg-violet-600 dark:bg-violet-400',
    subscription_account: 'bg-cyan-600 dark:bg-cyan-400',
  }[kind];

  if (isLoading) {
    return <TableSkeleton columns={[t("对象"), t("消耗"), t("占比")]} rows={5} />;
  }
  if (items.length === 0) {
    return <EmptyState title={emptyTitle} description={emptyDescription} />;
  }
  return (
    <div className="space-y-4">
      {items.map((item, index) => {
        const quota = Math.abs(numberValue(item.quota));
        const width = `${Math.max(4, (quota / maxQuota) * 100)}%`;
        const label = topItemLabel(item, kind);
        return (
          <div key={`${kind}-${item.key || item.user_id || item.channel_id || item.model || item.token_name || index}`} className="space-y-2">
            <div className="flex items-center justify-between gap-3">
              <div className="flex min-w-0 items-center gap-2">
                <span className="grid size-6 shrink-0 place-items-center rounded-md bg-muted text-xs font-semibold text-muted-foreground">
                  {index + 1}
                </span>
                <span className="truncate text-sm font-semibold text-foreground" title={label}>
                  {label}
                </span>
              </div>
              <span className="shrink-0 text-sm font-semibold text-foreground">
                {formatQuota(quota, quotaPerUnit)}
              </span>
            </div>
            <div className="h-2.5 overflow-hidden rounded-full bg-muted">
              <div className={`h-full rounded-full ${barStyles}`} style={{ width }} />
            </div>
            <div className="flex items-center justify-between gap-3 text-xs font-medium text-muted-foreground">
              <span>{formatCompactInteger(item.count)}{t("次请求")}</span>
              <span>{formatCompactInteger(totalTokens(item))} tokens</span>
            </div>
          </div>
        );
      })}
    </div>
  );
}

export function AdminOverviewPage() {
  const { data, isLoading, dataUpdatedAt } = useQuery({
    queryKey: ['admin-summary'],
    queryFn: async () => {
      const res = await adminApiClient.get('/admin/summary');
      return unwrapApiData<AdminSummary>(res.data);
    },
  });
  const [rankingTab, setRankingTab] = useState<TopUsageKind>('user');

  // Render clock derived from the query fetch time (pure render).
  const nowUnix = dataUpdatedAt ? Math.floor(dataUpdatedAt / 1000) : 0;

  const totals = data?.totals ?? {};
  const channels = data?.channels ?? [];
  const subscriptionAccounts = useMemo(
    () => (data?.subscription_accounts ?? []).map(normalizeSubscriptionAccount),
    [data],
  );
  const logs = data?.recent_logs ?? [];
  const users = data?.recent_users ?? [];
  const costAnalysis = data?.cost_analysis ?? {};
  const topModels = data?.top_models ?? [];
  const topChannels = data?.top_channels ?? [];
  const topUsers = data?.top_users ?? [];
  const topTokens = data?.top_tokens ?? [];
  const topSubscriptionAccounts = data?.top_subscription_accounts ?? [];
  const alerts = data?.alerts ?? [];
  const latestReconciliation = data?.latest_reconciliation;
  const quotaPerUnit = quotaPerUnitFromOptions(data?.pricing_options);
  const configuredModels = totals.configured_models || modelCount(channels) || data?.model_catalog?.length || 0;
  const paymentAmountCents =
    data?.payment_summary?.recent_amount_cents ??
    data?.payment_summary?.recent_amount_money_cents ??
    data?.payment_summary?.recent_amount ??
    0;

  const rankingTabs: Array<{
    key: TopUsageKind;
    label: string;
    items: UsageAggregateItem[];
    emptyTitle: string;
    emptyDescription: string;
  }> = [
    { key: 'user', label: t("用户"), items: topUsers, emptyTitle: t("暂无用户用量"), emptyDescription: t("产生调用后会显示高消耗用户。") },
    { key: 'model', label: t("模型"), items: topModels, emptyTitle: t("暂无模型用量"), emptyDescription: t("产生调用后会显示模型消耗排行。") },
    { key: 'channel', label: t("渠道"), items: topChannels, emptyTitle: t("暂无渠道用量"), emptyDescription: t("渠道产生调用后会显示消耗排行。") },
    { key: 'token', label: 'Token', items: topTokens, emptyTitle: t("暂无 Token 用量"), emptyDescription: t("API Token 产生调用后会显示消耗排行。") },
    { key: 'subscription_account', label: t("订阅账号"), items: topSubscriptionAccounts, emptyTitle: t("暂无订阅账号用量"), emptyDescription: t("订阅账号产生调用后会显示消耗排行。") },
  ];
  const activeRanking = rankingTabs.find((tab) => tab.key === rankingTab) ?? rankingTabs[0];

  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h2 className="text-2xl font-bold tracking-normal text-foreground">{t("运营总览")}</h2>
          <p className="mt-1 text-sm font-medium text-muted-foreground">{t("按业务域进入管理，先处理异常，再查看运营指标。")}</p>
        </div>
      </div>

      {isLoading ? (
        <Card>
          <CardContent className="p-4">
            <TableSkeleton columns={[t("类型"), t("对象")]} rows={3} />
          </CardContent>
        </Card>
      ) : alerts.length > 0 ? (
        <Card>
          <CardHeader className="border-b border-border">
            <CardTitle role="heading" aria-level={3}>{t("风险告警")}</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3 p-4">
            {alerts.slice(0, 5).map((alert, index) => (
              <div key={`${alert.type}-${alert.channel_id || alert.run_id || index}`} className="rounded-lg border border-amber-200 bg-amber-50 p-3 dark:border-amber-500/30 dark:bg-amber-500/10">
                <div className="flex items-center gap-2 text-sm font-bold text-amber-800 dark:text-amber-200">
                  <AlertTriangle className="size-4" />
                  {alert.message || alert.type || t("告警")}
                </div>
                <div className="mt-1 text-xs font-medium text-amber-700/80 dark:text-amber-200/80">
                  {alert.channel_id ? t(`渠道 #${alert.channel_id}`) : alert.run_id ? t(`对账 #${alert.run_id}`) : alert.severity || '-'}
                </div>
              </div>
            ))}
            {latestReconciliation?.run_id ? (
              <Button variant="outline" size="sm" nativeButton={false} render={<Link to="/admin/reconciliation" />}>
                <Scale className="size-4" />{t("查看对账")}</Button>
            ) : null}
          </CardContent>
        </Card>
      ) : (
        <div className="flex flex-wrap items-center gap-x-6 gap-y-2 rounded-xl border border-border bg-card px-4 py-3 text-sm">
          <span className="flex items-center gap-2 font-medium text-emerald-600 dark:text-emerald-300">
            <CheckCircle2 className="size-4" />{t("运行正常，暂无告警")}
          </span>
          <span className="text-muted-foreground">
            {t("渠道余额")} <span className="font-semibold text-foreground">${numberValue(totals.channel_balance).toFixed(2)}</span>
          </span>
          <span className="text-muted-foreground">
            {t("可用模型")} <span className="font-semibold text-foreground">{configuredModels}</span>
          </span>
          <span className="text-muted-foreground">
            {latestReconciliation?.run_id ? t(`最近对账 #${latestReconciliation.run_id}`) : t("暂无对账记录")}
          </span>
          {latestReconciliation?.run_id ? (
            <Link to="/admin/reconciliation" className="font-medium text-foreground hover:underline">{t("查看对账")}</Link>
          ) : null}
        </div>
      )}

      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard
          title={t("用户")}
          value={formatInteger(totals.users)}
          detail={t(`${formatInteger(totals.active_users)} 个启用用户`)}
          icon={Users}
        />
        <StatCard
          title={t("上游供应商")}
          value={formatInteger(totals.channels)}
          detail={t(`${formatInteger(totals.active_channels)} 个启用渠道`)}
          icon={Database}
        />
        <StatCard
          title={t("订阅账号")}
          value={formatInteger(totals.subscription_accounts)}
          detail={t(`${formatInteger(totals.active_subscription_accounts)} 个启用账号`)}
          icon={KeyRound}
        />
        <StatCard
          title={t("调用请求")}
          value={formatInteger(totals.request_count)}
          detail={t(`${formatInteger(totals.log_count)} 条账务日志`)}
          icon={Activity}
        />
        <CostCard
          title={t("用户侧收入")}
          value={formatQuota(costAnalysis.revenue_quota ?? totals.quota_used, quotaPerUnit)}
          detail={t("consume 账本计费金额")}
          icon={TrendingUp}
          tone="green"
        />
        <CostCard
          title={t("上游成本")}
          value={formatQuota(costAnalysis.upstream_cost ?? totals.upstream_cost, quotaPerUnit)}
          detail={t("渠道侧成本汇总")}
          icon={Database}
          tone="blue"
        />
        <CostCard
          title={t("毛利")}
          value={formatQuota(costAnalysis.gross_profit ?? totals.gross_profit, quotaPerUnit)}
          detail={t(`毛利率 ${formatMargin(costAnalysis.gross_margin)}`)}
          icon={LineChart}
          tone={numberValue(costAnalysis.gross_profit ?? totals.gross_profit) >= 0 ? 'green' : 'red'}
        />
        <StatCard
          title={t("账务记录")}
          value={formatMoneyCents(paymentAmountCents)}
          detail={t(`${formatInteger(data?.payment_summary?.recent_order_count)} 条近期充值/兑换/退款`)}
          icon={CreditCard}
        />
      </div>

      <Card>
        <CardContent className="space-y-3 p-4">
          {managementAreas.map((area) => {
            const Icon = area.icon;
            return (
              <div key={area.title} className="flex flex-wrap items-center gap-2">
                <span className="flex w-32 shrink-0 items-center gap-2 text-sm font-semibold text-foreground">
                  <Icon className="size-4 text-muted-foreground" />{t(area.title)}
                </span>
                {area.links.map((link) => (
                  <Link key={link.to} to={link.to} className="rounded-lg bg-muted px-3 py-1.5 text-sm font-medium text-foreground transition-colors hover:bg-accent hover:text-accent-foreground">
                    {t(link.label)}
                  </Link>
                ))}
              </div>
            );
          })}
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="border-b border-border">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <CardTitle role="heading" aria-level={3}>{t("消耗排行")}</CardTitle>
            <div className="flex flex-wrap gap-1 rounded-lg bg-muted p-1" role="tablist" aria-label={t("消耗排行维度")}>
              {rankingTabs.map((tab) => (
                <button
                  key={tab.key}
                  type="button"
                  role="tab"
                  aria-selected={tab.key === rankingTab}
                  onClick={() => setRankingTab(tab.key)}
                  className={cn(
                    'rounded-md px-3 py-1.5 text-sm font-medium transition-colors',
                    tab.key === rankingTab ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground',
                  )}
                >
                  {tab.label}
                </button>
              ))}
            </div>
          </div>
        </CardHeader>
        <CardContent className="p-4">
          <TopUsageList
            kind={activeRanking.key}
            items={activeRanking.items}
            isLoading={isLoading}
            emptyTitle={activeRanking.emptyTitle}
            emptyDescription={activeRanking.emptyDescription}
            quotaPerUnit={quotaPerUnit}
          />
        </CardContent>
      </Card>

      <div className="grid gap-6 xl:grid-cols-[1.15fr_0.85fr]">
        <Card>
          <CardHeader className="border-b border-border">
            <CardTitle role="heading" aria-level={3}>{t("上游供应商")}</CardTitle>
          </CardHeader>
          <CardContent className="p-0">
            {isLoading ? (
              <div className="p-4">
                <TableSkeleton columns={[t("渠道"), t("供应商"), t("模型"), t("状态"), t("余额")]} rows={5} />
              </div>
            ) : channels.length === 0 ? (
              <EmptyState title={t("暂无渠道")} description={t("创建上游渠道后会显示在这里。")} />
            ) : (
              <div className="overflow-x-auto">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>{t("渠道")}</TableHead>
                      <TableHead>{t("供应商")}</TableHead>
                      <TableHead>{t("模型")}</TableHead>
                      <TableHead>{t("状态")}</TableHead>
                      <TableHead>{t("余额")}</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {channels.map((channel) => (
                      <TableRow key={channel.id}>
                        <TableCell className="font-semibold">{channel.name || `#${channel.id}`}</TableCell>
                        <TableCell>{PROVIDER_NAMES[numberValue(channel.type)] || `Type ${channel.type || '-'}`}</TableCell>
                        <TableCell className="max-w-72 truncate">{channel.models || '-'}</TableCell>
                        <TableCell>{channel.status === 1 ? t("启用") : t("停用")}</TableCell>
                        <TableCell>${numberValue(channel.balance).toFixed(2)}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="border-b border-border">
            <CardTitle role="heading" aria-level={3}>{t("订阅账号")}</CardTitle>
          </CardHeader>
          <CardContent className="p-0">
            {isLoading ? (
              <div className="p-4">
                <TableSkeleton columns={[t("名称"), t("平台"), t("分组"), t("优先级"), t("过期"), t("状态")]} rows={5} />
              </div>
            ) : subscriptionAccounts.length === 0 ? (
              <EmptyState title={t("暂无订阅账号")} description={t("新建 Claude / Codex 订阅账号后会显示在这里。")} />
            ) : (
              <div className="overflow-x-auto">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>{t("名称")}</TableHead>
                      <TableHead>{t("平台")}</TableHead>
                      <TableHead>{t("分组")}</TableHead>
                      <TableHead className="hidden md:table-cell">{t("优先级")}</TableHead>
                      <TableHead className="hidden lg:table-cell">{t("过期")}</TableHead>
                      <TableHead className="hidden xl:table-cell">{t("限额")}</TableHead>
                      <TableHead>{t("状态")}</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {subscriptionAccounts.map((account) => (
                      <TableRow key={account.id}>
                        <TableCell className="font-semibold">{account.name || `#${account.id}`}</TableCell>
                        <TableCell>{subscriptionPlatformLabel(account.platform)}</TableCell>
                        <TableCell>{account.group || '-'}</TableCell>
                        <TableCell className="hidden md:table-cell">{formatInteger(account.priority ?? 0)}</TableCell>
                        <TableCell className="hidden lg:table-cell">{formatDate(account.expiresAt)}</TableCell>
                        <TableCell className="hidden xl:table-cell">
                          <SubscriptionQuotaMini account={account} nowUnix={nowUnix} />
                        </TableCell>
                        <TableCell>
                          <AccountStatusBadge
                            info={{
                              status: account.status ?? 0,
                              expiresAt: account.expiresAt,
                              rateLimitedUntil: account.rateLimitedUntil,
                              quotaUsedPercent: account.quotaUsedPercent,
                              primaryQuotaUsedPercent: account.primaryQuotaUsedPercent,
                              secondaryQuotaUsedPercent: account.secondaryQuotaUsedPercent,
                              quotaSnapshotPaused: account.quotaSnapshotPaused,
                              quotaLimitUsd: account.quotaLimitUsd,
                              quotaUsedUsd: account.quotaUsedUsd,
                              quota5hLimitUsd: account.quota5hLimitUsd,
                              quota5hUsedUsd: account.quota5hUsedUsd,
                              quota5hWindowStart: account.quota5hWindowStart,
                              quotaDailyLimitUsd: account.quotaDailyLimitUsd,
                              quotaDailyUsedUsd: account.quotaDailyUsedUsd,
                              quotaDailyWindowStart: account.quotaDailyWindowStart,
                              quotaWeeklyLimitUsd: account.quotaWeeklyLimitUsd,
                              quotaWeeklyUsedUsd: account.quotaWeeklyUsedUsd,
                              quotaWeeklyWindowStart: account.quotaWeeklyWindowStart,
                              unschedulableReason: account.unschedulableReason,
                              recoveryPolicy: account.recoveryPolicy,
                              expectedRecoveryAt: account.expectedRecoveryAt,
                              unschedulableSince: account.unschedulableSince,
                            }}
                            now={nowUnix}
                          />
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            )}
          </CardContent>
        </Card>
      </div>

      <div className="grid gap-6 xl:grid-cols-[1.15fr_0.85fr]">
        <Card>
          <CardHeader className="border-b border-border">
            <CardTitle role="heading" aria-level={3}>{t("最近调用与订单动态")}</CardTitle>
          </CardHeader>
          <CardContent className="p-0">
            {isLoading ? (
              <div className="p-4">
                <TableSkeleton columns={[t("用户"), t("类型"), t("模型"), t("费用"), t("端点"), t("时间")]} rows={8} />
              </div>
            ) : logs.length === 0 ? (
              <EmptyState title={t("暂无流水")} description={t("用户调用、充值、兑换或退款后会显示在这里。")} />
            ) : (
              <div className="overflow-x-auto">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>{t("用户")}</TableHead>
                      <TableHead>{t("类型")}</TableHead>
                      <TableHead>{t("模型")}</TableHead>
                      <TableHead>{t("端点")}</TableHead>
                      <TableHead>{t("上游服务")}</TableHead>
                      <TableHead>{t("费用")}</TableHead>
                      <TableHead>{t("时间")}</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {logs.map((log) => (
                      <TableRow key={log.id}>
                        <TableCell className="font-mono text-xs">{log.userId || '-'}</TableCell>
                        <TableCell>{t(LOG_TYPE_NAMES[log.type || ''] || log.type || '-')}</TableCell>
                        <TableCell>{log.modelName || '-'}</TableCell>
                        <TableCell className="font-mono text-xs">{log.endpoint || '-'}</TableCell>
                        <TableCell>
                          {log.channelName ? (
                            <span className="inline-flex rounded-md bg-muted px-2.5 py-1 text-xs font-medium text-foreground">
                              {log.channelName}
                              {log.channelTypeStr && log.channelTypeStr !== 'Unknown' && ` (${log.channelTypeStr})`}
                            </span>
                          ) : log.channelId ? (
                            <span className="text-xs text-muted-foreground">#{log.channelId}</span>
                          ) : (
                            '-'
                          )}
                        </TableCell>
                        <TableCell className="font-semibold">{formatQuota(log.amount)}</TableCell>
                        <TableCell>{formatDate(log.createdAt)}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="border-b border-border">
            <CardTitle role="heading" aria-level={3}>{t("最近用户")}</CardTitle>
          </CardHeader>
          <CardContent className="p-0">
            {isLoading ? (
              <div className="p-4">
                <TableSkeleton columns={[t("用户"), t("分组"), t("状态")]} rows={5} />
              </div>
            ) : users.length === 0 ? (
              <EmptyState title={t("暂无用户")} description={t("注册或创建用户后会显示在这里。")} />
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t("用户")}</TableHead>
                    <TableHead>{t("分组")}</TableHead>
                    <TableHead>{t("状态")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {users.map((user) => (
                    <TableRow key={user.id}>
                      <TableCell>
                        <div className="font-semibold">{user.display_name || user.displayName || user.username || `#${user.id}`}</div>
                        <div className="text-xs text-muted-foreground">{user.email || user.username || '-'}</div>
                      </TableCell>
                      <TableCell>{user.group || '-'}</TableCell>
                      <TableCell>{user.status === 1 ? t("启用") : t("停用")}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
