import { useQuery } from '@tanstack/react-query';
import {
  AlertTriangle,
  ArrowLeftRight,
  TrendingUp,
  DollarSign,
  Zap,
  ShieldAlert,
} from 'lucide-react';
import { adminApiClient } from '@/lib/api';
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
import { cn } from '@/lib/utils';
import { locale, t } from '@/lib/i18n';

// ── Types matching the /api/admin/routing-ops response ──────────────────────

interface RoutingOpsSource {
  source_kind: string;
  source_id: number;
  quota: number;
  upstream_cost: number;
  gross_profit: number;
  prompt_tokens: number;
  completion_tokens: number;
  cache_read_tokens: number;
  cache_creation_5m_tokens: number;
  cache_creation_1h_tokens: number;
  count: number;
}

interface RoutingOpsTotals {
  quota: number;
  upstream_cost: number;
  gross_profit: number;
  prompt_tokens: number;
  completion_tokens: number;
  cache_read_tokens: number;
  cache_creation_5m_tokens: number;
  cache_creation_1h_tokens: number;
  count: number;
  channel_count: number;
  subscription_count: number;
}

interface RoutingOpsRates {
  selection_total: number;
  success_total: number;
  error_total: number;
  client_error_total: number;
  fallback_total: number;
  error_rate: number;
  fallback_rate: number;
}

interface RoutingOpsAlert {
  kind: string;
  severity: string;
  message: string;
  detail: string;
}

interface RoutingOpsView {
  success: boolean;
  partial?: boolean;
  errors?: string[];
  window: { start: number; end: number };
  sources: RoutingOpsSource[] | null; // Go nil slice marshals as null
  truncated?: boolean;
  totals: RoutingOpsTotals;
  unpriced: { routed_but_unpriced: number };
  rates?: RoutingOpsRates;
  alerts: RoutingOpsAlert[];
}

// ── Helpers ────────────────────────────────────────────────────────────────

function formatQuotaAsUSD(quota: number): string {
  // Quota is in internal units (1 USD = 10000 units by convention).
  const usd = quota / 10000;
  return usd.toLocaleString('en-US', { style: 'currency', currency: 'USD' });
}

function formatNumber(n: number): string {
  return n.toLocaleString('en-US');
}

function alertIcon(severity: string) {
  if (severity === 'critical') {
    return <ShieldAlert className="h-5 w-5 text-destructive" />;
  }
  return <AlertTriangle className="h-5 w-5 text-yellow-500" />;
}

// ── Page component ─────────────────────────────────────────────────────────

