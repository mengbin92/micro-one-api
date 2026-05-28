import { useQuery } from '@tanstack/react-query';
import { useState } from 'react';
import { Button } from '@/components/ui/button';
import { EmptyState } from '@/components/EmptyState';
import { TableSkeleton } from '@/components/LoadingStates';
import { apiClient } from '@/lib/api';
import { unwrapApiData } from '@/lib/api-response';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';

interface UsageLog {
  id?: string;
  type: string;
  amount: number;
  balance_after: number;
  reference_id?: string;
  remark?: string;
  created_at: number;
  token_name?: string;
  model_name?: string;
  endpoint?: string;
  quota?: number;
  prompt_tokens?: number;
  completion_tokens?: number;
  elapsed_time?: number;
  is_stream?: boolean;
}

interface UsageLogData {
  items?: UsageLog[];
  logs?: UsageLog[];
  total?: number;
}

const LOG_TYPE_NAMES: Record<string, string> = {
  redeem: '兑换',
  recharge: '充值',
  consume: '消费',
  refund: '退款',
};

function normalizeLogs(data: UsageLog[] | UsageLogData): UsageLog[] {
  if (Array.isArray(data)) return data;
  if (Array.isArray(data?.items)) return data.items;
  if (Array.isArray(data?.logs)) return data.logs;
  return [];
}

function formatQuota(value: number) {
  return (value / 500000).toFixed(4);
}

function formatDate(value: number) {
  if (!value) return '-';
  return new Date(value * 1000).toLocaleString();
}

function formatDuration(value?: number) {
  if (!value || value <= 0) return '-';
  return value >= 1000 ? `${(value / 1000).toFixed(2)}s` : `${value}ms`;
}

function totalTokens(log: UsageLog) {
  return log.quota || (log.prompt_tokens || 0) + (log.completion_tokens || 0);
}

function hasTokenBreakdown(log: UsageLog) {
  return (log.prompt_tokens || 0) > 0 || (log.completion_tokens || 0) > 0;
}

export function UsagePage() {
  const [page, setPage] = useState(1);
  const [type, setType] = useState('consume');
  const pageSize = 20;

  const { data, isLoading } = useQuery({
    queryKey: ['user-usage-logs', page, type],
    queryFn: async () => {
      const params = new URLSearchParams({
        page: String(page),
        page_size: String(pageSize),
      });
      if (type) params.set('type', type);
      const res = await apiClient.get(`/user/logs?${params}`);
      const payload = unwrapApiData<UsageLog[] | UsageLogData>(res.data);
      return {
        logs: normalizeLogs(payload),
        total: Array.isArray(payload) ? payload.length : payload.total,
      };
    },
  });

  const logs = data?.logs ?? [];

  return (
    <div className="space-y-5">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h2 className="text-2xl font-black tracking-normal text-slate-950 dark:text-white">使用记录</h2>
          <p className="mt-1 text-sm font-medium text-slate-500 dark:text-slate-400">
            展示 API 消费流水，包含模型、端点、Token 和耗时。
          </p>
        </div>
        <div className="flex items-center gap-2">
          <select
            value={type}
            onChange={(event) => {
              setType(event.target.value);
              setPage(1);
            }}
            className="h-10 rounded-lg border border-slate-200 bg-white px-3 text-sm font-semibold dark:border-white/10 dark:bg-background"
          >
            <option value="consume">消费记录</option>
            <option value="">全部流水</option>
          </select>
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              setType('consume');
              setPage(1);
            }}
          >
            重置
          </Button>
        </div>
      </div>

      {isLoading ? (
        <TableSkeleton columns={['API 密钥', '模型', '端点', '类型', 'Token', '费用', '耗时', '时间']} rows={8} />
      ) : logs.length === 0 ? (
        <EmptyState title="暂无使用记录" description="API 请求处理完成后，消费记录会显示在这里。" />
      ) : (
        <>
          <div className="overflow-x-auto rounded-lg bg-white shadow-sm ring-1 ring-slate-200 dark:bg-card dark:ring-white/10">
            <Table className="min-w-[1050px]">
              <TableHeader>
                <TableRow className="bg-slate-50 hover:bg-slate-50 dark:bg-white/5">
                  <TableHead>API 密钥</TableHead>
                  <TableHead>模型</TableHead>
                  <TableHead>端点</TableHead>
                  <TableHead>类型</TableHead>
                  <TableHead>Token</TableHead>
                  <TableHead>费用</TableHead>
                  <TableHead>耗时</TableHead>
                  <TableHead>时间</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {logs.map((log, index) => (
                  <TableRow key={`${log.id || log.reference_id || log.created_at}-${index}`}>
                    <TableCell className="font-medium">{log.token_name || '-'}</TableCell>
                    <TableCell className="font-semibold">{log.model_name || '-'}</TableCell>
                    <TableCell className="font-mono text-xs">{log.endpoint || '-'}</TableCell>
                    <TableCell>
                      <span className="inline-flex rounded-md bg-blue-100 px-2 py-1 text-xs font-bold text-blue-700 dark:bg-blue-500/15 dark:text-blue-300">
                        {LOG_TYPE_NAMES[log.type] || log.type || '-'}
                        {log.is_stream ? ' / 流式' : ''}
                      </span>
                    </TableCell>
                    <TableCell>
                      <div className="text-sm font-semibold">
                        {totalTokens(log).toLocaleString()}
                        {hasTokenBreakdown(log) ? (
                          <div className="text-xs text-slate-400">
                            输入 {(log.prompt_tokens || 0).toLocaleString()} / 输出 {(log.completion_tokens || 0).toLocaleString()}
                          </div>
                        ) : null}
                      </div>
                    </TableCell>
                    <TableCell className="font-semibold text-emerald-600">{formatQuota(Math.abs(log.amount ?? 0))}</TableCell>
                    <TableCell>{formatDuration(log.elapsed_time)}</TableCell>
                    <TableCell>{formatDate(log.created_at)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>

          <div className="flex items-center justify-end gap-2">
            <Button variant="outline" size="sm" onClick={() => setPage((value) => Math.max(1, value - 1))} disabled={page === 1}>
              上一页
            </Button>
            <span className="min-w-14 text-center text-sm text-muted-foreground">第 {page} 页</span>
            <Button variant="outline" size="sm" onClick={() => setPage((value) => value + 1)} disabled={logs.length < pageSize}>
              下一页
            </Button>
          </div>
        </>
      )}
    </div>
  );
}
