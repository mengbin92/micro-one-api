/**
 * Health visualization components for channel health monitoring
 * These charts provide advanced visualizations for health trends and metrics
 */

import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  Pie,
  PieChart,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import { cn } from '@/lib/utils';

interface HealthTrendData {
  timestamp: string;
  healthy: number;
  degraded: number;
  unavailable: number;
  total: number;
  healthRate: number;
}

interface ResponseTimeData {
  channel: string;
  avgResponseTime: number;
  p50: number;
  p95: number;
  p99: number;
}

interface UptimeData {
  name: string;
  value: number;
  color: string;
}

interface FailureRateData {
  timestamp: string;
  rate: number;
  threshold?: number;
}

const COLORS = {
  healthy: '#10b981',
  degraded: '#f59e0b',
  unavailable: '#ef4444',
  unknown: '#64748b',
  grid: '#e5e7eb',
  text: '#64748b',
};

/**
 * Health status trend over time
 */
export function HealthTrendChart({ data }: { data: HealthTrendData[] }) {
  if (data.length === 0) {
    return (
      <div className="flex h-60 items-center justify-center text-muted-foreground">
        暂无趋势数据
      </div>
    );
  }

  return (
    <ResponsiveContainer width="100%" height={200}>
      <AreaChart data={data} margin={{ left: 0, right: 8, top: 20, bottom: 0 }}>
        <defs>
          <linearGradient id="healthyGradient" x1="0" x2="0" y1="0" y2="1">
            <stop offset="0%" stopColor={COLORS.healthy} stopOpacity={0.3} />
            <stop offset="100%" stopColor={COLORS.healthy} stopOpacity={0.05} />
          </linearGradient>
          <linearGradient id="unavailableGradient" x1="0" x2="0" y1="0" y2="1">
            <stop offset="0%" stopColor={COLORS.unavailable} stopOpacity={0.3} />
            <stop offset="100%" stopColor={COLORS.unavailable} stopOpacity={0.05} />
          </linearGradient>
        </defs>
        <CartesianGrid stroke={COLORS.grid} strokeDasharray="4 4" vertical={false} />
        <XAxis
          dataKey="timestamp"
          tick={{ fontSize: 11, fill: COLORS.text }}
          tickLine={false}
          axisLine={false}
          interval="preserveStartEnd"
        />
        <YAxis tick={{ fontSize: 11, fill: COLORS.text }} tickLine={false} axisLine={false} width={32} />
        <Tooltip
          formatter={(value, name) => {
            const numValue = typeof value === 'number' ? value : 0;
            const nameStr = typeof name === 'string' ? name : '';
            return [numValue, nameStr === 'healthy' ? '健康' : nameStr === 'unavailable' ? '不可用' : nameStr];
          }}
          contentStyle={{ fontSize: '12px' }}
        />
        <Area
          type="monotone"
          dataKey="healthy"
          name="healthy"
          stroke={COLORS.healthy}
          strokeWidth={2}
          fill="url(#healthyGradient)"
        />
        <Area
          type="monotone"
          dataKey="unavailable"
          name="unavailable"
          stroke={COLORS.unavailable}
          strokeWidth={2}
          fill="url(#unavailableGradient)"
        />
      </AreaChart>
    </ResponsiveContainer>
  );
}

/**
 * Response time distribution by channel
 */
export function ResponseTimeChart({ data }: { data: ResponseTimeData[] }) {
  if (data.length === 0) {
    return (
      <div className="flex h-60 items-center justify-center text-muted-foreground">
        暂无响应时间数据
      </div>
    );
  }

  const maxTime = Math.max(...data.map((d) => d.avgResponseTime));

  return (
    <div className="space-y-3">
      {data.map((item) => {
        const percentage = maxTime > 0 ? (item.avgResponseTime / maxTime) * 100 : 0;
        return (
          <div key={item.channel} className="space-y-1">
            <div className="flex items-center justify-between text-xs">
              <span className="font-medium text-foreground">{item.channel}</span>
              <span className="text-muted-foreground">{item.avgResponseTime.toFixed(0)}ms</span>
            </div>
            <div className="h-2 w-full overflow-hidden rounded-full bg-muted">
              <div
                className={cn(
                  'h-full rounded-full transition-all',
                  item.avgResponseTime < 200 ? 'bg-green-500' :
                  item.avgResponseTime < 500 ? 'bg-amber-500' : 'bg-red-500'
                )}
                style={{ width: `${percentage}%` }}
              />
            </div>
          </div>
        );
      })}
    </div>
  );
}

/**
 * Channel uptime percentage (pie chart)
 */
