import { Link } from 'react-router-dom';
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
import { BarChart3, Box, ChevronRight, Gift, KeyRound, Sparkles, WalletCards, Zap } from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import { apiClient } from '@/lib/api';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { EmptyState } from '@/components/EmptyState';
import { ChartSkeleton, MetricCardsSkeleton } from '@/components/LoadingStates';
import { unwrapApiData } from '@/lib/api-response';
import { cn } from '@/lib/utils';

interface UsageItem {
  date?: string;
  day?: string;
  count: number;
  quota: number;
  prompt_tokens?: number;
  completion_tokens?: number;
  cache_read_tokens?: number;
}

interface UserSelf {
  id: number;
  username: string;
  display_name: string;
  role: number;
}

interface AccountDashboard {
  quota?: number;
  used_quota?: number;
  request_count?: number;
  frozen_quota?: number;
  group?: string;
  group_ratio?: number;
  usage?: UsageItem[];
  today_quota?: number;
  today_prompt_tokens?: number;
  today_completion_tokens?: number;
  today_cache_read_tokens?: number;
  avg_latency?: number;
  model_distribution?: ModelDistributionItem[];
}

interface ModelDistributionItem {
  model: string;
  tokens: number;
}

interface Token {
  id: number;
  name?: string;
  status: number;
}

interface TokenListData {
  items?: Token[];
  total?: number;
}

function formatQuota(q: number, digits = 2) {
  return (q / 500000).toFixed(digits);
}

function formatMoney(q: number, digits = 2) {
  return `US$${formatQuota(q, digits)}`;
}

function compactNumber(value: number) {
  if (value >= 1000000) {
    return `${(value / 1000000).toFixed(2)}M`;
  }
  if (value >= 1000) {
    return `${(value / 1000).toFixed(1)}K`;
  }
  return value.toLocaleString();
}

function numberOrZero(value: unknown) {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0;
}

function nonCachedInputTokens(item: UsageItem) {
  const inputTokens = item.prompt_tokens || 0;
  const cacheReadTokens = item.cache_read_tokens || 0;
  if (cacheReadTokens <= 0) return inputTokens;
  return Math.max(0, inputTokens - cacheReadTokens);
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
  if (hour < 6) return '凌晨好';
  if (hour < 12) return '上午好';
  if (hour < 18) return '下午好';
  return '晚上好';
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
    <Card className="min-h-40 rounded-lg border-0 bg-white shadow-sm ring-1 ring-slate-200 dark:bg-card dark:ring-white/10">
      <CardContent className="flex h-full flex-col justify-between p-5">
        <div className="flex items-start justify-between gap-4">
          <span className="text-sm font-bold text-slate-500 dark:text-slate-400">{title}</span>
          <span className={cn('grid size-12 shrink-0 place-items-center rounded-lg', styles)}>
            <Icon className="size-5" />
          </span>
        </div>
        <div>
          <div className={cn('break-words text-3xl font-black tracking-normal', styles.split(' ')[0])}>{value}</div>
          <div className="mt-4 text-sm font-semibold text-slate-400">{subtitle}</div>
        </div>
      </CardContent>
    </Card>
  );
}

