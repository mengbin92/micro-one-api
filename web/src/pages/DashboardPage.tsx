import { useQuery } from '@tanstack/react-query';
import { apiClient } from '@/lib/api';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
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
  quota: number;
  used_quota: number;
  role: number;
}

function formatQuota(q: number) {
  return (q / 500000).toFixed(2);
}

export function DashboardPage() {
  const { data: user } = useQuery({
    queryKey: ['user-self'],
    queryFn: async () => {
      const res = await apiClient.get('/user/self');
      return res.data.data as UserSelf;
    },
  });

  const { data: usage, isLoading } = useQuery({
    queryKey: ['dashboard-usage'],
    queryFn: async () => {
      const res = await apiClient.get('/user/dashboard');
      return res.data.data as UsageItem[];
    },
  });

  const items = usage ?? [];
  const totalCount = items.reduce((s, x) => s + (x.count || 0), 0);
  const totalQuota = items.reduce((s, x) => s + (x.quota || 0), 0);
  const totalTokens = items.reduce(
    (s, x) => s + (x.prompt_tokens || 0) + (x.completion_tokens || 0),
    0
  );

  return (
    <div className="space-y-6">
      <h2 className="text-2xl font-semibold">Dashboard</h2>

      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">
              Remaining Quota
            </CardTitle>
          </CardHeader>
          <CardContent>
            <p className="text-2xl font-bold">{user ? formatQuota(user.quota) : '—'}</p>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">Used Quota</CardTitle>
          </CardHeader>
          <CardContent>
            <p className="text-2xl font-bold">{user ? formatQuota(user.used_quota) : '—'}</p>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm font-medium text-muted-foreground">
              Requests (range)
            </CardTitle>
          </CardHeader>
          <CardContent>
            <p className="text-2xl font-bold">{totalCount.toLocaleString()}</p>
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
            <p className="text-xs text-muted-foreground mt-1">
              Quota: {formatQuota(totalQuota)}
            </p>
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Daily Usage</CardTitle>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <div className="h-48 flex items-center justify-center text-muted-foreground">
              Loading...
            </div>
          ) : items.length === 0 ? (
            <div className="h-48 flex items-center justify-center text-muted-foreground">
              No usage data
            </div>
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
