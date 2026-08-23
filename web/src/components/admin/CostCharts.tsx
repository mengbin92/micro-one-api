/**
 * Cost visualization components for cost analytics
 * These charts provide comprehensive visualizations for cost, revenue, and profit analysis
 */

import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  Line,
  LineChart,
  Pie,
  PieChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import type { PieLabelRenderProps } from 'recharts';
import { cn } from '@/lib/utils';

interface CostTrendData {
  date: string;
  revenue: number;
  cost: number;
  profit: number;
}

interface ChannelCostData {
  name: string;
  cost: number;
  revenue: number;
  profit: number;
}

interface ProfitMarginData {
  date: string;
  margin: number;
}

interface CostBreakdownData {
  name: string;
  value: number;
  color: string;
}

const COLORS = {
  revenue: '#10b981',
  cost: '#ef4444',
  profit: '#3b82f6',
  grid: '#e5e7eb',
  text: '#64748b',
};

const PIE_START_ANGLE = 90;
const PIE_END_ANGLE = -270;
const PIE_PADDING_ANGLE = 2;
const PIE_LABEL_OFFSET = 20;
const PIE_LABEL_GAP = 22;

interface PiePoint {
  x: number;
  y: number;
}

interface PieLabelLineProps {
  cx?: number;
  cy?: number;
  outerRadius?: number;
  index?: number;
  points?: PiePoint[];
  stroke?: string;
}

interface PieLabelLayout {
  x: number;
  y: number;
  textAnchor: 'start' | 'end';
  side: 'left' | 'right';
}

function polarToCartesian(cx: number, cy: number, radius: number, angle: number): PiePoint {
  const radians = -angle * (Math.PI / 180);
  return {
    x: cx + Math.cos(radians) * radius,
    y: cy + Math.sin(radians) * radius,
  };
}

// eslint-disable-next-line react-refresh/only-export-components -- pure layout helper is unit-tested separately
export function getPieLabelLayouts(
  data: CostBreakdownData[],
  props: Pick<PieLabelRenderProps, 'cx' | 'cy' | 'outerRadius'>,
): PieLabelLayout[] {
  const total = data.reduce((sum, item) => sum + Math.max(item.value, 0), 0);
  if (total <= 0 || typeof props.cx !== 'number' || typeof props.cy !== 'number' || typeof props.outerRadius !== 'number') {
    return data.map(() => ({ x: 0, y: 0, textAnchor: 'start', side: 'right' }));
  }

  const nonZeroCount = data.filter((item) => item.value > 0).length;
  const totalPaddingAngle = nonZeroCount > 0 ? nonZeroCount * PIE_PADDING_ANGLE : 0;
  const realTotalAngle = 360 - totalPaddingAngle;
  const direction = PIE_END_ANGLE >= PIE_START_ANGLE ? 1 : -1;
  let previousEndAngle = PIE_START_ANGLE;

  const points = data.map((item, index) => {
    const value = Math.max(item.value, 0);
    const startAngle = index === 0
      ? PIE_START_ANGLE
      : previousEndAngle + direction * PIE_PADDING_ANGLE;
    const endAngle = startAngle + direction * (value > 0 ? (value / total) * realTotalAngle : 0);
    previousEndAngle = endAngle;

    const midAngle = (startAngle + endAngle) / 2;
    const point = polarToCartesian(props.cx, props.cy, props.outerRadius + PIE_LABEL_OFFSET, midAngle);
    return {
      ...point,
      baseY: point.y,
      side: point.x >= props.cx ? 'right' as const : 'left' as const,
    };
  });

  const layouts = points.map((point) => ({
    x: props.cx + (point.side === 'right' ? 1 : -1) * (props.outerRadius + 52),
    y: point.baseY,
    textAnchor: point.side === 'right' ? 'start' as const : 'end' as const,
    side: point.side,
  }));

  (['left', 'right'] as const).forEach((side) => {
    const indexes = layouts
      .map((layout, index) => ({ index, y: layout.y }))
      .filter((item) => layouts[item.index].side === side)
      .sort((a, b) => a.y - b.y);

    if (indexes.length === 0) return;

    const minY = props.cy - props.outerRadius - PIE_LABEL_OFFSET;
    const maxY = props.cy + props.outerRadius + PIE_LABEL_OFFSET;
    let nextY = minY;
    indexes.forEach(({ index }) => {
      layouts[index].y = Math.max(layouts[index].y, nextY);
      nextY = layouts[index].y + PIE_LABEL_GAP;
    });

    const overflow = layouts[indexes[indexes.length - 1].index].y - maxY;
    if (overflow > 0) {
      indexes.forEach(({ index }) => {
        layouts[index].y -= overflow;
      });
    }

    const underflow = minY - layouts[indexes[0].index].y;
    if (underflow > 0) {
      indexes.forEach(({ index }) => {
        layouts[index].y += underflow;
      });
    }
  });

  return layouts;
}