export function DashboardPage() {
  const { data: user, isLoading: isUserLoading } = useQuery({
    queryKey: ['user-self'],
    queryFn: async () => {
      const res = await apiClient.get('/user/self');
      return unwrapApiData<UserSelf>(res.data);
    },
  });

  const { data: dashboard, isLoading } = useQuery({
    queryKey: ['dashboard-summary'],
    queryFn: async () => {
      const res = await apiClient.get('/user/dashboard');
      return unwrapApiData<AccountDashboard>(res.data);
    },
  });

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
  const promptTokens = items.reduce((s, x) => s + nonCachedInputTokens(x), 0);
  const completionTokens = items.reduce((s, x) => s + (x.completion_tokens || 0), 0);
  const cacheReadTokens = items.reduce((s, x) => s + (x.cache_read_tokens || 0), 0);
  const totalTokens = promptTokens + completionTokens + cacheReadTokens;
  const remainingQuota = numberOrZero(dashboard?.quota);
  const usedQuota = numberOrZero(dashboard?.used_quota);
  const requestCount = items.length > 0 ? totalCount : numberOrZero(dashboard?.request_count);
  const todayRequests = latest?.count ?? 0;
  const todayQuota = dashboard?.today_quota ?? latest?.quota ?? 0;
  const todayPromptTokens = latest ? nonCachedInputTokens(latest) : dashboard?.today_prompt_tokens ?? 0;
  const todayCompletionTokens = dashboard?.today_completion_tokens ?? latest?.completion_tokens ?? 0;
  const todayCacheReadTokens = dashboard?.today_cache_read_tokens ?? latest?.cache_read_tokens ?? 0;
  const avgLatency = dashboard?.avg_latency ?? 0;
  const chartData = items.map((item) => ({
    ...item,
    label: item.date || item.day,
    input_tokens: nonCachedInputTokens(item),
    output_tokens: item.completion_tokens || 0,
    cache_read_tokens: item.cache_read_tokens || 0,
  }));
  const tokenCount = tokens?.length ?? 0;
  const activeTokenCount = tokens?.filter((token) => token.status === 1).length ?? tokenCount;
  const isSummaryLoading = isUserLoading || isLoading || isTokensLoading;
  const displayName = user?.display_name || user?.username || '用户';

  // Model distribution from backend
  const modelDistribution = dashboard?.model_distribution ?? [];
  const modelColors = ['#f97316', '#2563eb', '#10b981', '#8b5cf6', '#ef4444', '#06b6d4', '#f59e0b', '#6366f1', '#ec4899', '#14b8a6'];
  const pieData = modelDistribution.length > 0
    ? modelDistribution.map((item, index) => ({
        name: item.model,
        value: item.tokens,
        color: modelColors[index % modelColors.length],
      }))
    : [
        { name: '输入 Tokens', value: promptTokens, color: '#f97316' },
        { name: '输出 Tokens', value: completionTokens, color: '#2563eb' },
      ].filter((item) => item.value > 0);
  const distributionData = pieData.length > 0 ? pieData : [{ name: '总 Tokens', value: totalTokens || 1, color: '#f97316' }];

  return (
    <div className="space-y-7">
      <section>
        <h2 className="text-4xl font-black tracking-normal text-slate-950 dark:text-white">
          {getGreeting()}，{displayName}
        </h2>
        <p className="mt-4 text-lg font-medium text-slate-500 dark:text-slate-400">
          欢迎使用 Micro API 中转平台，实时掌握你的 API 使用情况。
        </p>
      </section>

      <section className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-6">
        {isSummaryLoading ? (
          <MetricCardsSkeleton />
        ) : (
          <>
            <MetricCard title="剩余额度" value={formatMoney(remainingQuota)} subtitle="可用余额" tone="orange" icon={WalletCards} />
            <MetricCard title="已用额度" value={`$${formatQuota(usedQuota, 4)}`} subtitle="累计消耗" tone="purple" icon={Sparkles} />
            <MetricCard title="调用次数" value={requestCount.toLocaleString()} subtitle={`今日 ${todayRequests.toLocaleString()}`} tone="green" icon={BarChart3} />
            <MetricCard title="API 密钥" value={tokenCount.toLocaleString()} subtitle={`可用 ${activeTokenCount.toLocaleString()}`} tone="blue" icon={KeyRound} />
            <MetricCard title="今日消耗" value={`$${formatQuota(todayQuota, 4)}`} subtitle={`今日 Token ${compactNumber(todayPromptTokens + todayCompletionTokens)} / 缓存 ${compactNumber(todayCacheReadTokens)}`} tone="amber" icon={Box} />
            <MetricCard title="平均延迟" value={avgLatency > 0 ? `${(avgLatency / 1000).toFixed(2)}s` : "-"} subtitle={avgLatency > 0 ? `${totalCount} 次调用` : "暂无数据"} tone="blue" icon={Zap} />
          </>
        )}
      </section>

      <section className="grid grid-cols-1 gap-5 xl:grid-cols-[minmax(0,1.25fr)_minmax(340px,0.75fr)_360px]">
        <Card className="rounded-lg border-0 bg-white shadow-sm ring-1 ring-slate-200 dark:bg-card dark:ring-white/10">
          <CardHeader className="border-b border-slate-100 p-6 dark:border-white/10">
            <div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
              <div>
                <CardTitle className="text-2xl font-black tracking-normal text-slate-950 dark:text-white">
                  Token 使用趋势
                </CardTitle>
                <p className="mt-3 text-base font-semibold text-slate-500 dark:text-slate-400">
                  总量 {compactNumber(totalTokens)} Tokens
                </p>
                <p className="mt-1 text-sm font-medium text-slate-500 dark:text-slate-400">
                  输入 {compactNumber(promptTokens)} / 输出 {compactNumber(completionTokens)} / 缓存 {compactNumber(cacheReadTokens)}
                </p>
              </div>
              <div className="h-11 rounded-lg border border-slate-200 px-4 text-sm font-bold leading-11 text-slate-700 dark:border-white/10 dark:text-slate-200">
                近 7 天
              </div>
            </div>
          </CardHeader>
          <CardContent className="p-6">
            {isLoading ? (
              <ChartSkeleton />
            ) : chartData.length === 0 ? (
              <EmptyState title="暂无使用数据" description="请求完成后会在这里展示 Token 使用趋势。" />
            ) : (
              <ResponsiveContainer width="100%" height={300}>
                <AreaChart data={chartData} margin={{ left: 0, right: 8, top: 20, bottom: 0 }}>
                  <defs>
                    <linearGradient id="inputTokens" x1="0" x2="0" y1="0" y2="1">
                      <stop offset="0%" stopColor="#f97316" stopOpacity={0.24} />
                      <stop offset="100%" stopColor="#f97316" stopOpacity={0.02} />
                    </linearGradient>
                    <linearGradient id="cacheReadTokens" x1="0" x2="0" y1="0" y2="1">
                      <stop offset="0%" stopColor="#10b981" stopOpacity={0.20} />
                      <stop offset="100%" stopColor="#10b981" stopOpacity={0.02} />
                    </linearGradient>
                  </defs>
                  <CartesianGrid stroke="#e5e7eb" strokeDasharray="4 4" vertical={false} />
                  <XAxis dataKey="label" tick={{ fontSize: 12, fill: '#94a3b8' }} tickLine={false} axisLine={false} />
                  <YAxis tick={{ fontSize: 12, fill: '#94a3b8' }} tickLine={false} axisLine={false} width={48} tickFormatter={compactNumber} />
                  <Tooltip formatter={(value) => compactNumber(Number(value))} />
                  <Area
                    type="monotone"
                    dataKey="input_tokens"
                    name="输入 Tokens"
                    stroke="#f97316"
                    strokeWidth={3}
                    fill="url(#inputTokens)"
                  />
                  <Area
                    type="monotone"
                    dataKey="output_tokens"
                    name="输出 Tokens"
                    stroke="#2563eb"
                    strokeWidth={3}
                    fill="transparent"
                  />
                  <Area
                    type="monotone"
                    dataKey="cache_read_tokens"
                    name="缓存 Tokens"
                    stroke="#10b981"
                    strokeWidth={2}
                    fill="url(#cacheReadTokens)"
                  />
                </AreaChart>
              </ResponsiveContainer>
            )}
          </CardContent>
        </Card>

        <Card className="rounded-lg border-0 bg-white shadow-sm ring-1 ring-slate-200 dark:bg-card dark:ring-white/10">
          <CardHeader className="border-b border-slate-100 p-6 dark:border-white/10">
            <CardTitle className="text-2xl font-black tracking-normal text-slate-950 dark:text-white">
              {modelDistribution.length > 0 ? "模型分布" : "Token 分布"}
            </CardTitle>
          </CardHeader>
          <CardContent className="p-6">
            {isLoading ? (
              <ChartSkeleton />
            ) : totalTokens === 0 ? (
              <EmptyState title="暂无分布数据" description="产生 Token 消耗后会展示占比。" />
            ) : (
              <div className="grid min-h-[300px] place-items-center">
                <ResponsiveContainer width="100%" height={260}>
                  <PieChart>
                    <Pie data={distributionData} dataKey="value" innerRadius="62%" outerRadius="86%" paddingAngle={3}>
                      {distributionData.map((entry) => (
                        <Cell key={entry.name} fill={entry.color} />
                      ))}
                    </Pie>
                    <Tooltip formatter={(value) => compactNumber(Number(value))} />
                  </PieChart>
                </ResponsiveContainer>
                <div className="-mt-40 text-center">
                  <div className="text-4xl font-black text-slate-950 dark:text-white">{compactNumber(totalTokens)}</div>
                  <div className="mt-2 text-sm font-semibold text-slate-400">总 Tokens</div>
                </div>
                <div className="mt-12 flex flex-wrap justify-center gap-4">
                  {distributionData.map((entry) => (
                    <div key={entry.name} className="flex items-center gap-2 text-sm font-semibold text-slate-500 dark:text-slate-400">
                      <span className="size-3 rounded-full" style={{ backgroundColor: entry.color }} />
                      {entry.name}
                    </div>
                  ))}
                  {cacheReadTokens > 0 ? (
                    <div className="flex items-center gap-2 text-sm font-semibold text-slate-500 dark:text-slate-400">
                      <span className="size-3 rounded-full bg-emerald-500" />
                      缓存 Tokens {compactNumber(cacheReadTokens)}
                    </div>
                  ) : null}
                </div>
              </div>
            )}
          </CardContent>
        </Card>

        <Card className="rounded-lg border-0 bg-white shadow-sm ring-1 ring-slate-200 dark:bg-card dark:ring-white/10">
          <CardHeader className="border-b border-slate-100 p-6 dark:border-white/10">
            <CardTitle className="text-2xl font-black tracking-normal text-slate-950 dark:text-white">
              快捷操作
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-4 p-6">
            <Link
              to="/tokens"
              className="flex items-center gap-4 rounded-lg border border-slate-100 p-5 transition-colors hover:border-orange-200 hover:bg-orange-50/50 dark:border-white/10 dark:hover:bg-orange-500/10"
            >
              <span className="grid size-14 place-items-center rounded-lg bg-orange-50 text-orange-600 dark:bg-orange-500/10 dark:text-orange-300">
                <KeyRound className="size-6" />
              </span>
              <span className="min-w-0 flex-1">
                <span className="block font-black text-slate-950 dark:text-white">创建 API 密钥</span>
                <span className="mt-1 block text-sm font-medium text-slate-400">生成新的 API 密钥</span>
              </span>
              <ChevronRight className="size-5 shrink-0 text-slate-300" />
            </Link>
            <Link
              to="/usage"
              className="flex items-center gap-4 rounded-lg border border-slate-100 p-5 transition-colors hover:border-blue-200 hover:bg-blue-50/50 dark:border-white/10 dark:hover:bg-blue-500/10"
            >
              <span className="grid size-14 place-items-center rounded-lg bg-blue-50 text-blue-600 dark:bg-blue-500/10 dark:text-blue-300">
                <BarChart3 className="size-6" />
              </span>
              <span className="min-w-0 flex-1">
                <span className="block font-black text-slate-950 dark:text-white">查看使用记录</span>
                <span className="mt-1 block text-sm font-medium text-slate-400">查看详细的调用日志</span>
              </span>
              <ChevronRight className="size-5 shrink-0 text-slate-300" />
            </Link>
            <Link
              to="/redeem"
              className="flex items-center gap-4 rounded-lg border border-slate-100 p-5 transition-colors hover:border-violet-200 hover:bg-violet-50/50 dark:border-white/10 dark:hover:bg-violet-500/10"
            >
              <span className="grid size-14 place-items-center rounded-lg bg-violet-50 text-violet-600 dark:bg-violet-500/10 dark:text-violet-300">
                <Gift className="size-6" />
              </span>
              <span className="min-w-0 flex-1">
                <span className="block font-black text-slate-950 dark:text-white">兑换充值码</span>
                <span className="mt-1 block text-sm font-medium text-slate-400">使用兑换码为账户充值</span>
              </span>
              <ChevronRight className="size-5 shrink-0 text-slate-300" />
            </Link>
          </CardContent>
        </Card>
      </section>
    </div>
  );
}
