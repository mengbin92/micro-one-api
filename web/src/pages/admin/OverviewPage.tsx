import { useQuery } from '@tanstack/react-query';
import { Activity, Boxes, CreditCard, Database, Gauge, LineChart, ScrollText, Users } from 'lucide-react';
import { Link } from 'react-router-dom';
import { EmptyState } from '@/components/EmptyState';
import { TableSkeleton } from '@/components/LoadingStates';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { adminApiClient } from '@/lib/api';
import { unwrapApiData } from '@/lib/api-response';
import { quotaPerUnitFromOptions, quotaToCurrencyUnits } from '@/lib/quota';

interface AdminTotals {
  users?: number;
  active_users?: number;
  channels?: number;
  active_channels?: number;
  configured_models?: number;
  request_count?: number;
  quota_used?: number;
  channel_balance?: number;
  stale_balance_channels?: number;
  log_count?: number;
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

interface AdminSummary {
  totals?: AdminTotals;
  recent_users?: AdminUser[];
  channels?: AdminChannel[];
  recent_logs?: AdminLog[];
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

function formatMoneyCents(value?: number | string) {
  return `$${(numberValue(value) / 100).toFixed(2)}`;
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
  const logs = data?.recent_logs ?? [];
  const users = data?.recent_users ?? [];
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
          title="调用请求"
          value={formatInteger(totals.request_count)}
          detail={`${formatQuota(totals.quota_used, quotaPerUnit)} 配额消耗`}
          icon={Activity}
        />
        <StatCard
          title="账务记录"
          value={formatMoneyCents(paymentAmountCents)}
          detail={`${formatInteger(data?.payment_summary?.recent_order_count)} 条近期充值/兑换/退款`}
          icon={CreditCard}
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
              <div className="text-sm font-semibold text-slate-500">配额消耗</div>
              <div className="text-2xl font-black">{formatQuota(totals.quota_used, quotaPerUnit)}</div>
              <div className="text-xs font-medium text-slate-400">{Object.keys(completionRatio).length} 个兼容倍率项</div>
            </div>
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

      <Card className="rounded-lg border-0 bg-white shadow-sm ring-1 ring-slate-200 dark:bg-card dark:ring-white/10">
        <CardHeader className="border-b border-slate-100 dark:border-white/10">
          <CardTitle role="heading" aria-level={3} className="flex items-center gap-2">
            <ScrollText className="size-5" />
            最近调用与订单动态
          </CardTitle>
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
    </div>
  );
}
