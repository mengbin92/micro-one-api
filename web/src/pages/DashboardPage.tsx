import { Link } from 'react-router';
import { useQuery } from '@tanstack/react-query';
import {
  Area,
  AreaChart,
  CartesianGrid,
  Cell,
  Pie,
  PieChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import { BarChart3, Box, ChevronRight, FlaskConical, Gift, KeyRound, Sparkles, WalletCards, Zap } from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import { apiClient } from '@/lib/api';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { EmptyState } from '@/components/EmptyState';
import { ChartSkeleton, MetricCardsSkeleton } from '@/components/LoadingStates';
import { unwrapApiData } from '@/lib/api-response';
import { formatAmountUnits, formatUSD } from '@/lib/amount';
import { cn } from '@/lib/utils';
import { locale, t } from '@/lib/i18n';
import {
  accountDashboardQueryOptions,
  userSelfQueryOptions,
  type UsageSummaryItem,
} from '@/lib/account-queries';

interface Token {
  id: number;
  name?: string;
  status: number;
}

interface TokenListData {
  items?: Token[];
  total?: number;
}

const CHART_COLORS = [
  'var(--chart-1)',
  'var(--chart-2)',
  'var(--chart-3)',
  'var(--chart-4)',
  'var(--chart-5)',
];

const CHART_TOOLTIP_STYLE = {
  background: 'var(--popover)',
  color: 'var(--popover-foreground)',
  border: '1px solid var(--border)',
  borderRadius: '8px',
  fontSize: '12px',
};

function formatAmount(q: number, digits = 4) {
  return formatAmountUnits(q, digits);
}

function formatMoney(q: number, digits = 4) {
  return formatUSD(q, digits);
}

function compactNumber(value: number) {
  if (value >= 1000000) {
    return `${(value / 1000000).toFixed(2)}M`;
  }
  if (value >= 1000) {
    return `${(value / 1000).toFixed(1)}K`;
  }
  return value.toLocaleString(locale());
}

function numberOrZero(value: unknown) {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0;
}

// §9.1 display contract: prefer the canonical uncached-input sum from the
// billing aggregate; legacy rows (all canonical sums zero) fall back to the
// reported prompt WITHOUT a prompt-cache subtraction — mixed legacy
// subset/exclusive rows make that arithmetic unsound.
function displayInputTokens(item: UsageSummaryItem) {
  if ((item.uncached_input_tokens || 0) > 0 || (item.billable_total_tokens || 0) > 0) {
    return item.uncached_input_tokens || 0;
  }
  return item.prompt_tokens || 0;
}

function displayTotalTokens(item: UsageSummaryItem) {
  if ((item.billable_total_tokens || 0) > 0) return item.billable_total_tokens || 0;
  return displayInputTokens(item) + (item.completion_tokens || 0) + (item.cache_read_tokens || 0) + (item.cache_creation_tokens || 0);
}

function normalizeTokens(data: Token[] | TokenListData): Token[] {
  const onlyNamedTokens = (items: Token[]) => items.filter((token) => token.name?.trim());
  if (Array.isArray(data)) {
    return onlyNamedTokens(data);
  }
  if (Array.isArray(data?.items)) {
    return onlyNamedTokens(data.items);
  }
  return [];
}

function getGreeting() {
  const hour = new Date().getHours();
  if (hour < 6) return t("凌晨好");
  if (hour < 12) return t("上午好");
  if (hour < 18) return t("下午好");
  return t("晚上好");
}

function MetricCard({
  title,
  value,
  subtitle,
  tone,
  icon: Icon,
}: {
  title: string;
  value: string;
  subtitle: string;
  tone: 'orange' | 'purple' | 'green' | 'blue' | 'amber';
  icon: LucideIcon;
}) {
  const styles = {
    orange: 'text-orange-600 bg-orange-50 dark:bg-orange-500/10 dark:text-orange-300',
    purple: 'text-violet-600 bg-violet-50 dark:bg-violet-500/10 dark:text-violet-300',
    green: 'text-emerald-600 bg-emerald-50 dark:bg-emerald-500/10 dark:text-emerald-300',
    blue: 'text-blue-600 bg-blue-50 dark:bg-blue-500/10 dark:text-blue-300',
    amber: 'text-amber-600 bg-amber-50 dark:bg-amber-500/10 dark:text-amber-300',
  }[tone];

  return (
    <Card className="min-h-40">
      <CardContent className="flex h-full flex-col justify-between p-5">
        <div className="flex items-start justify-between gap-4">
          <span className="text-sm font-medium text-muted-foreground">{title}</span>
          <span className={cn('grid size-12 shrink-0 place-items-center rounded-lg', styles)}>
            <Icon className="size-5" />
          </span>
        </div>
        <div>
          <div className={cn('break-words text-3xl font-semibold tracking-normal', styles.split(' ')[0])}>{value}</div>
          <div className="mt-4 text-sm font-medium text-muted-foreground">{subtitle}</div>
        </div>
      </CardContent>
    </Card>
  );
}

export function DashboardPage() {
  const { data: user, isLoading: isUserLoading } = useQuery(userSelfQueryOptions);

  const { data: dashboard, isLoading } = useQuery(accountDashboardQueryOptions);

  const { data: tokens, isLoading: isTokensLoading } = useQuery({
    queryKey: ['tokens'],
    queryFn: async () => {
      const res = await apiClient.get('/token');
      return normalizeTokens(unwrapApiData<Token[] | TokenListData>(res.data));
    },
  });

  const items = Array.isArray(dashboard?.usage) ? dashboard.usage : [];
  const latest = items.at(-1);
  const totalCount = items.reduce((s, x) => s + (x.count || 0), 0);
  const promptTokens = items.reduce((s, x) => s + displayInputTokens(x), 0);
  const completionTokens = items.reduce((s, x) => s + (x.completion_tokens || 0), 0);
  const cacheReadTokens = items.reduce((s, x) => s + (x.cache_read_tokens || 0), 0);
  const totalTokens = items.reduce((sum, item) => sum + displayTotalTokens(item), 0);
  const balance = numberOrZero(dashboard?.balance);
  const usedAmount = numberOrZero(dashboard?.used_amount);
  const requestCount = items.length > 0 ? totalCount : numberOrZero(dashboard?.request_count);
  const todayRequests = latest?.count ?? 0;
  const todayAmount = dashboard?.today_amount ?? latest?.amount ?? 0;
  const todayPromptTokens = latest ? displayInputTokens(latest) : dashboard?.today_prompt_tokens ?? 0;
  const todayCompletionTokens = dashboard?.today_completion_tokens ?? latest?.completion_tokens ?? 0;
  const todayCacheReadTokens = dashboard?.today_cache_read_tokens ?? latest?.cache_read_tokens ?? 0;
  const avgLatency = dashboard?.avg_latency ?? 0;
  const chartData = items.map((item) => ({
    ...item,
    label: item.date || item.day,
    input_tokens: displayInputTokens(item),
    output_tokens: item.completion_tokens || 0,
    cache_read_tokens: item.cache_read_tokens || 0,
    cache_creation_tokens: item.cache_creation_tokens || 0,
    total_tokens: displayTotalTokens(item),
  }));
  const tokenCount = tokens?.length ?? 0;
  const activeTokenCount = tokens?.filter((token) => token.status === 1).length ?? tokenCount;
  const isSummaryLoading = isUserLoading || isLoading || isTokensLoading;
  const displayName = user?.display_name || user?.username || t("用户");

  // Model distribution from backend
  const modelDistribution = dashboard?.model_distribution ?? [];
  const pieData = modelDistribution.length > 0
    ? modelDistribution.map((item, index) => ({
        name: item.model,
        value: item.tokens,
        color: CHART_COLORS[index % CHART_COLORS.length],
      }))
    : [
        { name: t("输入 Tokens"), value: promptTokens, color: CHART_COLORS[0] },
        { name: t("输出 Tokens"), value: completionTokens, color: CHART_COLORS[1] },
      ].filter((item) => item.value > 0);
  const distributionData = pieData.length > 0 ? pieData : [{ name: t("总 Tokens"), value: totalTokens || 1, color: CHART_COLORS[0] }];

  return (
    <div className="space-y-7">
      <section>
        <h2 className="text-3xl font-bold tracking-normal text-foreground sm:text-4xl lg:text-5xl">
          {getGreeting()}，{displayName}
        </h2>
        <p className="mt-4 text-lg font-medium text-muted-foreground">{t("欢迎使用 Micro API 中转平台，实时掌握你的 API 使用情况。")}</p>
      </section>

      <section className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-6">
        {isSummaryLoading ? (
          <MetricCardsSkeleton />
        ) : (
          <>
            <MetricCard title={t("钱包余额")} value={formatMoney(balance)} subtitle={t("可用余额")} tone="orange" icon={WalletCards} />
            <MetricCard title={t("已用金额")} value={`$${formatAmount(usedAmount, 4)}`} subtitle={t("累计消耗")} tone="purple" icon={Sparkles} />
            <MetricCard title={t("调用次数")} value={requestCount.toLocaleString(locale())} subtitle={t(`今日 ${todayRequests.toLocaleString(locale())}`)} tone="green" icon={BarChart3} />
            <MetricCard title={t("API 密钥")} value={tokenCount.toLocaleString(locale())} subtitle={t(`可用 ${activeTokenCount.toLocaleString(locale())}`)} tone="blue" icon={KeyRound} />
            <MetricCard title={t("今日消耗")} value={`$${formatAmount(todayAmount, 4)}`} subtitle={t(`今日 Token ${compactNumber(todayPromptTokens + todayCompletionTokens)} / 缓存 ${compactNumber(todayCacheReadTokens)}`)} tone="amber" icon={Box} />
            <MetricCard title={t("平均延迟")} value={avgLatency > 0 ? `${(avgLatency / 1000).toFixed(2)}s` : "-"} subtitle={avgLatency > 0 ? t(`${totalCount} 次调用`) : t("暂无数据")} tone="blue" icon={Zap} />
          </>
        )}
      </section>

      <section className="grid grid-cols-1 gap-5 xl:grid-cols-[minmax(0,1.25fr)_minmax(340px,0.75fr)_360px]">
        <Card>
          <CardHeader className="border-b border-border p-6">
            <div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
              <div>
                <CardTitle className="text-2xl font-bold tracking-normal text-foreground">{t("Token 使用趋势")}</CardTitle>
                <p className="mt-3 text-base font-medium text-muted-foreground">{t("总量")}{compactNumber(totalTokens)} Tokens
                </p>
                <p className="mt-1 text-sm font-medium text-muted-foreground">{t("输入")}{compactNumber(promptTokens)}{t("/ 输出")}{compactNumber(completionTokens)}{t("/ 缓存")}{compactNumber(cacheReadTokens)}
                </p>
              </div>
              <div className="h-11 rounded-lg border border-border px-4 text-sm font-semibold leading-11 text-foreground">{t("近 7 天")}</div>
            </div>
          </CardHeader>
          <CardContent className="p-6">
            {isLoading ? (
              <ChartSkeleton />
            ) : chartData.length === 0 ? (
              <EmptyState title={t("暂无使用数据")} description={t("请求完成后会在这里展示 Token 使用趋势。")} />
            ) : (
              <ResponsiveContainer width="100%" height={300}>
                <AreaChart data={chartData} margin={{ left: 0, right: 8, top: 20, bottom: 0 }}>
                  <defs>
                    <linearGradient id="inputTokens" x1="0" x2="0" y1="0" y2="1">
                      <stop offset="0%" stopColor="var(--chart-1)" stopOpacity={0.24} />
                      <stop offset="100%" stopColor="var(--chart-1)" stopOpacity={0.02} />
                    </linearGradient>
                    <linearGradient id="cacheReadTokens" x1="0" x2="0" y1="0" y2="1">
                      <stop offset="0%" stopColor="var(--chart-3)" stopOpacity={0.20} />
                      <stop offset="100%" stopColor="var(--chart-3)" stopOpacity={0.02} />
                    </linearGradient>
                  </defs>
                  <CartesianGrid stroke="var(--chart-grid)" strokeDasharray="4 4" vertical={false} />
                  <XAxis dataKey="label" tick={{ fontSize: 12, fill: 'var(--chart-label)' }} tickLine={false} axisLine={false} />
                  <YAxis tick={{ fontSize: 12, fill: 'var(--chart-label)' }} tickLine={false} axisLine={false} width={48} tickFormatter={compactNumber} />
                  <Tooltip formatter={(value) => compactNumber(Number(value))} contentStyle={CHART_TOOLTIP_STYLE} />
                  <Area
                    type="monotone"
                    dataKey="input_tokens"
                    name={t("输入 Tokens")}
                    stroke="var(--chart-1)"
                    strokeWidth={3}
                    fill="url(#inputTokens)"
                  />
                  <Area
                    type="monotone"
                    dataKey="output_tokens"
                    name={t("输出 Tokens")}
                    stroke="var(--chart-2)"
                    strokeWidth={3}
                    strokeDasharray="6 4"
                    fill="transparent"
                  />
                  <Area
                    type="monotone"
                    dataKey="cache_read_tokens"
                    name={t("缓存 Tokens")}
                    stroke="var(--chart-3)"
                    strokeWidth={2}
                    strokeDasharray="2 4"
                    fill="url(#cacheReadTokens)"
                  />
                </AreaChart>
              </ResponsiveContainer>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="border-b border-border p-6">
            <CardTitle className="text-2xl font-bold tracking-normal text-foreground">
              {modelDistribution.length > 0 ? t("模型分布") : t("Token 分布")}
            </CardTitle>
          </CardHeader>
          <CardContent className="p-6">
            {isLoading ? (
              <ChartSkeleton />
            ) : totalTokens === 0 ? (
              <EmptyState title={t("暂无分布数据")} description={t("产生 Token 消耗后会展示占比。")} />
            ) : (
              <div className="grid min-h-[300px] place-items-center">
                <ResponsiveContainer width="100%" height={260}>
                  <PieChart>
                    <Pie data={distributionData} dataKey="value" innerRadius="62%" outerRadius="86%" paddingAngle={3}>
                      {distributionData.map((entry) => (
                        <Cell key={entry.name} fill={entry.color} />
                      ))}
                    </Pie>
                    <Tooltip formatter={(value) => compactNumber(Number(value))} contentStyle={CHART_TOOLTIP_STYLE} />
                  </PieChart>
                </ResponsiveContainer>
                <div className="-mt-40 text-center">
                  <div className="text-4xl font-bold text-foreground">{compactNumber(totalTokens)}</div>
                  <div className="mt-2 text-sm font-medium text-muted-foreground">{t("总 Tokens")}</div>
                </div>
                <div className="mt-12 flex flex-wrap justify-center gap-4">
                  {distributionData.map((entry) => (
                    <div key={entry.name} className="flex items-center gap-2 text-sm font-medium text-muted-foreground">
                      <span className="size-3 rounded-full" style={{ backgroundColor: entry.color }} />
                      {entry.name}
                    </div>
                  ))}
                  {cacheReadTokens > 0 ? (
                    <div className="flex items-center gap-2 text-sm font-medium text-muted-foreground">
                      <span className="size-3 rounded-full bg-emerald-500" />{t("缓存 Tokens")}{compactNumber(cacheReadTokens)}
                    </div>
                  ) : null}
                </div>
              </div>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="border-b border-border p-6">
            <CardTitle className="text-2xl font-bold tracking-normal text-foreground">{t("快捷操作")}</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4 p-6">
            <Link
              to="/tokens"
              className="flex items-center gap-4 rounded-xl border border-border bg-card p-5 transition-colors hover:border-orange-200 hover:bg-orange-50/50 dark:hover:bg-orange-500/10"
            >
              <span className="grid size-14 place-items-center rounded-xl bg-orange-50 text-orange-600 dark:bg-orange-500/10 dark:text-orange-300">
                <KeyRound className="size-6" />
              </span>
              <span className="min-w-0 flex-1">
                <span className="block font-semibold text-foreground">{t("创建 API 密钥")}</span>
                <span className="mt-1 block text-sm font-medium text-muted-foreground">{t("生成新的 API 密钥")}</span>
              </span>
              <ChevronRight className="size-5 shrink-0 text-muted-foreground" />
            </Link>
            <Link
              to="/usage"
              className="flex items-center gap-4 rounded-xl border border-border bg-card p-5 transition-colors hover:border-blue-200 hover:bg-blue-50/50 dark:hover:bg-blue-500/10"
            >
              <span className="grid size-14 place-items-center rounded-xl bg-blue-50 text-blue-600 dark:bg-blue-500/10 dark:text-blue-300">
                <BarChart3 className="size-6" />
              </span>
              <span className="min-w-0 flex-1">
                <span className="block font-semibold text-foreground">{t("查看使用记录")}</span>
                <span className="mt-1 block text-sm font-medium text-muted-foreground">{t("查看详细的调用日志")}</span>
              </span>
              <ChevronRight className="size-5 shrink-0 text-muted-foreground" />
            </Link>
            <Link
              to="/playground"
              className="flex items-center gap-4 rounded-xl border border-border bg-card p-5 transition-colors hover:border-emerald-200 hover:bg-emerald-50/50 dark:hover:bg-emerald-500/10"
            >
              <span className="grid size-14 place-items-center rounded-xl bg-emerald-50 text-emerald-600 dark:bg-emerald-500/10 dark:text-emerald-300">
                <FlaskConical className="size-6" />
              </span>
              <span className="min-w-0 flex-1">
                <span className="block font-semibold text-foreground">{t("在线调试 API")}</span>
                <span className="mt-1 block text-sm font-medium text-muted-foreground">{t("直接发送一条测试请求")}</span>
              </span>
              <ChevronRight className="size-5 shrink-0 text-muted-foreground" />
            </Link>
            <Link
              to="/redeem"
              className="flex items-center gap-4 rounded-xl border border-border bg-card p-5 transition-colors hover:border-violet-200 hover:bg-violet-50/50 dark:hover:bg-violet-500/10"
            >
              <span className="grid size-14 place-items-center rounded-xl bg-violet-50 text-violet-600 dark:bg-violet-500/10 dark:text-violet-300">
                <Gift className="size-6" />
              </span>
              <span className="min-w-0 flex-1">
                <span className="block font-semibold text-foreground">{t("兑换充值码")}</span>
                <span className="mt-1 block text-sm font-medium text-muted-foreground">{t("使用兑换码为账户充值")}</span>
              </span>
              <ChevronRight className="size-5 shrink-0 text-muted-foreground" />
            </Link>
          </CardContent>
        </Card>
      </section>
    </div>
  );
}