export function RoutingOpsPage() {
  const { data, isLoading, error, refetch } = useQuery<RoutingOpsView>({
    queryKey: ['admin', 'routing-ops'],
    queryFn: async () => {
      const res = await adminApiClient.get<RoutingOpsView>('/admin/routing-ops');
      return res.data;
    },
    refetchInterval: 60_000, // refresh every minute
  });

  if (isLoading) {
    return <MetricCardsSkeleton count={4} />;
  }

  if (error || !data) {
    return (
      <div className="space-y-4">
        <EmptyState
          title={t("无法加载路由运营数据")}
          description={error ? String(error) : t("请检查后端服务状态")}
        />
        <div className="flex justify-center">
          <Button variant="outline" onClick={() => refetch()}>{t("重试")}</Button>
        </div>
      </div>
    );
  }

  const totals = data.totals;
  // The Go backend marshals a nil slice as `"sources":null` (zero buckets,
  // partial failure, or svc==nil early return) — normalize before rendering.
  const sources = data.sources ?? [];
  const channelShare =
    totals.count > 0 && totals.channel_count >= 0
      ? ((totals.channel_count / totals.count) * 100).toFixed(1)
      : 'N/A';
  const subShare =
    totals.count > 0 && totals.subscription_count >= 0
      ? ((totals.subscription_count / totals.count) * 100).toFixed(1)
      : 'N/A';

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">{t("路由运营视图")}</h1>
          <p className="text-sm text-muted-foreground">{t("跨来源流量、成本与告警（窗口：")}{data.window.start ? new Date(data.window.start * 1000).toLocaleString(locale()) : '-'} —{' '}
            {data.window.end ? new Date(data.window.end * 1000).toLocaleString(locale()) : '-'})
          </p>
        </div>
        <Button variant="outline" size="sm" onClick={() => refetch()}>{t("刷新")}</Button>
      </div>

      {/* Dependency errors banner */}
      {data.partial && data.errors && data.errors.length > 0 && (
        <Card className="border-yellow-500/50 bg-yellow-500/5">
          <CardContent className="pt-6">
            <div className="flex items-start gap-3">
              <AlertTriangle className="mt-0.5 h-5 w-5 shrink-0 text-yellow-500" />
              <div className="space-y-1">
                <p className="font-medium text-yellow-700 dark:text-yellow-400">{t("部分数据加载失败")}</p>
                <ul className="text-sm text-muted-foreground">
                  {data.errors.map((e, i) => (
                    <li key={i}>{e}</li>
                  ))}
                </ul>
              </div>
            </div>
          </CardContent>
        </Card>
      )}

      {/* Alerts banner */}
      {data.alerts && data.alerts.length > 0 && (
        <div className="space-y-2">
          {data.alerts.map((alert, i) => (
            <Card
              key={i}
              className={cn(
                alert.severity === 'critical'
                  ? 'border-destructive/50 bg-destructive/5'
                  : 'border-yellow-500/50 bg-yellow-500/5',
              )}
            >
              <CardContent className="pt-6">
                <div className="flex items-start gap-3">
                  {alertIcon(alert.severity)}
                  <div className="space-y-1">
                    <p className="font-medium">{alert.message}</p>
                    {alert.detail && (
                      <p className="text-sm text-muted-foreground">{alert.detail}</p>
                    )}
                  </div>
                </div>
              </CardContent>
            </Card>
          ))}
        </div>
      )}

      {/* Metric cards */}
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">{t("总请求数")}</CardTitle>
            <Zap className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">{formatNumber(totals.count)}</div>
            <p className="text-xs text-muted-foreground">{t("渠道")}{channelShare}{t("% · 订阅")}{subShare}%
            </p>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">{t("收入")}</CardTitle>
            <DollarSign className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">{formatQuotaAsUSD(totals.quota)}</div>
            <p className="text-xs text-muted-foreground">{t("用户扣费总额")}</p>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">{t("上游成本")}</CardTitle>
            <TrendingUp className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-bold">{formatQuotaAsUSD(totals.upstream_cost)}</div>
            <p className="text-xs text-muted-foreground">{t("供应商采购成本")}</p>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-medium">{t("毛利")}</CardTitle>
            <ArrowLeftRight className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent>
            <div
              className={cn(
                'text-2xl font-bold',
                totals.gross_profit < 0 && 'text-destructive',
              )}
            >
              {formatQuotaAsUSD(totals.gross_profit)}
            </div>
            <p className="text-xs text-muted-foreground">{t("收入 − 上游成本")}</p>
          </CardContent>
        </Card>
      </div>

      {/* Routing rates (fallback + error) */}
      {data.rates && (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <Card>
            <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
              <CardTitle className="text-sm font-medium">{t("总选择数")}</CardTitle>
              <ArrowLeftRight className="h-4 w-4 text-muted-foreground" />
            </CardHeader>
            <CardContent>
              <div className="text-2xl font-bold">{formatNumber(data.rates.selection_total)}</div>
              <p className="text-xs text-muted-foreground">{t("进程启动至今累计")}</p>
            </CardContent>
          </Card>
          <Card>
            <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
              <CardTitle className="text-sm font-medium">{t("成功率")}</CardTitle>
              <TrendingUp className="h-4 w-4 text-muted-foreground" />
            </CardHeader>
            <CardContent>
              <div className="text-2xl font-bold">
                {data.rates.selection_total > 0
                  ? ((data.rates.success_total / data.rates.selection_total) * 100).toFixed(1) + '%'
                  : 'N/A'}
              </div>
              <p className="text-xs text-muted-foreground">{t("成功")}{formatNumber(data.rates.success_total)} / {formatNumber(data.rates.selection_total)}
              </p>
            </CardContent>
          </Card>
          <Card>
            <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
              <CardTitle className="text-sm font-medium">{t("错误率")}</CardTitle>
              <ShieldAlert className="h-4 w-4 text-muted-foreground" />
            </CardHeader>
            <CardContent>
              <div
                className={cn(
                  'text-2xl font-bold',
                  data.rates.error_rate > 0.05 && 'text-destructive',
                )}
              >
                {(data.rates.error_rate * 100).toFixed(1)}%
              </div>
              <p className="text-xs text-muted-foreground">{t("错误")}{formatNumber(data.rates.error_total + data.rates.client_error_total)}{t("次")}</p>
            </CardContent>
          </Card>
          <Card>
            <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
              <CardTitle className="text-sm font-medium">{t("回退率")}</CardTitle>
              <AlertTriangle className="h-4 w-4 text-muted-foreground" />
            </CardHeader>
            <CardContent>
              <div
                className={cn(
                  'text-2xl font-bold',
                  data.rates.fallback_rate > 0.1 && 'text-yellow-500',
                )}
              >
                {(data.rates.fallback_rate * 100).toFixed(1)}%
              </div>
              <p className="text-xs text-muted-foreground">{t("来源切换")}{formatNumber(data.rates.fallback_total)}{t("次")}</p>
            </CardContent>
          </Card>
        </div>
      )}

      {/* Source breakdown table */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center justify-between">
            <span>{t("来源明细")}</span>
            {data.truncated && (
              <span className="text-xs font-normal text-yellow-500">{t("仅显示前 200 条（已截断，合计使用全局数据）")}</span>
            )}
          </CardTitle>
        </CardHeader>
        <CardContent>
          {sources.length === 0 ? (
            <EmptyState
              title={t("无流量数据")}
              description={t("当前时间窗口内没有路由记录")}
            />
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("来源类型")}</TableHead>
                  <TableHead>{t("来源 ID")}</TableHead>
                  <TableHead className="text-right">{t("请求数")}</TableHead>
                  <TableHead className="text-right">{t("收入")}</TableHead>
                  <TableHead className="text-right">{t("上游成本")}</TableHead>
                  <TableHead className="text-right">{t("毛利")}</TableHead>
                  <TableHead className="text-right">Prompt</TableHead>
                  <TableHead className="text-right">Completion</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {sources.map((src, i) => (
                  <TableRow key={`${src.source_kind}-${src.source_id}-${i}`}>
                    <TableCell className="font-medium">
                      <span
                        className={cn(
                          'inline-flex rounded px-2 py-0.5 text-xs',
                          src.source_kind === 'channel'
                            ? 'bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-300'
                            : 'bg-purple-100 text-purple-700 dark:bg-purple-900/30 dark:text-purple-300',
                        )}
                      >
                        {src.source_kind === 'channel' ? t("渠道") : t("订阅")}
                      </span>
                    </TableCell>
                    <TableCell>{src.source_id}</TableCell>
                    <TableCell className="text-right">{formatNumber(src.count)}</TableCell>
                    <TableCell className="text-right">{formatQuotaAsUSD(src.quota)}</TableCell>
                    <TableCell className="text-right">
                      {formatQuotaAsUSD(src.upstream_cost)}
                    </TableCell>
                    <TableCell
                      className={cn(
                        'text-right',
                        src.gross_profit < 0 && 'text-destructive font-medium',
                      )}
                    >
                      {formatQuotaAsUSD(src.gross_profit)}
                    </TableCell>
                    <TableCell className="text-right text-muted-foreground">
                      {formatNumber(src.prompt_tokens)}
                    </TableCell>
                    <TableCell className="text-right text-muted-foreground">
                      {formatNumber(src.completion_tokens)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
