import { useQuery } from '@tanstack/react-query';
import {
  Activity,
  AlertCircle,
  AlertTriangle,
  CheckCircle2,
  Clock,
  RefreshCw,
  TrendingUp,
  Zap,
} from 'lucide-react';
import { useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { adminApiClient } from '@/lib/api';
import { unwrapApiData } from '@/lib/api-response';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { EmptyState } from '@/components/EmptyState';
import { MetricCardsSkeleton } from '@/components/LoadingStates';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { HealthDistributionChart } from '@/components/admin/HealthCharts';
import { AccountStatusBadge } from '@/components/admin/AccountStatusBadge';
import {
  normalizeSubscriptionAccount,
  type RawSubscriptionAccount,
  type SubscriptionAccountSummary,
} from '@/lib/subscription-account';
import { cn } from '@/lib/utils';

interface ChannelHealth {
  id: string;
  name: string;
  status: string;
  healthStatus?: string;
  health_status?: string;
  healthLastError?: string;
  health_last_error?: string;
  healthConsecutiveFailures?: number;
  health_consecutive_failures?: number;
  healthLastSuccessTime?: string;
  health_last_success_time?: string;
  healthLastFailureTime?: string;
  health_last_failure_time?: string;
  circuitOpenedUntil?: string | number;
  circuit_opened_until?: string | number;
  type?: number;
  group?: string;
  balance?: number;
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

function channelHealthStatus(channel: ChannelHealth) {
  // If channel has explicit health status, use it
  if (channel.healthStatus || channel.health_status) {
    return channel.healthStatus || channel.health_status || 'healthy';
  }
  // If channel is disabled, consider it "unavailable" for health monitoring purposes
  if (Number(channel.status) !== 1) {
    return 'unavailable';
  }
  // For enabled channels without explicit health data, mark as "unknown" rather than assuming healthy
  // This provides a more accurate picture when health checks haven't run yet
  return 'unknown';
}

function channelHealthError(channel: ChannelHealth) {
  return channel.healthLastError || channel.health_last_error || '';
}

function channelHealthFailures(channel: ChannelHealth) {
  return Number(channel.healthConsecutiveFailures ?? channel.health_consecutive_failures ?? 0);
}

function channelCircuitUntil(channel: ChannelHealth) {
  return Number(channel.circuitOpenedUntil ?? channel.circuit_opened_until ?? 0);
}

function healthBadgeClass(status: string) {
  if (status === 'unavailable') return 'bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-200';
  if (status === 'degraded') return 'bg-amber-100 text-amber-800 dark:bg-amber-900 dark:text-amber-200';
  if (status === 'unknown') return 'bg-slate-100 text-slate-800 dark:bg-slate-900 dark:text-slate-200';
  return 'bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200';
}

function healthIcon(status: string) {
  if (status === 'unavailable') return <AlertCircle className="size-4 text-red-600 dark:text-red-400" />;
  if (status === 'degraded') return <AlertTriangle className="size-4 text-amber-600 dark:text-amber-400" />;
  if (status === 'unknown') return <Clock className="size-4 text-slate-600 dark:text-slate-400" />;
  return <CheckCircle2 className="size-4 text-green-600 dark:text-green-400" />;
}

function formatTime(dateString?: string | number): string {
  if (!dateString) return '-';
  try {
    const date = typeof dateString === 'number' ? new Date(dateString) : new Date(dateString);
    return date.toLocaleString('zh-CN');
  } catch {
    return String(dateString);
  }
}

function MetricCard({
  title,
  value,
  subtitle,
  tone,
  icon: Icon,
}: {
  title: string;
  value: string | number;
  subtitle: string;
  tone: 'red' | 'amber' | 'green' | 'blue';
  icon: React.ElementType;
}) {
  const styles = {
    red: 'text-red-600 bg-red-50 dark:bg-red-500/10 dark:text-red-300',
    amber: 'text-amber-600 bg-amber-50 dark:bg-amber-500/10 dark:text-amber-300',
    green: 'text-emerald-600 bg-emerald-50 dark:bg-emerald-500/10 dark:text-emerald-300',
    blue: 'text-blue-600 bg-blue-50 dark:bg-blue-500/10 dark:text-blue-300',
  }[tone];

  return (
    <Card className="min-h-32 rounded-lg border-0 bg-white shadow-sm ring-1 ring-slate-200 dark:bg-card dark:ring-white/10">
      <CardContent className="flex h-full flex-col justify-between p-5">
        <div className="flex items-start justify-between gap-4">
          <span className="text-sm font-bold text-slate-500 dark:text-slate-400">{title}</span>
          <span className={cn('grid size-12 shrink-0 place-items-center rounded-lg', styles)}>
            <Icon className="size-5" />
          </span>
        </div>
        <div>
          <div className={cn('break-words text-3xl font-black tracking-normal', styles.split(' ')[0])}>
            {value}
          </div>
          <div className="mt-4 text-sm font-semibold text-slate-400">{subtitle}</div>
        </div>
      </CardContent>
    </Card>
  );
}

export function ChannelHealthPage() {
  const [autoRefresh, setAutoRefresh] = useState(true);
  const navigate = useNavigate();

  const { data: channels, isLoading, refetch, isFetching } = useQuery({
    queryKey: ['admin-channels-health'],
    queryFn: async () => {
      // Fetch all channels without status filter to show complete health monitoring
      const params = new URLSearchParams({ page: '1', page_size: '1000' });
      const res = await adminApiClient.get(`/channel?${params}`);
      return unwrapApiData<ChannelHealth[]>(res.data);
    },
    refetchInterval: autoRefresh ? 30000 : false, // Poll every 30 seconds when auto-refresh is on
  });

  const healthMetrics = useMemo(() => {
    const all = channels ?? [];
    const healthy = all.filter((ch) => channelHealthStatus(ch) === 'healthy');
    const degraded = all.filter((ch) => channelHealthStatus(ch) === 'degraded');
    const unavailable = all.filter((ch) => channelHealthStatus(ch) === 'unavailable');
    const unknown = all.filter((ch) => channelHealthStatus(ch) === 'unknown');
    const total = all.length;

    // Calculate health rate only for channels with explicit health status (excluding unknown)
    const channelsWithHealthData = all.filter((ch) => {
      const status = channelHealthStatus(ch);
      return status === 'healthy' || status === 'degraded' || status === 'unavailable';
    });
    const healthRate = channelsWithHealthData.length > 0
      ? ((healthy.length / channelsWithHealthData.length) * 100).toFixed(1)
      : '0.0';

    return {
      total,
      healthy: healthy.length,
      degraded: degraded.length,
      unavailable: unavailable.length,
      unknown: unknown.length,
      healthRate,
    };
  }, [channels]);

  const unhealthyChannels = useMemo(() => {
    return (channels ?? []).filter((ch) => {
      const status = channelHealthStatus(ch);
      return status === 'unavailable' || status === 'degraded';
    });
  }, [channels]);

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-2xl font-semibold">渠道健康监控</h2>
          <p className="mt-2 text-sm text-muted-foreground">
            实时监控所有渠道的健康状态和可用性（包含启用和禁用的渠道）
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => setAutoRefresh(!autoRefresh)}
            className={cn(autoRefresh && 'bg-green-50 text-green-700 dark:bg-green-500/10 dark:text-green-300')}
          >
            {autoRefresh ? <Activity className="size-4 mr-2" /> : <Clock className="size-4 mr-2" />}
            {autoRefresh ? '自动刷新开启' : '自动刷新关闭'}
          </Button>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => void refetch()}
            disabled={isFetching}
          >
            <RefreshCw className={cn('size-4 mr-2', isFetching && 'animate-spin')} />
            刷新
          </Button>
        </div>
      </div>

      {/* Health Metrics */}
      {isLoading ? (
        <MetricCardsSkeleton />
      ) : (
        <section className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-6">
          <MetricCard
            title="总渠道数"
            value={healthMetrics.total}
            subtitle="配置的渠道总数"
            tone="blue"
            icon={Database}
          />
          <MetricCard
            title="健康渠道"
            value={healthMetrics.healthy}
            subtitle="运行正常"
            tone="green"
            icon={CheckCircle2}
          />
          <MetricCard
            title="降级渠道"
            value={healthMetrics.degraded}
            subtitle="性能下降"
            tone="amber"
            icon={AlertTriangle}
          />
          <MetricCard
            title="不可用"
            value={healthMetrics.unavailable}
            subtitle="无法访问"
            tone="red"
            icon={AlertCircle}
          />
          <MetricCard
            title="未知状态"
            value={healthMetrics.unknown}
            subtitle="暂无数据"
            tone="blue"
            icon={Clock}
          />
          <MetricCard
            title="健康率"
            value={`${healthMetrics.healthRate}%`}
            subtitle="可用性比例"
            tone="green"
            icon={TrendingUp}
          />
        </section>
      )}

      {/* Health Distribution Chart */}
      {channels && channels.length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle className="text-xl font-black">渠道健康分布</CardTitle>
          </CardHeader>
          <CardContent>
            <HealthDistributionChart
              healthy={healthMetrics.healthy}
              degraded={healthMetrics.degraded}
              unavailable={healthMetrics.unavailable}
              unknown={healthMetrics.unknown}
            />
          </CardContent>
        </Card>
      )}

      {/* Unhealthy Channels Alert */}
      {unhealthyChannels.length > 0 && (
        <Card className="border-amber-200 bg-amber-50 dark:border-amber-500/30 dark:bg-amber-500/10">
          <CardContent className="flex items-center gap-3 p-4">
            <AlertTriangle className="size-5 shrink-0 text-amber-600 dark:text-amber-300" />
            <div className="min-w-0 flex-1">
              <div className="font-semibold text-amber-950 dark:text-amber-100">
                {unhealthyChannels.length} 个渠道状态异常
              </div>
              <div className="mt-1 text-sm text-amber-900/80 dark:text-amber-100/80">
                {unhealthyChannels.slice(0, 3).map((ch) => (
                  <span key={ch.id} className="mr-3">
                    {ch.name} ({channelHealthStatus(ch)})
                  </span>
                ))}
                {unhealthyChannels.length > 3 && (
                  <span>以及其他 {unhealthyChannels.length - 3} 个渠道</span>
                )}
              </div>
            </div>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => {
                document.getElementById('unhealthy-channels-container')?.scrollIntoView({ behavior: 'smooth', block: 'start' });
              }}
            >
              查看详情
            </Button>
          </CardContent>
        </Card>
      )}

      {/* Channel Health Grid */}
      <Card>
        <CardHeader>
          <CardTitle className="text-xl font-black">渠道健康状态</CardTitle>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <div className="space-y-4">
              {[1, 2, 3, 4, 5].map((i) => (
                <div key={i} className="h-24 animate-pulse rounded-lg bg-muted/50" />
              ))}
            </div>
          ) : !channels || channels.length === 0 ? (
            <EmptyState title="暂无渠道" description="请先配置渠道" />
          ) : (
            <div className="space-y-2" id="unhealthy-channels-container">
              {channels.map((channel, index) => {
                const status = channelHealthStatus(channel);
                const error = channelHealthError(channel);
                const failures = channelHealthFailures(channel);
                const circuitUntil = channelCircuitUntil(channel);

                // Find the first unhealthy channel index for the anchor id
                const isUnhealthy = status === 'unavailable' || status === 'degraded';
                const firstUnhealthyIndex = channels.findIndex(ch => {
                  const s = channelHealthStatus(ch);
                  return s === 'unavailable' || s === 'degraded';
                });
                const shouldSetId = isUnhealthy && index === firstUnhealthyIndex;

                return (
                  <div
                    key={channel.id}
                    id={shouldSetId ? 'unhealthy-channels' : undefined}
                    className={cn(
                      'flex items-center gap-4 rounded-lg border p-4 transition-colors hover:bg-muted/50',
                      status === 'unavailable' && 'border-red-200 bg-red-50/50 dark:border-red-500/30 dark:bg-red-500/5',
                      status === 'degraded' && 'border-amber-200 bg-amber-50/50 dark:border-amber-500/30 dark:bg-amber-500/5'
                    )}
                  >
                    {/* Status Icon */}
                    <div className="shrink-0">{healthIcon(status)}</div>

                    {/* Channel Info */}
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-3">
                        <span className="font-semibold text-foreground">{channel.name}</span>
                        <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${healthBadgeClass(status)}`}>
                          {status === 'healthy' ? '健康' : status === 'degraded' ? '降级' : status === 'unknown' ? '未知' : '不可用'}
                        </span>
                        {channel.type && PROVIDER_NAMES[channel.type] && (
                          <span className="text-xs text-muted-foreground">
                            {PROVIDER_NAMES[channel.type]}
                          </span>
                        )}
                      </div>
                      {error && (
                        <p className="mt-1 truncate text-sm text-red-600 dark:text-red-400">{error}</p>
                      )}
                      <div className="mt-2 flex flex-wrap gap-3 text-xs text-muted-foreground">
                        {failures > 0 && (
                          <span className="flex items-center gap-1">
                            <AlertCircle className="size-3" />
                            连续失败 {failures} 次
                          </span>
                        )}
                        {circuitUntil > 0 && (
                          <span className="flex items-center gap-1">
                            <Clock className="size-3" />
                            熔断恢复于 {formatTime(circuitUntil * 1000)}
                          </span>
                        )}
                        {channel.balance !== undefined && (
                          <span className="flex items-center gap-1">
                            <Zap className="size-3" />
                            余额 ${channel.balance.toFixed(2)}
                          </span>
                        )}
                        {channel.group && (
                          <span className="flex items-center gap-1">
                            分组: {channel.group}
                          </span>
                        )}
                      </div>
                    </div>

                    {/* Actions */}
                    <div className="flex shrink-0 gap-2">
                      <Button
                        type="button"
                        variant="ghost"
                        size="sm"
                        onClick={() => {
                          navigate(`/admin/channels?search=${encodeURIComponent(channel.name)}`);
                        }}
                      >
                        管理
                      </Button>
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </CardContent>
      </Card>

      {/* Subscription Account Token Health */}
      <SubscriptionAccountHealth autoRefresh={autoRefresh} />

      {/* Health Info */}
      <Card className="bg-muted/50">
        <CardContent className="p-4">
          <div className="flex items-start gap-3 text-sm">
            <InfoIcon className="mt-0.5 size-4 shrink-0 text-blue-600" />
            <div className="space-y-1 text-muted-foreground">
              <p>
                <strong>健康状态说明：</strong>系统会定期检查所有渠道的可用性
              </p>
              <ul className="ml-4 list-disc space-y-1">
                <li><strong>健康：</strong>渠道响应正常，性能良好</li>
                <li><strong>降级：</strong>渠道可用但响应较慢或部分功能受限</li>
                <li><strong>不可用：</strong>渠道无法访问或连续多次失败</li>
                <li><strong>未知：</strong>渠道暂无健康检查数据或检查尚未完成</li>
              </ul>
            </div>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}



const HOUR_S = 3600;
const DAY_S = 86400;
const WEEK_S = 7 * DAY_S;
const FIVE_H_S = 5 * HOUR_S;

function formatPercentCHP(value: number) {
  if (!Number.isFinite(value)) return '-';
  const rounded = Math.round(value * 10) / 10;
  return `${Number.isInteger(rounded) ? rounded.toFixed(0) : rounded.toFixed(1)}%`;
}

function formatResetAfterCHP(seconds?: number | null) {
  if (seconds == null || !Number.isFinite(seconds)) return '';
  if (seconds <= 0) return '即将重置';
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  if (days > 0) return `${days}天${hours > 0 ? `${hours}小时` : ''}后`;
  if (hours > 0) return `${hours}小时${minutes > 0 ? `${minutes}分钟` : ''}后`;
  if (minutes > 0) return `${minutes}分钟后`;
  return '1分钟内';
}

function effectiveWindowUsedCHP(used: number, windowStart: number | undefined, nowUnix: number, windowS: number): number {
  if (!windowStart || windowStart <= 0 || nowUnix - windowStart >= windowS) return 0;
  return used;
}

function SubscriptionQuotaSummary({ account, now }: { account: SubscriptionAccountSummary; now: number }) {
  const localRows: Array<{ label: string; used?: number; limit?: number; windowStart?: number; windowS: number }> = [
    { label: '总额', used: account.quotaUsedUsd, limit: account.quotaLimitUsd, windowS: 0 },
    { label: '5h', used: account.quota5hUsedUsd, limit: account.quota5hLimitUsd, windowStart: account.quota5hWindowStart, windowS: FIVE_H_S },
    { label: '24h', used: account.quotaDailyUsedUsd, limit: account.quotaDailyLimitUsd, windowStart: account.quotaDailyWindowStart, windowS: DAY_S },
    { label: '7d', used: account.quotaWeeklyUsedUsd, limit: account.quotaWeeklyLimitUsd, windowStart: account.quotaWeeklyWindowStart, windowS: WEEK_S },
  ].filter((row) => (row.used ?? 0) > 0 || (row.limit ?? 0) > 0);

  const upstreamWindows: Array<{ key: string; label: string; usedPercent?: number | null; resetAfter?: number | null }> = [
    {
      key: 'primary',
      label: account.primaryQuotaWindowMinutes === 300 ? '5h' : account.primaryQuotaWindowMinutes === 10080 ? '7d' : '主',
      usedPercent: account.primaryQuotaUsedPercent,
      resetAfter: account.primaryQuotaResetAfterSeconds,
    },
    {
      key: 'secondary',
      label: account.secondaryQuotaWindowMinutes === 300 ? '5h' : account.secondaryQuotaWindowMinutes === 10080 ? '7d' : '次',
      usedPercent: account.secondaryQuotaUsedPercent,
      resetAfter: account.secondaryQuotaResetAfterSeconds,
    },
  ].filter((w) => w.usedPercent != null || w.resetAfter != null);

  if (localRows.length === 0 && upstreamWindows.length === 0) {
    if (account.quotaUsedPercent != null || account.quotaResetAt) {
      const usedPercent = account.quotaUsedPercent ?? 0;
      const barColor = usedPercent >= 100 ? 'bg-red-500' : usedPercent >= 80 ? 'bg-amber-500' : 'bg-emerald-500';
      const resetAfter = account.quotaResetAt ? Math.max(0, account.quotaResetAt - now) : null;
      return (
        <div className="min-w-[140px] space-y-0.5">
          <div className="flex items-center justify-between gap-2 text-xs">
            <span className="font-medium">配额</span>
            <span className="tabular-nums text-muted-foreground">{formatPercentCHP(usedPercent)}</span>
          </div>
          <div className="h-1.5 overflow-hidden rounded-full bg-muted">
            <div className={cn('h-full rounded-full transition-all', barColor)} style={{ width: `${Math.min(100, usedPercent)}%` }} />
          </div>
          {resetAfter != null && resetAfter > 0 && <div className="text-[11px] text-muted-foreground">重置：{formatResetAfterCHP(resetAfter)}</div>}
        </div>
      );
    }
    return <span className="text-sm text-muted-foreground">-</span>;
  }

  return (
    <div className="min-w-[170px] space-y-1">
      {localRows.map((row) => {
        const used = effectiveWindowUsedCHP(Number(row.used ?? 0), row.windowStart, now, row.windowS);
        const limit = Number(row.limit ?? 0);
        const ratio = limit > 0 ? used / limit : 0;
        const barWidth = limit > 0 ? Math.min(100, ratio * 100) : 0;
        const barColor = ratio >= 1 ? 'bg-red-500' : ratio >= 0.8 ? 'bg-amber-500' : 'bg-emerald-500';
        return (
          <div key={row.label} className="space-y-0.5">
            <div className="flex items-center justify-between gap-2 text-xs">
              <span className="font-medium">{row.label}</span>
              <span className="tabular-nums text-muted-foreground">
                {`$${used.toFixed(2)}`}
                {limit > 0 ? ` / $${limit.toFixed(2)}` : ''}
              </span>
            </div>
            {limit > 0 && (
              <div className="h-1.5 overflow-hidden rounded-full bg-muted">
                <div className={cn('h-full rounded-full transition-all', barColor)} style={{ width: `${barWidth}%` }} />
              </div>
            )}
          </div>
        );
      })}
      {upstreamWindows.map((window) => {
        const usedPercent = window.usedPercent ?? 0;
        const barWidth = Math.min(100, usedPercent);
        const barColor = usedPercent >= 100 ? 'bg-red-500' : usedPercent >= 80 ? 'bg-amber-500' : 'bg-emerald-500';
        return (
          <div key={window.key} className="space-y-0.5">
            <div className="flex items-center justify-between gap-2 text-xs">
              <span className="font-medium">{window.label}</span>
              <span className="tabular-nums text-muted-foreground">{formatPercentCHP(usedPercent)}</span>
            </div>
            <div className="h-1.5 overflow-hidden rounded-full bg-muted">
              <div className={cn('h-full rounded-full transition-all', barColor)} style={{ width: `${barWidth}%` }} />
            </div>
            {window.resetAfter != null && window.resetAfter > 0 && (
              <div className="text-[11px] text-muted-foreground">重置：{formatResetAfterCHP(window.resetAfter)}</div>
            )}
          </div>
        );
      })}
    </div>
  );
}

function SubscriptionAccountHealth({ autoRefresh }: { autoRefresh: boolean }) {
  const { data: accounts, isLoading, dataUpdatedAt } = useQuery({
    queryKey: ['admin-subscription-accounts-health'],
    queryFn: async () => {
      // The list endpoint is /api/subscription-accounts (plural, alias of
      // /v1/subscription-accounts). It returns { accounts, total } directly —
      // NOT the { success, data } envelope used by one-api compat shims — so
      // we read res.data.accounts instead of unwrapApiData(...).
      const params = new URLSearchParams({ page: '1', page_size: '1000' });
      const res = await adminApiClient.get(`/subscription-accounts?${params}`);
      const payload = res.data as { accounts?: RawSubscriptionAccount[]; total?: number };
      return (payload.accounts ?? []).map(normalizeSubscriptionAccount);
    },
    refetchInterval: autoRefresh ? 30000 : false,
  });
  // Derive "now" from the query's last-updated timestamp so token-expiry
  // badges refresh together with polling, without calling Date.now() in render.
  const now = dataUpdatedAt ? Math.floor(dataUpdatedAt / 1000) : 0;

  const all = accounts ?? [];
  const active = all.filter((a) => a.status === 1);
  // Token health: expired / expiring soon (<1h) / rate-limited / healthy
  const expired = active.filter((a) => a.expires_at && a.expires_at > 0 && a.expires_at <= now);
  const expiringSoon = active.filter((a) => a.expires_at && a.expires_at > now && a.expires_at - now < 3600);
  const rateLimited = active.filter((a) => a.rate_limited_until && a.rate_limited_until > now);
  const healthy = active.filter((a) => {
    const expOk = !a.expires_at || a.expires_at === 0 || a.expires_at > now + 3600;
    const rateOk = !a.rate_limited_until || a.rate_limited_until <= now;
    return expOk && rateOk;
  });

  function formatTime(ts?: number): string {
    if (!ts || ts === 0) return '-';
    try {
      return new Date(ts * 1000).toLocaleString('zh-CN');
    } catch {
      return String(ts);
    }
  }

  if (isLoading) {
    return <MetricCardsSkeleton />;
  }

  if (all.length === 0) {
    return (
      <Card>
        <CardHeader>
          <CardTitle className="text-xl font-black">订阅账号 Token 健康</CardTitle>
        </CardHeader>
        <CardContent>
          <EmptyState
            title="暂无订阅账号"
            description="配置 Codex / Claude OAuth 订阅账号后，这里会展示 Token 过期、限流等健康状态"
          />
        </CardContent>
      </Card>
    );
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-xl font-black">订阅账号 Token 健康</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
          <div className="rounded-lg border p-3">
            <div className="text-2xl font-black text-blue-600">{all.length}</div>
            <div className="text-xs text-muted-foreground">总账号数</div>
          </div>
          <div className="rounded-lg border p-3">
            <div className="text-2xl font-black text-green-600">{healthy.length}</div>
            <div className="text-xs text-muted-foreground">Token 正常</div>
          </div>
          <div className="rounded-lg border p-3">
            <div className="text-2xl font-black text-amber-600">{expiringSoon.length}</div>
            <div className="text-xs text-muted-foreground">即将过期</div>
          </div>
          <div className="rounded-lg border p-3">
            <div className="text-2xl font-black text-red-600">{expired.length}</div>
            <div className="text-xs text-muted-foreground">已过期</div>
          </div>
          <div className="rounded-lg border p-3">
            <div className="text-2xl font-black text-orange-600">{rateLimited.length}</div>
            <div className="text-xs text-muted-foreground">被限流</div>
          </div>
        </div>

        <div className="border rounded-lg overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>名称</TableHead>
                <TableHead>平台</TableHead>
                <TableHead className="hidden md:table-cell">上游账号</TableHead>
                <TableHead>过期时间</TableHead>
                <TableHead className="hidden md:table-cell">最近使用</TableHead>
                <TableHead className="hidden lg:table-cell">限额状态</TableHead>
                <TableHead>状态</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {all.map((account) => {
                return (
                  <TableRow key={account.id}>
                    <TableCell className="font-medium">{account.name}</TableCell>
                    <TableCell>{account.platform}</TableCell>
                    <TableCell className="hidden md:table-cell font-mono text-xs">
                      {account.accountId || '-'}
                    </TableCell>
                    <TableCell className="text-xs">{formatTime(account.expiresAt)}</TableCell>
                    <TableCell className="hidden md:table-cell text-xs">{formatTime(account.lastUsedAt)}</TableCell>
                    <TableCell className="hidden lg:table-cell">
                      <SubscriptionQuotaSummary account={account} now={now} />
                    </TableCell>
                    <TableCell>
                      <AccountStatusBadge
                        info={{
                          status: account.status,
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
                        now={now}
                      />
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </div>
      </CardContent>
    </Card>
  );
}

function InfoIcon({ className }: { className?: string }) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      className={className}
    >
      <circle cx="12" cy="12" r="10" />
      <path d="M12 16v-4" />
      <path d="M12 8h.01" />
    </svg>
  );
}

function Database({ className }: { className?: string }) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      className={className}
    >
      <ellipse cx="12" cy="5" rx="9" ry="3" />
      <path d="M3 5V19A9 3 0 0 0 21 19V5" />
      <path d="M3 12A9 3 0 0 0 21 12" />
    </svg>
  );
}
