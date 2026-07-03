import { useQuery } from '@tanstack/react-query';
import { Activity, AlertTriangle, Boxes, CreditCard, Database, Gauge, KeyRound, LineChart, Scale, TrendingUp, Users } from 'lucide-react';

import { Link } from 'react-router-dom';
import { EmptyState } from '@/components/EmptyState';
import { TableSkeleton } from '@/components/LoadingStates';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { adminApiClient } from '@/lib/api';
import { unwrapApiData } from '@/lib/api-response';
import { quotaPerUnitFromOptions, quotaToCurrencyUnits } from '@/lib/amount';

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

interface AdminSubscriptionAccount {
  id?: number;
  name?: string;
  platform?: string;
  account_type?: string;
  accountType?: string;
  status?: number;
  group?: string;
  models?: string;
  priority?: number;
  account_id?: string;
  accountId?: string;
  expires_at?: number;
  expiresAt?: number;
  updated_at?: number;
  updatedAt?: number;
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
}

interface UsageAggregateItem {
  key?: string;
  user_id?: string;
  channel_id?: number;
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
  subscription_accounts?: AdminSubscriptionAccount[];
  recent_logs?: AdminLog[];
  cost_analysis?: CostAnalysis;
  top_models?: UsageAggregateItem[];
  top_channels?: UsageAggregateItem[];
  top_users?: UsageAggregateItem[];
  top_tokens?: UsageAggregateItem[];
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
};

function subscriptionPlatformLabel(platform?: string) {
  if (!platform) return '-';
  return SUBSCRIPTION_PLATFORM_LABELS[platform] ?? platform;
}

function subscriptionStatusLabel(status?: number) {
  return status === 1 ? '启用' : '停用';
}

function numberValue(value: unknown): number {
  const parsed = Number(value ?? 0);
  return Number.isFinite(parsed) ? parsed : 0;
}

function formatQuota(value?: number | string, quotaPerUnit?: number) {
  return quotaToCurrencyUnits(value, quotaPerUnit).toFixed(4);
}

function formatInteger(value?: number): string {
  return numberValue(value).toLocaleString();
}

