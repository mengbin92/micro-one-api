import { useQuery } from '@tanstack/react-query';
import {
  ArrowDownCircle,
  ArrowUpCircle,
  DollarSign,
  Download,
  TrendingDown,
  TrendingUp,
} from 'lucide-react';
import { useMemo } from 'react';
import { toast } from 'sonner';
import { adminApiClient } from '@/lib/api';
import { unwrapApiData } from '@/lib/api-response';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { EmptyState } from '@/components/EmptyState';
import { MetricCardsSkeleton } from '@/components/LoadingStates';
import { ChannelCostComparison, CostBreakdownChart } from '@/components/admin/CostCharts';
import { amountUnitsToCurrencyUnits, formatUSD } from '@/lib/amount';
import { cn } from '@/lib/utils';

const CHART_COLORS = [
  'var(--chart-1)',
  'var(--chart-2)',
  'var(--chart-3)',
  'var(--chart-4)',
  'var(--chart-5)',
];

interface AdminSummary {
  totals?: {
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
  };
  cost_analysis?: {
    revenue_quota?: number;
    upstream_cost?: number;
    gross_profit?: number;
    gross_margin?: number;
    profitable?: boolean;
  };
  top_models?: Array<{
    model?: string;
    quota?: number;
    upstream_cost?: number;
    gross_profit?: number;
    count?: number;
  }>;
  top_channels?: Array<{
    name?: string;
    quota?: number;
    upstream_cost?: number;
    gross_profit?: number;
    count?: number;
  }>;
  top_subscription_accounts?: Array<{
    name?: string;
    platform?: string;
    status?: number;
    account_id?: string;
    expires_at?: number;
    subscription_account_id?: number;
    quota?: number;
    upstream_cost?: number;
    gross_profit?: number;
    count?: number;
    account_event_cost_usd?: number;
    account_event_charged_usd?: number;
    account_event_average_rate_multiplier?: number;
    account_event_count?: number;
    account_event_last_occurred_at?: number;
  }>;
  top_subscription_account_quota_events?: Array<{
    name?: string;
    platform?: string;
    status?: number;
    account_id?: string;
    subscription_account_id?: number;
    cost_usd?: number;
    charged_usd?: number;
    average_rate_multiplier?: number;
    count?: number;
    last_occurred_at?: number;
    ledger_quota?: number;
    ledger_upstream_cost?: number;
    ledger_gross_profit?: number;
    ledger_count?: number;
  }>;
}

function formatMoney(q: number, digits = 4) {
  return formatUSD(q, digits);
}

function formatUSDValue(value: number | undefined, digits = 4) {
  const parsed = Number(value ?? 0);
  if (!Number.isFinite(parsed)) return '$0.0000';
  return `$${parsed.toFixed(digits)}`;
}

function MetricCard({
  title,
  value,
  subtitle,
  tone,
  trend,
  icon: Icon,
}: {
  title: string;
  value: string;
  subtitle: string;
  tone: 'green' | 'red' | 'blue' | 'purple';
  trend?: 'up' | 'down' | 'neutral';
  icon: React.ElementType;
}) {
  const styles = {
    green: 'text-emerald-600 bg-emerald-50 dark:bg-emerald-500/10 dark:text-emerald-300',
    red: 'text-red-600 bg-red-50 dark:bg-red-500/10 dark:text-red-300',
    blue: 'text-blue-600 bg-blue-50 dark:bg-blue-500/10 dark:text-blue-300',
    purple: 'text-violet-600 bg-violet-50 dark:bg-violet-500/10 dark:text-violet-300',
  }[tone];

  return (
    <Card className="min-h-36">
      <CardContent className="flex h-full flex-col justify-between p-5">
        <div className="flex items-start justify-between gap-4">
          <span className="text-sm font-medium text-muted-foreground">{title}</span>
          <span className={cn('grid size-12 shrink-0 place-items-center rounded-lg', styles)}>
            <Icon className="size-5" />
          </span>
        </div>
        <div>
          <div className={cn('break-words text-3xl font-semibold tracking-normal', styles.split(' ')[0])}>
            {value}
          </div>
          <div className="mt-3 flex items-center gap-2 text-sm font-medium text-muted-foreground">
            {trend === 'up' && <TrendingUp className="size-4 text-green-600" />}
            {trend === 'down' && <TrendingDown className="size-4 text-red-600" />}
            {subtitle}
          </div>
        </div>
      </CardContent>
    </Card>
  );
}