/**
 * Cost trend over time showing revenue, cost, and profit
 */
export function CostTrendChart({ data }: { data: CostTrendData[] }) {
  if (data.length === 0) {
    return (
      <div className="flex h-64 items-center justify-center text-muted-foreground">
        暂无趋势数据
      </div>
    );
  }

  return (
    <ResponsiveContainer width="100%" height={280}>
      <AreaChart data={data} margin={{ left: 0, right: 8, top: 20, bottom: 0 }}>
        <defs>
          <linearGradient id="revenueGradient" x1="0" x2="0" y1="0" y2="1">
            <stop offset="0%" stopColor={COLORS.revenue} stopOpacity={0.3} />
            <stop offset="100%" stopColor={COLORS.revenue} stopOpacity={0.05} />
          </linearGradient>
          <linearGradient id="costGradient" x1="0" x2="0" y1="0" y2="1">
            <stop offset="0%" stopColor={COLORS.cost} stopOpacity={0.3} />
            <stop offset="100%" stopColor={COLORS.cost} stopOpacity={0.05} />
          </linearGradient>
          <linearGradient id="profitGradient" x1="0" x2="0" y1="0" y2="1">
            <stop offset="0%" stopColor={COLORS.profit} stopOpacity={0.3} />
            <stop offset="100%" stopColor={COLORS.profit} stopOpacity={0.05} />
          </linearGradient>
        </defs>
        <CartesianGrid stroke={COLORS.grid} strokeDasharray="4 4" vertical={false} />
        <XAxis
          dataKey="date"
          tick={{ fontSize: 11, fill: COLORS.text }}
          tickLine={false}
          axisLine={false}
          interval="preserveStartEnd"
          tickFormatter={(value) => {
            const date = new Date(value);
            return `${date.getMonth() + 1}/${date.getDate()}`;
          }}
        />
        <YAxis
          tick={{ fontSize: 11, fill: COLORS.text }}
          tickLine={false}
          axisLine={false}
          width={48}
          tickFormatter={(value) => `$${value.toFixed(0)}`}
        />
        <Tooltip
          formatter={(value, name) => {
            const numValue = typeof value === 'number' ? value : 0;
            const nameStr = typeof name === 'string' ? name : '';
            return [
              `$${numValue.toFixed(2)}`,
              nameStr === 'revenue' ? '收入' : nameStr === 'cost' ? '成本' : '利润'
            ];
          }}
          contentStyle={{ fontSize: '12px' }}
        />
        <Area
          type="monotone"
          dataKey="revenue"
          name="revenue"
          stroke={COLORS.revenue}
          strokeWidth={2}
          fill="url(#revenueGradient)"
        />
        <Area
          type="monotone"
          dataKey="cost"
          name="cost"
          stroke={COLORS.cost}
          strokeWidth={2}
          fill="url(#costGradient)"
        />
        <Area
          type="monotone"
          dataKey="profit"
          name="profit"
          stroke={COLORS.profit}
          strokeWidth={2}
          fill="url(#profitGradient)"
        />
      </AreaChart>
    </ResponsiveContainer>
  );
}

/**
 * Horizontal bar chart comparing costs across channels
 */