function formatCompactInteger(value?: number): string {
  const n = numberValue(value);
  if (Math.abs(n) >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (Math.abs(n) >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
  return n.toLocaleString();
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
  return new Date(timestamp * 1000).toLocaleString();
}

function parsePricingMap(value?: string) {
  if (!value) return {};
  try {
    const parsed = JSON.parse(value) as Record<string, number>;
    return parsed && typeof parsed === 'object' ? parsed : {};
  } catch {
    return {};
  }
}

function parseModelPriceMap(value?: string) {
  if (!value) return {};
  try {
    const parsed = JSON.parse(value) as Record<string, unknown>;
    return parsed && typeof parsed === 'object' ? parsed : {};
  } catch {
    return {};
  }
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
    <Card className="rounded-lg border-0 bg-white shadow-sm ring-1 ring-slate-200 dark:bg-card dark:ring-white/10">
      <CardContent className="flex items-center gap-4 p-5">
        <div className="grid size-11 place-items-center rounded-lg bg-slate-950 text-white dark:bg-white dark:text-slate-950">
          <Icon className="size-5" />
        </div>
        <div className="min-w-0">
          <div className="text-sm font-semibold text-slate-500 dark:text-slate-400">{title}</div>
          <div className="mt-1 truncate text-2xl font-black text-slate-950 dark:text-white">{value}</div>
          <div className="mt-1 text-xs font-medium text-slate-400">{detail}</div>
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
    <Card className="rounded-lg border-0 bg-white shadow-sm ring-1 ring-slate-200 dark:bg-card dark:ring-white/10">
      <CardContent className="flex items-center gap-4 p-5">
        <div className={`grid size-11 place-items-center rounded-lg ${styles}`}>
          <Icon className="size-5" />
        </div>
        <div className="min-w-0">
          <div className="text-sm font-semibold text-slate-500 dark:text-slate-400">{title}</div>
          <div className="mt-1 truncate text-2xl font-black text-slate-950 dark:text-white">{value}</div>
          <div className="mt-1 text-xs font-medium text-slate-400">{detail}</div>
        </div>
      </CardContent>
    </Card>
  );
}

function topItemLabel(item: UsageAggregateItem, kind: 'model' | 'channel' | 'user' | 'token') {
  if (kind === 'model') return item.model || item.key || '-';
  if (kind === 'channel') return item.name || (item.channel_id ? `#${item.channel_id}` : item.key || '-');
  if (kind === 'token') return item.token_name || item.key || '-';
  return item.user_id || item.key || '-';
}

function TopUsageChartCard({
  title,
  kind,
  items,
  isLoading,
  emptyTitle,
  emptyDescription,
  quotaPerUnit,
}: {
  title: string;
  kind: 'model' | 'channel' | 'user' | 'token';
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
  }[kind];

  return (
    <Card className="rounded-lg border-0 bg-white shadow-sm ring-1 ring-slate-200 dark:bg-card dark:ring-white/10">
      <CardHeader className="border-b border-slate-100 dark:border-white/10">
        <CardTitle role="heading" aria-level={3}>{title}</CardTitle>
      </CardHeader>
      <CardContent className="p-4">
        {isLoading ? (
          <TableSkeleton columns={['对象', '消耗', '占比']} rows={5} />
        ) : items.length === 0 ? (
          <EmptyState title={emptyTitle} description={emptyDescription} />
        ) : (
          <div className="space-y-4">
            {items.map((item, index) => {
              const quota = Math.abs(numberValue(item.quota));
              const width = `${Math.max(4, (quota / maxQuota) * 100)}%`;
              const label = topItemLabel(item, kind);
              return (
                <div key={`${kind}-${item.key || item.user_id || item.channel_id || item.model || item.token_name || index}`} className="space-y-2">
                  <div className="flex items-center justify-between gap-3">
                    <div className="flex min-w-0 items-center gap-2">
                      <span className="grid size-6 shrink-0 place-items-center rounded-md bg-slate-100 text-xs font-black text-slate-500 dark:bg-white/10 dark:text-slate-300">
                        {index + 1}
                      </span>
                      <span className="truncate text-sm font-bold text-slate-900 dark:text-white" title={label}>
                        {label}
                      </span>
                    </div>
                    <span className="shrink-0 text-sm font-black text-slate-950 dark:text-white">
                      {formatQuota(quota, quotaPerUnit)}
                    </span>
                  </div>
                  <div className="h-2.5 overflow-hidden rounded-full bg-slate-100 dark:bg-white/10">
                    <div className={`h-full rounded-full ${barStyles}`} style={{ width }} />
                  </div>
                  <div className="flex items-center justify-between gap-3 text-xs font-semibold text-slate-400">
                    <span>{formatCompactInteger(item.count)} 次请求</span>
                    <span>{formatCompactInteger(totalTokens(item))} tokens</span>
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </CardContent>
    </Card>
  );
}

export function AdminOverviewPage() {
  const { data, isLoading } = useQuery({
    queryKey: ['admin-summary'],
    queryFn: async () => {
      const res = await adminApiClient.get('/admin/summary');
      return unwrapApiData<AdminSummary>(res.data);
    },
  });

  const totals = data?.totals ?? {};
  const channels = data?.channels ?? [];
  const subscriptionAccounts = data?.subscription_accounts ?? [];
  const logs = data?.recent_logs ?? [];
  const users = data?.recent_users ?? [];
  const costAnalysis = data?.cost_analysis ?? {};
  const topModels = data?.top_models ?? [];
  const topChannels = data?.top_channels ?? [];
  const topUsers = data?.top_users ?? [];
  const topTokens = data?.top_tokens ?? [];
  const alerts = data?.alerts ?? [];
  const latestReconciliation = data?.latest_reconciliation;
  const modelPrice = parseModelPriceMap(data?.pricing_options?.ModelPrice);
  const modelRatio = parsePricingMap(data?.pricing_options?.ModelRatio);
  const completionRatio = parsePricingMap(data?.pricing_options?.CompletionRatio);
  const quotaPerUnit = quotaPerUnitFromOptions(data?.pricing_options);
  const configuredModels = totals.configured_models || modelCount(channels) || data?.model_catalog?.length || 0;
  const paymentAmountCents =
    data?.payment_summary?.recent_amount_cents ??
    data?.payment_summary?.recent_amount_money_cents ??
    data?.payment_summary?.recent_amount ??
    0;

  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h2 className="text-2xl font-black tracking-normal text-slate-950 dark:text-white">管理总览</h2>
          <p className="mt-1 text-sm font-medium text-slate-500 dark:text-slate-400">
            查看平台运行状态、上游渠道、用户规模、调用流水和价格配置。
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button variant="outline" size="sm" nativeButton={false} render={<Link to="/admin/channels" />}>
            渠道配置
          </Button>
          <Button variant="outline" size="sm" nativeButton={false} render={<Link to="/admin/pricing" />}>
            模型价格
          </Button>
          <Button variant="outline" size="sm" nativeButton={false} render={<Link to="/admin/subscription-accounts" />}>
            订阅账号
          </Button>
        </div>
      </div>

      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard
          title="用户"
          value={formatInteger(totals.users)}
          detail={`${formatInteger(totals.active_users)} 个启用用户`}
          icon={Users}
        />
        <StatCard
          title="上游供应商"
          value={formatInteger(totals.channels)}
          detail={`${formatInteger(totals.active_channels)} 个启用渠道`}
          icon={Database}
        />
        <StatCard
          title="订阅账号"
          value={formatInteger(totals.subscription_accounts)}
          detail={`${formatInteger(totals.active_subscription_accounts)} 个启用账号`}
          icon={KeyRound}
        />
        <StatCard
          title="调用请求"
          value={formatInteger(totals.request_count)}
          detail={`${formatQuota(totals.quota_used, quotaPerUnit)} 金额消耗`}
          icon={Activity}
        />
        <StatCard
          title="账务记录"
          value={formatMoneyCents(paymentAmountCents)}
          detail={`${formatInteger(data?.payment_summary?.recent_order_count)} 条近期充值/兑换/退款`}
          icon={CreditCard}
        />
      </div>

      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
        <CostCard
          title="用户侧收入"
          value={formatQuota(costAnalysis.revenue_quota ?? totals.quota_used, quotaPerUnit)}
          detail="consume 账本计费金额"
          icon={TrendingUp}
          tone="green"
        />
        <CostCard
          title="上游成本"
          value={formatQuota(costAnalysis.upstream_cost ?? totals.upstream_cost, quotaPerUnit)}
          detail="渠道侧成本汇总"
          icon={Database}
          tone="blue"
        />
        <CostCard
          title="毛利"
          value={formatQuota(costAnalysis.gross_profit ?? totals.gross_profit, quotaPerUnit)}
          detail={`毛利率 ${formatMargin(costAnalysis.gross_margin)}`}
          icon={LineChart}
          tone={numberValue(costAnalysis.gross_profit ?? totals.gross_profit) >= 0 ? 'green' : 'red'}
        />
        <CostCard
          title="告警"
          value={formatInteger(alerts.length)}
          detail={latestReconciliation?.run_id ? `最近对账 #${latestReconciliation.run_id}` : '暂无对账记录'}
          icon={AlertTriangle}
          tone={alerts.length > 0 ? 'amber' : 'green'}
        />
      </div>

      <div className="grid gap-4 lg:grid-cols-3">
        <Card className="rounded-lg border-0 bg-white shadow-sm ring-1 ring-slate-200 dark:bg-card dark:ring-white/10">
          <CardContent className="flex items-center gap-4 p-5">
            <Gauge className="size-10 text-emerald-600" />
            <div>
              <div className="text-sm font-semibold text-slate-500">渠道余额</div>
              <div className="text-2xl font-black">${numberValue(totals.channel_balance).toFixed(2)}</div>
              <div className="text-xs font-medium text-slate-400">{formatInteger(totals.stale_balance_channels)} 个余额待刷新</div>
            </div>
          </CardContent>
        </Card>
        <Card className="rounded-lg border-0 bg-white shadow-sm ring-1 ring-slate-200 dark:bg-card dark:ring-white/10">
          <CardContent className="flex items-center gap-4 p-5">
            <Boxes className="size-10 text-blue-600" />
            <div>
              <div className="text-sm font-semibold text-slate-500">可用模型</div>
              <div className="text-2xl font-black">{configuredModels}</div>
              <div className="text-xs font-medium text-slate-400">{Object.keys(modelPrice).length || Object.keys(modelRatio).length} 个模型价格项</div>
            </div>
          </CardContent>
        </Card>
        <Card className="rounded-lg border-0 bg-white shadow-sm ring-1 ring-slate-200 dark:bg-card dark:ring-white/10">
          <CardContent className="flex items-center gap-4 p-5">
            <LineChart className="size-10 text-violet-600" />
            <div>
              <div className="text-sm font-semibold text-slate-500">金额消耗</div>
              <div className="text-2xl font-black">{formatQuota(totals.quota_used, quotaPerUnit)}</div>
              <div className="text-xs font-medium text-slate-400">{Object.keys(completionRatio).length} 个兼容倍率项</div>
            </div>
          </CardContent>
        </Card>
      </div>

      <div className="grid gap-6 xl:grid-cols-4">
        <TopUsageChartCard
          title="高消耗用户"
          kind="user"
          items={topUsers}
          isLoading={isLoading}
          emptyTitle="暂无用户用量"
          emptyDescription="产生调用后会显示高消耗用户。"
          quotaPerUnit={quotaPerUnit}
        />
        <TopUsageChartCard
          title="高消耗模型"
          kind="model"
          items={topModels}
          isLoading={isLoading}
          emptyTitle="暂无模型用量"
          emptyDescription="产生调用后会显示模型消耗排行。"
          quotaPerUnit={quotaPerUnit}
        />
        <TopUsageChartCard
          title="高消耗渠道"
          kind="channel"
          items={topChannels}
          isLoading={isLoading}
          emptyTitle="暂无渠道用量"
          emptyDescription="渠道产生调用后会显示消耗排行。"
          quotaPerUnit={quotaPerUnit}
        />
        <TopUsageChartCard
          title="高消耗 Token"
          kind="token"
          items={topTokens}
          isLoading={isLoading}
          emptyTitle="暂无 Token 用量"
          emptyDescription="API Token 产生调用后会显示消耗排行。"
          quotaPerUnit={quotaPerUnit}
        />
      </div>

      <div className="grid gap-6 xl:grid-cols-4">
        <Card className="rounded-lg border-0 bg-white shadow-sm ring-1 ring-slate-200 dark:bg-card dark:ring-white/10 xl:col-span-4">
          <CardHeader className="border-b border-slate-100 dark:border-white/10">
            <CardTitle role="heading" aria-level={3}>风险告警</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3 p-4">
            {isLoading ? (
              <TableSkeleton columns={['类型', '对象']} rows={4} />
            ) : alerts.length === 0 ? (
              <EmptyState title="暂无告警" description="渠道余额、毛利和对账差异正常。" />
            ) : (
              alerts.slice(0, 5).map((alert, index) => (
                <div key={`${alert.type}-${alert.channel_id || alert.run_id || index}`} className="rounded-lg border border-amber-200 bg-amber-50 p-3 dark:border-amber-500/30 dark:bg-amber-500/10">
                  <div className="flex items-center gap-2 text-sm font-bold text-amber-800 dark:text-amber-200">
                    <AlertTriangle className="size-4" />
                    {alert.message || alert.type || '告警'}
                  </div>
                  <div className="mt-1 text-xs font-medium text-amber-700/80 dark:text-amber-200/80">
                    {alert.channel_id ? `渠道 #${alert.channel_id}` : alert.run_id ? `对账 #${alert.run_id}` : alert.severity || '-'}
                  </div>
                </div>
              ))
            )}
            {latestReconciliation?.run_id ? (
              <Button variant="outline" size="sm" nativeButton={false} render={<Link to="/admin/reconciliation" />}>
                <Scale className="size-4" />
                查看对账
              </Button>
            ) : null}
          </CardContent>
        </Card>
      </div>

      <div className="grid gap-6 xl:grid-cols-[1.15fr_0.85fr]">
        <Card className="rounded-lg border-0 bg-white shadow-sm ring-1 ring-slate-200 dark:bg-card dark:ring-white/10">
          <CardHeader className="border-b border-slate-100 dark:border-white/10">
            <CardTitle role="heading" aria-level={3}>上游供应商</CardTitle>
          </CardHeader>
          <CardContent className="p-0">
            {isLoading ? (
              <div className="p-4">
                <TableSkeleton columns={['渠道', '供应商', '模型', '状态', '余额']} rows={5} />
              </div>
            ) : channels.length === 0 ? (
              <EmptyState title="暂无渠道" description="创建上游渠道后会显示在这里。" />
            ) : (
              <div className="overflow-x-auto">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>渠道</TableHead>
                      <TableHead>供应商</TableHead>
                      <TableHead>模型</TableHead>
                      <TableHead>状态</TableHead>
                      <TableHead>余额</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {channels.map((channel) => (
                      <TableRow key={channel.id}>
                        <TableCell className="font-semibold">{channel.name || `#${channel.id}`}</TableCell>
                        <TableCell>{PROVIDER_NAMES[numberValue(channel.type)] || `Type ${channel.type || '-'}`}</TableCell>
                        <TableCell className="max-w-72 truncate">{channel.models || '-'}</TableCell>
                        <TableCell>{channel.status === 1 ? '启用' : '停用'}</TableCell>
                        <TableCell>${numberValue(channel.balance).toFixed(2)}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            )}
          </CardContent>
        </Card>

        <Card className="rounded-lg border-0 bg-white shadow-sm ring-1 ring-slate-200 dark:bg-card dark:ring-white/10">
          <CardHeader className="border-b border-slate-100 dark:border-white/10">
            <CardTitle role="heading" aria-level={3}>订阅账号</CardTitle>
          </CardHeader>
          <CardContent className="p-0">
            {isLoading ? (
              <div className="p-4">
                <TableSkeleton columns={['名称', '平台', '分组', '优先级', '过期', '状态']} rows={5} />
              </div>
            ) : subscriptionAccounts.length === 0 ? (
              <EmptyState title="暂无订阅账号" description="新建 Claude / Codex 订阅账号后会显示在这里。" />
            ) : (
              <div className="overflow-x-auto">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>名称</TableHead>
                      <TableHead>平台</TableHead>
                      <TableHead>分组</TableHead>
                      <TableHead className="hidden md:table-cell">优先级</TableHead>
                      <TableHead className="hidden lg:table-cell">过期</TableHead>
                      <TableHead>状态</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {subscriptionAccounts.map((account) => (
                      <TableRow key={account.id}>
                        <TableCell className="font-semibold">{account.name || `#${account.id}`}</TableCell>
                        <TableCell>{subscriptionPlatformLabel(account.platform)}</TableCell>
                        <TableCell>{account.group || '-'}</TableCell>
                        <TableCell className="hidden md:table-cell">{formatInteger(account.priority ?? 0)}</TableCell>
                        <TableCell className="hidden lg:table-cell">{formatDate(account.expires_at ?? account.expiresAt)}</TableCell>
                        <TableCell>{subscriptionStatusLabel(account.status)}</TableCell>
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
        <Card className="rounded-lg border-0 bg-white shadow-sm ring-1 ring-slate-200 dark:bg-card dark:ring-white/10">
          <CardHeader className="border-b border-slate-100 dark:border-white/10">
            <CardTitle role="heading" aria-level={3}>最近调用与订单动态</CardTitle>
          </CardHeader>
          <CardContent className="p-0">
            {isLoading ? (
              <div className="p-4">
                <TableSkeleton columns={['用户', '类型', '模型', '费用', '端点', '时间']} rows={8} />
              </div>
            ) : logs.length === 0 ? (
              <EmptyState title="暂无流水" description="用户调用、充值、兑换或退款后会显示在这里。" />
            ) : (
              <div className="overflow-x-auto">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>用户</TableHead>
                      <TableHead>类型</TableHead>
                      <TableHead>模型</TableHead>
                      <TableHead>费用</TableHead>
                      <TableHead>端点</TableHead>
                      <TableHead>时间</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {logs.map((log) => (
                      <TableRow key={log.id}>
                        <TableCell className="font-mono text-xs">{log.userId || '-'}</TableCell>
                        <TableCell>{LOG_TYPE_NAMES[log.type || ''] || log.type || '-'}</TableCell>
                        <TableCell>{log.modelName || '-'}</TableCell>
                        <TableCell className="font-semibold">{formatQuota(log.amount)}</TableCell>
                        <TableCell className="font-mono text-xs">{log.endpoint || '-'}</TableCell>
                        <TableCell>{formatDate(log.createdAt)}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            )}
          </CardContent>
        </Card>

        <Card className="rounded-lg border-0 bg-white shadow-sm ring-1 ring-slate-200 dark:bg-card dark:ring-white/10">
          <CardHeader className="border-b border-slate-100 dark:border-white/10">
            <CardTitle role="heading" aria-level={3}>最近用户</CardTitle>
          </CardHeader>
          <CardContent className="p-0">
            {isLoading ? (
              <div className="p-4">
                <TableSkeleton columns={['用户', '分组', '状态']} rows={5} />
              </div>
            ) : users.length === 0 ? (
              <EmptyState title="暂无用户" description="注册或创建用户后会显示在这里。" />
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>用户</TableHead>
                    <TableHead>分组</TableHead>
                    <TableHead>状态</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {users.map((user) => (
                    <TableRow key={user.id}>
                      <TableCell>
                        <div className="font-semibold">{user.display_name || user.displayName || user.username || `#${user.id}`}</div>
                        <div className="text-xs text-slate-400">{user.email || user.username || '-'}</div>
                      </TableCell>
                      <TableCell>{user.group || '-'}</TableCell>
                      <TableCell>{user.status === 1 ? '启用' : '停用'}</TableCell>
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