export function UptimePieChart({ data }: { data: UptimeData[] }) {
  if (data.length === 0) {
    return (
      <div className="flex h-60 items-center justify-center text-muted-foreground">
        暂无运行时间数据
      </div>
    );
  }

  return (
    <ResponsiveContainer width="100%" height={200}>
      <PieChart>
        <Pie
          data={data}
          dataKey="value"
          innerRadius="60%"
          outerRadius="80%"
          paddingAngle={2}
          label={(props: any) => {
            const name = props.name || '';
            const percent = props.percent ?? 0;
            return `${name} ${(percent * 100).toFixed(0)}%`;
          }}
          labelLine={false}
        >
          {data.map((entry, index) => (
            <Cell key={`cell-${index}`} fill={entry.color} />
          ))}
        </Pie>
        <Tooltip
          formatter={(value) => {
            const numValue = typeof value === 'number' ? value : 0;
            return `${numValue.toFixed(1)}%`;
          }}
        />
      </PieChart>
    </ResponsiveContainer>
  );
}

/**
 * Failure rate trends over time
 */
export function FailureRateChart({ data }: { data: FailureRateData[] }) {
  if (data.length === 0) {
    return (
      <div className="flex h-60 items-center justify-center text-muted-foreground">
        暂无失败率数据
      </div>
    );
  }

  const threshold = data[0]?.threshold ?? 5;

  return (
    <ResponsiveContainer width="100%" height={200}>
      <LineChart data={data} margin={{ left: 0, right: 8, top: 20, bottom: 0 }}>
        <CartesianGrid stroke={COLORS.grid} strokeDasharray="4 4" vertical={false} />
        <XAxis
          dataKey="timestamp"
          tick={{ fontSize: 11, fill: COLORS.text }}
          tickLine={false}
          axisLine={false}
          interval="preserveStartEnd"
        />
        <YAxis
          tick={{ fontSize: 11, fill: COLORS.text }}
          tickLine={false}
          axisLine={false}
          width={40}
          tickFormatter={(value) => `${value}%`}
        />
        <Tooltip
          formatter={(value) => {
            const numValue = typeof value === 'number' ? value : 0;
            return [`${numValue.toFixed(2)}%`, '失败率'];
          }}
          contentStyle={{ fontSize: '12px' }}
        />
        {/* Threshold line */}
        <Line
          type="monotone"
          dataKey={() => threshold}
          stroke={COLORS.unavailable}
          strokeWidth={1}
          strokeDasharray="4 4"
          dot={false}
          activeDot={false}
        />
        {/* Actual failure rate */}
        <Line
          type="monotone"
          dataKey="rate"
          stroke={COLORS.degraded}
          strokeWidth={2}
          dot={false}
        />
      </LineChart>
    </ResponsiveContainer>
  );
}

/**
 * Overall health distribution bar chart
 */
export function HealthDistributionChart({ healthy, degraded, unavailable, unknown = 0 }: {
  healthy: number;
  degraded: number;
  unavailable: number;
  unknown?: number;
}) {
  const total = healthy + degraded + unavailable + unknown;
  if (total === 0) {
    return (
      <div className="flex h-60 items-center justify-center text-muted-foreground">
        暂无数据
      </div>
    );
  }

  const data = [
    { name: '健康', value: healthy, color: COLORS.healthy },
    { name: '降级', value: degraded, color: COLORS.degraded },
    { name: '不可用', value: unavailable, color: COLORS.unavailable },
    ...(unknown > 0 ? [{ name: '未知', value: unknown, color: COLORS.unknown }] : []),
  ];

  return (
    <ResponsiveContainer width="100%" height={200}>
      <BarChart data={data} layout="vertical" margin={{ left: 40, right: 8, top: 20, bottom: 0 }}>
        <CartesianGrid stroke={COLORS.grid} strokeDasharray="4 4" horizontal={false} />
        <XAxis type="number" tick={{ fontSize: 11, fill: COLORS.text }} tickLine={false} axisLine={false} />
        <YAxis
          type="category"
          dataKey="name"
          tick={{ fontSize: 11, fill: COLORS.text }}
          tickLine={false}
          axisLine={false}
          width={40}
        />
        <Tooltip
          formatter={(value) => {
            const numValue = typeof value === 'number' ? value : 0;
            return numValue;
          }}
          contentStyle={{ fontSize: '12px' }}
        />
        <Bar dataKey="value" radius={[0, 4, 4, 0]}>
          {data.map((entry, index) => (
            <Cell key={`cell-${index}`} fill={entry.color} />
          ))}
        </Bar>
      </BarChart>
    </ResponsiveContainer>
  );
}