export function CostAnalysisPage() {
  const { data: summary, isLoading } = useQuery({
    queryKey: ['admin-summary'],
    queryFn: async () => {
      const res = await adminApiClient.get('/admin/summary');
      return unwrapApiData<AdminSummary>(res.data);
    },
  });

  const costMetrics = useMemo(() => {
    const totals = summary?.totals ?? {};
    const costAnalysis = summary?.cost_analysis ?? {};

    // Use cost_analysis if available, otherwise fall back to totals
    const revenue = costAnalysis.revenue_quota ?? 0;
    const upstreamCost = costAnalysis.upstream_cost ?? totals.upstream_cost ?? 0;
    const grossProfit = costAnalysis.gross_profit ?? totals.gross_profit ?? 0;
    const margin = costAnalysis.gross_margin ?? (revenue > 0 ? ((grossProfit / revenue) * 100) : 0);

    return {
      revenue,
      upstreamCost,
      grossProfit,
      margin,
    };
  }, [summary]);

  const topModels = useMemo(() => {
    const models = summary?.top_models ?? [];
    return models.map((m) => ({
      model: m.model || 'Unknown',
      cost: amountUnitsToCurrencyUnits(m.upstream_cost),
      quota: amountUnitsToCurrencyUnits(m.quota),
      profit: amountUnitsToCurrencyUnits(m.gross_profit),
    }));
  }, [summary]);

  const topChannels = useMemo(() => {
    const channels = summary?.top_channels ?? [];
    return channels.map((c) => ({
      name: c.name || 'Unknown',
      cost: amountUnitsToCurrencyUnits(c.upstream_cost),
      quota: amountUnitsToCurrencyUnits(c.quota),
      profit: amountUnitsToCurrencyUnits(c.gross_profit),
    }));
  }, [summary]);

  const topSubscriptionAccounts = useMemo(() => {
    const accounts = summary?.top_subscription_accounts ?? [];
    return accounts.map((a) => ({
      name: a.name || 'Unknown',
      platform: a.platform || '',
      status: a.status ?? 0,
      cost: amountUnitsToCurrencyUnits(a.upstream_cost),
      quota: amountUnitsToCurrencyUnits(a.quota),
      profit: amountUnitsToCurrencyUnits(a.gross_profit),
      count: a.count ?? 0,
      accountEventChargedUsd: a.account_event_charged_usd ?? 0,
      accountEventRateMultiplier: a.account_event_average_rate_multiplier ?? 0,
      accountEventCount: a.account_event_count ?? 0,
    }));
  }, [summary]);

  const topSubscriptionAccountQuotaEvents = useMemo(() => {
    const events = summary?.top_subscription_account_quota_events ?? [];
    return events.map((item) => ({
      name: item.name || 'Unknown',
      platform: item.platform || '',
      chargedUsd: item.charged_usd ?? 0,
      rawUsd: item.cost_usd ?? 0,
      averageRateMultiplier: item.average_rate_multiplier ?? 0,
      count: item.count ?? 0,
      ledgerCost: amountUnitsToCurrencyUnits(item.ledger_upstream_cost),
      ledgerQuota: amountUnitsToCurrencyUnits(item.ledger_quota),
      ledgerCount: item.ledger_count ?? 0,
    }));
  }, [summary]);

  // Prepare data for charts
  const channelCostData = useMemo(() => {
    return topChannels.map((c) => ({
      name: c.name,
      cost: c.cost,
      revenue: c.quota,
      profit: c.profit,
    }));
  }, [topChannels]);

  const costBreakdownData = useMemo(() => {
    // Use actual cost values instead of percentages to avoid confusion
    // The pie chart will automatically calculate percentages from the values
    return topModels.map((m, index) => ({
      name: m.model,
      value: m.cost,
      color: CHART_COLORS[index % CHART_COLORS.length],
    }));
  }, [topModels]);

  const handleExport = () => {
    toast.success('成本报表导出功能开发中，敬请期待...');
  };

  const hasData = costMetrics.revenue > 0 || costMetrics.upstreamCost > 0 || costMetrics.grossProfit > 0 || topSubscriptionAccountQuotaEvents.length > 0;

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-2xl font-semibold">成本分析</h2>
          <p className="mt-2 text-sm text-muted-foreground">
            全面的成本、收入和利润分析
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button type="button" variant="outline" size="sm" onClick={handleExport}>
            <Download className="mr-2 size-4" />
            导出报表
          </Button>
        </div>
      </div>

      {/* Cost Metrics */}
      {isLoading ? (
        <MetricCardsSkeleton />
      ) : !hasData ? (
        <EmptyState
          title="暂无成本数据"
          description="成本数据将在有 API 调用和消费记录后显示"
        />
      ) : (
        <section className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <MetricCard
            title="总收入"
            value={formatMoney(costMetrics.revenue)}
            subtitle={`累计收入`}
            tone="green"
            trend="up"
            icon={ArrowUpCircle}
          />
          <MetricCard
            title="上游成本"
            value={formatMoney(costMetrics.upstreamCost)}
            subtitle={`渠道支出`}
            tone="red"
            trend="down"
            icon={ArrowDownCircle}
          />
          <MetricCard
            title="毛利润"
            value={formatMoney(costMetrics.grossProfit)}
            subtitle={`营收 - 成本`}
            tone="blue"
            trend={costMetrics.grossProfit > 0 ? 'up' : 'down'}
            icon={DollarSign}
          />
          <MetricCard
            title="毛利率"
            value={`${costMetrics.margin.toFixed(1)}%`}
            subtitle={`利润 / 收入`}
            tone="purple"
            trend={costMetrics.margin > 30 ? 'up' : 'neutral'}
            icon={TrendingUp}
          />
        </section>
      )}

      {/* Charts Grid */}
      {hasData && (
        <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
          {/* Channel Cost Comparison */}
          {channelCostData.length > 0 && (
            <Card>
              <CardHeader>
                <CardTitle className="text-xl font-bold">渠道成本对比</CardTitle>
              </CardHeader>
              <CardContent>
                <ChannelCostComparison data={channelCostData} />
              </CardContent>
            </Card>
          )}

          {/* Cost Breakdown by Model */}
          {costBreakdownData.length > 0 && (
            <Card>
              <CardHeader>
                <CardTitle className="text-xl font-bold">TOP 模型成本分布</CardTitle>
                <p className="text-sm text-muted-foreground">
                  显示成本最高的模型分布
                </p>
              </CardHeader>
              <CardContent>
                <CostBreakdownChart data={costBreakdownData} />
              </CardContent>
            </Card>
          )}
        </div>
      )}

      {/* Top Models by Cost */}
      {hasData && topModels.length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle className="text-xl font-bold">高成本模型 TOP 5</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="space-y-3">
              {topModels.slice(0, 5).map((item, index) => (
                <div key={index} className="flex items-center justify-between rounded-lg border p-3">
                  <div>
                    <div className="font-medium text-foreground">{item.model}</div>
                    <div className="text-xs text-muted-foreground">
                      成本: ${item.cost.toFixed(2)} | 收入: ${item.quota.toFixed(2)}
                    </div>
                  </div>
                  <div className={cn(
                    'text-sm font-medium',
                    item.profit > 0 ? 'text-green-600' : 'text-red-600'
                  )}>
                    {item.profit > 0 ? '+' : ''}${item.profit.toFixed(2)}
                  </div>
                </div>
              ))}
            </div>
          </CardContent>
        </Card>
      )}

      {/* Top Channels by Cost */}
      {hasData && topChannels.length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle className="text-xl font-bold">高成本渠道 TOP 5</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="space-y-3">
              {topChannels.slice(0, 5).map((item, index) => (
                <div key={index} className="flex items-center justify-between rounded-lg border p-3">
                  <div>
                    <div className="font-medium text-foreground">{item.name}</div>
                    <div className="text-xs text-muted-foreground">
                      成本: ${item.cost.toFixed(2)} | 收入: ${item.quota.toFixed(2)}
                    </div>
                  </div>
                  <div className={cn(
                    'text-sm font-medium',
                    item.profit > 0 ? 'text-green-600' : 'text-red-600'
                  )}>
                    {item.profit > 0 ? '+' : ''}${item.profit.toFixed(2)}
                  </div>
                </div>
              ))}
            </div>
          </CardContent>
        </Card>
      )}

      {/* Top Subscription Accounts by Cost */}
      {hasData && topSubscriptionAccounts.length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle className="text-xl font-bold">订阅账号成本 TOP 5</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="space-y-3">
              {topSubscriptionAccounts.slice(0, 5).map((item, index) => (
                <div key={index} className="flex items-center justify-between rounded-lg border p-3">
                  <div>
                    <div className="font-medium text-foreground">
                      {item.name}
                      {item.platform && (
                        <span className="ml-2 text-xs text-muted-foreground">{item.platform}</span>
                      )}
                    </div>
                    <div className="text-xs text-muted-foreground">
                      成本: ${item.cost.toFixed(2)} | 收入: ${item.quota.toFixed(2)} | 调用: {item.count}
                    </div>
                    {item.accountEventCount > 0 && (
                      <div className="mt-1 text-xs text-muted-foreground">
                        账号本地扣减: {formatUSDValue(item.accountEventChargedUsd, 4)} | 平均倍率: ×{item.accountEventRateMultiplier.toFixed(2)}
                      </div>
                    )}
                  </div>
                  <div className={cn(
                    'text-sm font-medium',
                    item.profit > 0 ? 'text-green-600' : 'text-red-600'
                  )}>
                    {item.profit > 0 ? '+' : ''}${item.profit.toFixed(2)}
                  </div>
                </div>
              ))}
            </div>
          </CardContent>
        </Card>
      )}

      {/* Top Subscription Account Quota Events */}
      {hasData && topSubscriptionAccountQuotaEvents.length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle className="text-xl font-bold">账号本地额度事件 TOP 5</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="space-y-3">
              {topSubscriptionAccountQuotaEvents.slice(0, 5).map((item, index) => (
                <div key={index} className="flex items-center justify-between rounded-lg border p-3">
                  <div>
                    <div className="font-medium text-foreground">
                      {item.name}
                      {item.platform && (
                        <span className="ml-2 text-xs text-muted-foreground">{item.platform}</span>
                      )}
                    </div>
                    <div className="text-xs text-muted-foreground">
                      事件成本: {formatUSDValue(item.chargedUsd, 4)} | 原始成本: {formatUSDValue(item.rawUsd, 4)} | 事件: {item.count}
                    </div>
                    <div className="mt-1 text-xs text-muted-foreground">
                      Ledger 成本: ${item.ledgerCost.toFixed(2)} | Ledger 收入: ${item.ledgerQuota.toFixed(2)} | Ledger 调用: {item.ledgerCount}
                    </div>
                  </div>
                  <div className="text-sm font-medium text-muted-foreground dark:text-foreground">
                    ×{item.averageRateMultiplier.toFixed(2)}
                  </div>
                </div>
              ))}
            </div>
          </CardContent>
        </Card>
      )}

      {/* Cost Insights */}
      <Card className="bg-muted/50">
        <CardContent className="p-4">
          <div className="flex items-start gap-3 text-sm">
            <InfoIcon className="mt-0.5 size-4 shrink-0 text-blue-600" />
            <div className="space-y-1 text-muted-foreground">
              <p>
                <strong>成本分析说明：</strong>数据基于实际使用量和配置的渠道价格计算
              </p>
              <ul className="ml-4 list-disc space-y-1">
                <li><strong>收入：</strong>用户消耗的金额</li>
                <li><strong>上游成本：</strong>调用外部 API 的实际支出</li>
                <li><strong>毛利润：</strong>收入减去上游成本（未包含运营成本）</li>
                <li><strong>毛利率：</strong>毛利润占收入的比例</li>
              </ul>
            </div>
          </div>
        </CardContent>
      </Card>
    </div>
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
