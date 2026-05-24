import { useQuery } from '@tanstack/react-query';
import { apiClient } from '@/lib/api';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { EmptyState } from '@/components/EmptyState';
import { ChartSkeleton, MetricCardsSkeleton } from '@/components/LoadingStates';
import {
  AreaChart,
  Area,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
} from 'recharts';

interface UsageItem {
  date?: string;
  day?: string;
  count: number;
  quota: number;
  prompt_tokens?: number;
  completion_tokens?: number;
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
}

function formatQuota(q: number) {
  return (q / 500000).toFixed(2);
}

function numberOrZero(value: unknown) {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0;
}

export function DashboardPage() {
  const { isLoading: isUserLoading } = useQuery({
    queryKey: ['user-self'],
    queryFn: async () => {
      const res = await apiClient.get('/user/self');
      return res.data.data as UserSelf;
    },
  });

  const { data: dashboard, isLoading } = useQuery({
    queryKey: ['dashboard-summary'],
    queryFn: async () => {
      const res = await apiClient.get('/user/dashboard');
      return res.data.data as AccountDashboard;
    },
  });

  const items = Array.isArray(dashboard?.usage) ? dashboard.usage : [];
  const totalCount = items.reduce((s, x) => s + (x.count || 0), 0);
  const totalQuota = items.reduce((s, x) => s + (x.quota || 0), 0);
  const totalTokens = items.reduce(
    (s, x) => s + (x.prompt_tokens || 0) + (x.completion_tokens || 0),
    0
  );
  const remainingQuota = numberOrZero(dashboard?.quota);
  const usedQuota = numberOrZero(dashboard?.used_quota);
  const requestCount = items.length > 0 ? totalCount : numberOrZero(dashboard?.request_count);
  const quotaTotal = items.length > 0 ? totalQuota : usedQuota;

  return (
    <div className="space-y-6">
      <h2 className="text-2xl font-semibold">Dashboard</h2>

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {isUserLoading || isLoading ? (
          <MetricCardsSkeleton />
        ) : (
          <>
            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-sm font-medium text-muted-foreground">
                  Remaining Quota
                </CardTitle>
              </CardHeader>
              <CardContent>
                <p className="text-2xl font-bold">{dashboard ? formatQuota(remainingQuota) : '—'}</p>
              </CardContent>
            </Card>
            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-sm font-medium text-muted-foreground">Used Quota</CardTitle>
              </CardHeader>
              <CardContent>
                <p className="text-2xl font-bold">{dashboard ? formatQuota(usedQuota) : '—'}</p>
              </CardContent>
            </Card>
            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-sm font-medium text-muted-foreground">
                  Requests (range)
                </CardTitle>
              </CardHeader>
              <CardContent>
                <p className="text-2xl font-bold">{requestCount.toLocaleString()}</p>
              </CardContent>
            </Card>
            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-sm font-medium text-muted-foreground">
                  Tokens (range)
                </CardTitle>
              </CardHeader>
              <CardContent>
                <p className="text-2xl font-bold">
                  {totalTokens > 0 ? `${(totalTokens / 1000).toFixed(1)}K` : '—'}
                </p>
                <p className="mt-1 text-xs text-muted-foreground">
                  Quota: {formatQuota(quotaTotal)}
                </p>
              </CardContent>
            </Card>
          </>
        )}
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Daily Usage</CardTitle>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <ChartSkeleton />
          ) : items.length === 0 ? (
            <EmptyState title="No usage data" description="Usage will appear here after requests are processed." />
          ) : (
            <ResponsiveContainer width="100%" height={240}>
              <AreaChart data={items}>
                <CartesianGrid strokeDasharray="3 3" className="stroke-border" />
                <XAxis
                  dataKey={items[0]?.date ? 'date' : 'day'}
                  tick={{ fontSize: 12 }}
                />
                <YAxis tick={{ fontSize: 12 }} />
                <Tooltip />
                <Area
                  type="monotone"
                  dataKey="count"
                  stroke="#8b5cf6"
                  fill="#8b5cf6"
                  fillOpacity={0.2}
                  name="Requests"
                />
              </AreaChart>
            </ResponsiveContainer>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