export function ChannelCostComparison({ data }: { data: ChannelCostData[] }) {
  if (data.length === 0) {
    return (
      <div className="flex h-64 items-center justify-center text-muted-foreground">
        暂无渠道成本数据
      </div>
    );
  }

  const maxCost = Math.max(...data.map((d) => d.cost));
  const maxRevenue = Math.max(...data.map((d) => d.revenue));
  const maxValue = Math.max(maxCost, maxRevenue);

  return (
    <div className="space-y-3">
      {data.map((item) => {
        const costPercentage = maxValue > 0 ? (item.cost / maxValue) * 100 : 0;
        const profitMargin = item.revenue > 0 ? (item.profit / item.revenue) * 100 : 0;

        return (
          <div key={item.name} className="space-y-2">
            <div className="flex items-center justify-between text-sm">
              <span className="font-medium text-foreground">{item.name}</span>
              <span className="text-muted-foreground">${item.cost.toFixed(2)}</span>
            </div>
            <div className="relative h-8 w-full overflow-hidden rounded-lg bg-muted">
              {/* Cost bar */}
              <div
                className="absolute left-0 top-0 h-full rounded-l-lg bg-red-500 transition-all"
                style={{ width: `${costPercentage}%` }}
              >
                <span className="ml-2 flex h-full items-center text-xs font-medium text-white">
                  成本 ${item.cost.toFixed(0)}
                </span>
              </div>
              {/* Revenue overlay */}
              <div
                className="absolute left-0 top-0 h-full bg-green-500/30 transition-all"
                style={{ width: `${(item.revenue / maxValue) * 100}%` }}
              />
              {/* Profit indicator */}
              {item.profit > 0 && (
                <div className="absolute right-2 top-1/2 -translate-y-1/2 text-xs font-bold text-green-700">
                  +${item.profit.toFixed(0)}
                </div>
              )}
            </div>
            <div className="flex justify-between text-xs text-muted-foreground">
              <span>收入: ${item.revenue.toFixed(2)}</span>
              <span className={cn(
                'font-medium',
                profitMargin >= 30 ? 'text-green-600' : profitMargin >= 10 ? 'text-amber-600' : 'text-red-600'
              )}>
                利润率: {profitMargin.toFixed(1)}%
              </span>
            </div>
          </div>
        );
      })}
    </div>
  );
}

/**
 * Line chart showing profit margin trend
 */
export function ProfitMarginChart({ data }: { data: ProfitMarginData[] }) {
  if (data.length === 0) {
    return (
      <div className="flex h-64 items-center justify-center text-muted-foreground">
        暂无利润率数据
      </div>
    );
  }

  // Calculate average margin
  const avgMargin = data.reduce((sum, d) => sum + d.margin, 0) / data.length;

  return (
    <ResponsiveContainer width="100%" height={280}>
      <LineChart data={data} margin={{ left: 0, right: 8, top: 20, bottom: 0 }}>
        <CartesianGrid stroke={COLORS.grid} strokeDasharray="4 4" vertical={false} />
        <XAxis
          dataKey="date"
          tick={{ fontSize: 11, fill: COLORS.text }}
          tickLine={false}
          axisLine={false}
          interval="preserveStartEnd"
          tickFormatter={(value) => {
            const date = new Date(value);
            return `${date.getMonth() + 1}/${date.getDate()}`;
          }}
        />
        <YAxis
          tick={{ fontSize: 11, fill: COLORS.text }}
          tickLine={false}
          axisLine={false}
          width={48}
          tickFormatter={(value) => `${value.toFixed(0)}%`}
          domain={[0, 100]}
        />
        <Tooltip
          formatter={(value) => {
            const numValue = typeof value === 'number' ? value : 0;
            return [`${numValue.toFixed(1)}%`, '利润率'];
          }}
          contentStyle={{ fontSize: '12px' }}
        />
        {/* Average line */}
        <Line
          type="monotone"
          dataKey={() => avgMargin}
          stroke={COLORS.text}
          strokeWidth={1}
          strokeDasharray="4 4"
          dot={false}
          activeDot={false}
        />
        {/* Margin line */}
        <Line
          type="monotone"
          dataKey="margin"
          stroke={COLORS.profit}
          strokeWidth={2}
          dot={false}
          activeDot={{ r: 4 }}
        />
      </LineChart>
    </ResponsiveContainer>
  );
}

/**
 * Pie chart showing cost breakdown by model or provider
 */
export function CostBreakdownChart({ data }: { data: CostBreakdownData[] }) {
  if (data.length === 0) {
    return (
      <div className="flex h-64 items-center justify-center text-muted-foreground">
        暂无分布数据
      </div>
    );
  }

  const total = data.reduce((sum, d) => sum + d.value, 0);
  const renderLabel = (props: PieLabelRenderProps) => {
    const layout = getPieLabelLayouts(data, props)[props.index];
    const value = props.payload?.value ?? props.value ?? 0;

    return (
      <text
        x={layout.x}
        y={layout.y}
        textAnchor={layout.textAnchor}
        dominantBaseline="middle"
        fill={props.fill}
        fontSize={11}
      >
        {props.name || ''} ${value.toFixed(1)}
      </text>
    );
  };
  const renderLabelLine = (props: PieLabelLineProps) => {
    if (!props.points?.[0] || typeof props.cx !== 'number' || typeof props.cy !== 'number' || typeof props.outerRadius !== 'number' || typeof props.index !== 'number') {
      return <path d="" />;
    }

    const layout = getPieLabelLayouts(data, {
      cx: props.cx,
      cy: props.cy,
      outerRadius: props.outerRadius,
    })[props.index];
    const elbowX = props.cx + (layout.side === 'right' ? 1 : -1) * (props.outerRadius + 30);
    const start = props.points[0];

    return (
      <path
        d={`M ${start.x} ${start.y} L ${elbowX} ${layout.y} L ${layout.x} ${layout.y}`}
        fill="none"
        stroke={props.stroke || '#94a3b8'}
        strokeWidth={1}
        strokeOpacity={0.8}
      />
    );
  };

  return (
    <div className="relative flex items-center justify-center">
      <ResponsiveContainer width="100%" height={220}>
        <PieChart>
          <Pie
            data={data}
            dataKey="value"
            innerRadius="60%"
            outerRadius="80%"
            startAngle={PIE_START_ANGLE}
            endAngle={PIE_END_ANGLE}
            paddingAngle={PIE_PADDING_ANGLE}
            label={renderLabel}
            labelLine={renderLabelLine}
          >
            {data.map((entry, index) => (
              <Cell key={`cell-${index}`} fill={entry.color} />
            ))}
          </Pie>
          <Tooltip
            formatter={(value) => {
              const numValue = typeof value === 'number' ? value : 0;
              return `$${numValue.toFixed(2)}`;
            }}
            contentStyle={{ fontSize: '12px' }}
          />
        </PieChart>
      </ResponsiveContainer>
      <div className="absolute text-center">
        <div className="text-2xl font-black text-foreground">${total.toFixed(2)}</div>
        <div className="text-xs font-semibold text-muted-foreground">总计成本</div>
      </div>
    </div>
  );
}

/**
 * Combo chart comparing budget vs actual costs
 */
export function BudgetVsActualChart({ data }: {
  data: Array<{ period: string; budget: number; actual: number }>;
}) {
  if (data.length === 0) {
    return (
      <div className="flex h-64 items-center justify-center text-muted-foreground">
        暂无预算对比数据
      </div>
    );
  }

  return (
    <ResponsiveContainer width="100%" height={280}>
      <BarChart data={data} margin={{ left: 0, right: 8, top: 20, bottom: 0 }}>
        <CartesianGrid stroke={COLORS.grid} strokeDasharray="4 4" vertical={false} />
        <XAxis
          dataKey="period"
          tick={{ fontSize: 11, fill: COLORS.text }}
          tickLine={false}
          axisLine={false}
        />
        <YAxis
          tick={{ fontSize: 11, fill: COLORS.text }}
          tickLine={false}
          axisLine={false}
          width={48}
          tickFormatter={(value) => `$${value.toFixed(0)}`}
        />
        <Tooltip
          formatter={(value, name) => {
            const numValue = typeof value === 'number' ? value : 0;
            const nameStr = typeof name === 'string' ? name : '';
            return [
              `$${numValue.toFixed(2)}`,
              nameStr === 'budget' ? '预算' : '实际'
            ];
          }}
          contentStyle={{ fontSize: '12px' }}
        />
        <Bar dataKey="budget" name="budget" fill={COLORS.text} radius={[4, 4, 0, 0]} opacity={0.5} />
        <Bar dataKey="actual" name="actual" fill={COLORS.profit} radius={[4, 4, 0, 0]} />
      </BarChart>
    </ResponsiveContainer>
  );
}
