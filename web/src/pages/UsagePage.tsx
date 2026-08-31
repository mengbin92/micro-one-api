import { useQuery } from '@tanstack/react-query';
import { useState } from 'react';
import { createPortal } from 'react-dom';
import { ArrowDown, ArrowUp, Database } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { EmptyState } from '@/components/EmptyState';
import { TableSkeleton } from '@/components/LoadingStates';
import { apiClient } from '@/lib/api';
import { unwrapApiData } from '@/lib/api-response';
import { formatAmountUnits } from '@/lib/amount';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { locale, t } from '@/lib/i18n';

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
  cache_read_tokens?: number;
  elapsed_time?: number;
  is_stream?: boolean;
  cost_source?: string;
  subscription_cost?: number;
  balance_cost?: number;
  // token-usage-billing-semantics-remediation §9.1 display contract.
  uncached_input_tokens?: number;
  reported_prompt_tokens?: number;
  reported_total_tokens?: number;
  billable_total_tokens?: number;
  cache_creation_5m_tokens?: number;
  cache_creation_1h_tokens?: number;
  usage_semantics?: string;
  usage_parse_status?: string;
  usage_decision_reason?: string;
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
  return formatAmountUnits(value);
}

function formatDate(value: number) {
  if (!value) return '-';
  return new Date(value * 1000).toLocaleString(locale());
}

function formatDuration(value?: number) {
  if (!value || value <= 0) return '-';
  return value >= 1000 ? `${(value / 1000).toFixed(2)}s` : `${value}ms`;
}

// §9.1 display contract: rows written before the usage-semantics contract
// (usage_parse_status='legacy' or absent) have UNKNOWN historical semantics.
// They are rendered from their raw reported fields and tagged 历史口径; the
// UI must NOT fabricate a pseudo-precise uncached input via prompt-cache.
function isLegacyRow(log: UsageLog) {
  return !log.usage_parse_status || log.usage_parse_status === 'legacy';
}

function uncachedInputTokens(log: UsageLog) {
  if (!isLegacyRow(log)) {
    // The canonical bucket is authoritative; never re-derive prompt-cache.
    return log.uncached_input_tokens || 0;
  }
  return log.prompt_tokens || 0;
}

function displayTotalTokens(log: UsageLog) {
  if (!isLegacyRow(log)) {
    // billable total is the financial total (includes every cache bucket).
    if ((log.billable_total_tokens || 0) > 0) return log.billable_total_tokens || 0;
    return log.reported_total_tokens || log.quota || 0;
  }
  return log.quota || (log.prompt_tokens || 0) + (log.completion_tokens || 0) + (log.cache_read_tokens || 0);
}

function hasTokenBreakdown(log: UsageLog) {
  return (log.prompt_tokens || 0) > 0 || (log.completion_tokens || 0) > 0 || (log.cache_read_tokens || 0) > 0;
}

function compactToken(value?: number) {
  const safeValue = value || 0;
  if (safeValue >= 1000000) return `${(safeValue / 1000000).toFixed(2)}M`;
  if (safeValue >= 1000) return `${(safeValue / 1000).toFixed(1)}K`;
  return safeValue.toLocaleString(locale());
}

function CostCell({ log }: { log: UsageLog }) {
  const subscriptionCost = Math.abs(log.subscription_cost || 0);
  const balanceCost = Math.abs(log.balance_cost || 0);
  const total = Math.abs(log.amount ?? 0);

  if (subscriptionCost > 0 || balanceCost > 0) {
    return (
      <div className="space-y-1">
        <div className="font-semibold text-slate-900 dark:text-white">{formatQuota(total)}</div>
        {subscriptionCost > 0 ? (
          <div className="text-xs font-semibold text-sky-600 dark:text-sky-300">{t("订阅额度")}{formatQuota(subscriptionCost)}</div>
        ) : null}
        {balanceCost > 0 ? (
          <div className="text-xs font-semibold text-emerald-600 dark:text-emerald-300">{t("余额")}{formatQuota(balanceCost)}</div>
        ) : null}
      </div>
    );
  }

  return <span className="font-semibold text-emerald-600">{formatQuota(total)}</span>;
}

interface TooltipState {
  x: number;
  y: number;
  placement: 'top' | 'bottom';
}

function TokenUsageTooltip({
  log,
  inputTokens,
  upstreamInputTokens,
  outputTokens,
  cacheReadTokens,
  cacheCreationTokens,
  total,
  state,
}: {
  log: UsageLog;
  inputTokens: number;
  upstreamInputTokens: number;
  outputTokens: number;
  cacheReadTokens: number;
  cacheCreationTokens: number;
  total: number;
  state: TooltipState;
}) {
  return createPortal(
    <div
      className="pointer-events-none fixed z-[100] w-52 rounded-lg bg-slate-950 px-4 py-3 text-xs font-medium text-slate-300 shadow-xl dark:bg-slate-900"
      style={{
        left: state.x,
        top: state.y,
        transform: state.placement === 'top' ? 'translate(-50%, -100%)' : 'translate(-50%, 0)',
      }}
    >
      <div className="mb-2 flex items-center justify-between gap-2">
        <span className="text-sm font-bold text-white">{t("Token 明细")}</span>
        {isLegacyRow(log) ? (
          <span className="rounded bg-amber-500/20 px-1.5 py-0.5 text-[10px] font-semibold text-amber-300">{t("历史口径")}</span>
        ) : null}
        {log.usage_parse_status === 'ambiguous' ? (
          <span className="rounded bg-rose-500/20 px-1.5 py-0.5 text-[10px] font-semibold text-rose-300">{t("口径存疑")}</span>
        ) : null}
      </div>
      <div className="space-y-1">
        <div className="flex items-center justify-between gap-4">
          <span>{isLegacyRow(log) ? t("输入 Token（上报）") : t("非缓存输入 Token")}</span>
          <span className="font-bold text-white">{inputTokens.toLocaleString(locale())}</span>
        </div>
        {cacheReadTokens > 0 ? (
          <div className="flex items-center justify-between gap-4 text-slate-500">
            <span>{t("上游输入 Token")}</span>
            <span className="font-bold">{upstreamInputTokens.toLocaleString(locale())}</span>
          </div>
        ) : null}
        <div className="flex items-center justify-between gap-4">
          <span>{t("输出 Token")}</span>
          <span className="font-bold text-white">{outputTokens.toLocaleString(locale())}</span>
        </div>
        <div className="flex items-center justify-between gap-4">
          <span>{t("缓存读取 Token")}</span>
          <span className="font-bold text-white">{cacheReadTokens.toLocaleString(locale())}</span>
        </div>
        {cacheCreationTokens > 0 ? (
          <div className="flex items-center justify-between gap-4">
            <span>{t("缓存创建 Token")}</span>
            <span className="font-bold text-white">{cacheCreationTokens.toLocaleString(locale())}</span>
          </div>
        ) : null}
        <div className="mt-2 flex items-center justify-between gap-4 border-t border-white/10 pt-2">
          <span>{isLegacyRow(log) ? t("总 Token（历史口径）") : t("计费总 Token")}</span>
          <span className="font-bold text-sky-300">{total.toLocaleString(locale())}</span>
        </div>
        {!isLegacyRow(log) && (log.reported_total_tokens || 0) > 0 && (log.reported_total_tokens || 0) !== total ? (
          <div className="flex items-center justify-between gap-4 text-slate-500">
            <span>{t("上游上报总 Token")}</span>
            <span className="font-bold">{(log.reported_total_tokens || 0).toLocaleString(locale())}</span>
          </div>
        ) : null}
      </div>
    </div>,
    document.body,
  );
}

function TokenUsageCell({ log }: { log: UsageLog }) {
  const [tooltip, setTooltip] = useState<TooltipState | null>(null);
  const upstreamInputTokens = log.reported_prompt_tokens || log.prompt_tokens || 0;
  const inputTokens = uncachedInputTokens(log);
  const outputTokens = log.completion_tokens || 0;
  const cacheReadTokens = log.cache_read_tokens || 0;
  const cacheCreationTokens = (log.cache_creation_5m_tokens || 0) + (log.cache_creation_1h_tokens || 0);
  const total = displayTotalTokens(log);

  if (!hasTokenBreakdown(log)) {
    return <span className="text-sm font-semibold">{(log.quota || 0).toLocaleString(locale())}</span>;
  }

  function showTooltip(event: React.MouseEvent<HTMLDivElement> | React.FocusEvent<HTMLDivElement>) {
    const rect = event.currentTarget.getBoundingClientRect();
    const tooltipHeight = cacheReadTokens > 0 ? 164 : 144;
    const gap = 10;
    const hasRoomAbove = rect.top > tooltipHeight + gap;
    const nextX = Math.min(Math.max(rect.left + rect.width / 2, 96), window.innerWidth - 96);
    setTooltip({
      x: nextX,
      y: hasRoomAbove ? rect.top - gap : rect.bottom + gap,
      placement: hasRoomAbove ? 'top' : 'bottom',
    });
  }

  return (
    <div
      className="relative inline-flex min-w-[130px] flex-col gap-1"
      tabIndex={0}
      onMouseEnter={showTooltip}
      onMouseMove={showTooltip}
      onMouseLeave={() => setTooltip(null)}
      onFocus={showTooltip}
      onBlur={() => setTooltip(null)}
    >
      <div className="flex items-center gap-3 text-sm font-semibold text-slate-700 dark:text-slate-200">
        <span className="inline-flex items-center gap-1 tabular-nums">
          <ArrowDown className="size-3.5 text-emerald-500" />
          {compactToken(inputTokens)}
        </span>
        <span className="inline-flex items-center gap-1 tabular-nums">
          <ArrowUp className="size-3.5 text-violet-500" />
          {compactToken(outputTokens)}
        </span>
      </div>
      <div className="inline-flex items-center gap-1 text-xs font-semibold text-sky-600 dark:text-sky-300">
        <Database className="size-3.5" />
        <span className="tabular-nums">{compactToken(cacheReadTokens)}</span>
      </div>
      {tooltip ? (
        <TokenUsageTooltip
          log={log}
          inputTokens={inputTokens}
          upstreamInputTokens={upstreamInputTokens}
          outputTokens={outputTokens}
          cacheReadTokens={cacheReadTokens}
          cacheCreationTokens={cacheCreationTokens}
          total={total}
          state={tooltip}
        />
      ) : null}
    </div>
  );
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
          <h2 className="text-2xl font-black tracking-normal text-slate-950 dark:text-white">{t("使用记录")}</h2>
          <p className="mt-1 text-sm font-medium text-slate-500 dark:text-slate-400">{t("展示 API 消费流水，包含模型、端点、Token 和耗时。")}</p>
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
            <option value="consume">{t("消费记录")}</option>
            <option value="">{t("全部流水")}</option>
          </select>
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              setType('consume');
              setPage(1);
            }}
          >{t("重置")}</Button>
        </div>
      </div>

      {isLoading ? (
        <TableSkeleton columns={[t("API 密钥"), t("模型"), t("端点"), t("类型"), 'Token', t("费用"), t("耗时"), t("时间")]} rows={8} />
      ) : logs.length === 0 ? (
        <EmptyState title={t("暂无使用记录")} description={t("API 请求处理完成后，消费记录会显示在这里。")} />
      ) : (
        <>
          <div className="overflow-x-auto rounded-2xl border border-border bg-card shadow-surface-sm">
            <Table className="min-w-[1050px]">
              <TableHeader>
                <TableRow className="bg-slate-50 hover:bg-slate-50 dark:bg-white/5">
                  <TableHead>{t("API 密钥")}</TableHead>
                  <TableHead>{t("模型")}</TableHead>
                  <TableHead>{t("端点")}</TableHead>
                  <TableHead>{t("类型")}</TableHead>
                  <TableHead>Token</TableHead>
                  <TableHead>{t("费用")}</TableHead>
                  <TableHead>{t("耗时")}</TableHead>
                  <TableHead>{t("时间")}</TableHead>
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
                        {t(LOG_TYPE_NAMES[log.type] || log.type || '-')}
                        {log.is_stream ? ` / ${t('流式')}` : ''}
                      </span>
                    </TableCell>
                    <TableCell>
                      <TokenUsageCell log={log} />
                    </TableCell>
                    <TableCell>
                      <CostCell log={log} />
                    </TableCell>
                    <TableCell>{formatDuration(log.elapsed_time)}</TableCell>
                    <TableCell>{formatDate(log.created_at)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>

          <div className="flex items-center justify-end gap-2">
            <Button variant="outline" size="sm" onClick={() => setPage((value) => Math.max(1, value - 1))} disabled={page === 1}>{t("上一页")}</Button>
            <span className="min-w-14 text-center text-sm text-muted-foreground">{t('页码：')}{page}</span>
            <Button variant="outline" size="sm" onClick={() => setPage((value) => value + 1)} disabled={logs.length < pageSize}>{t("下一页")}</Button>
          </div>
        </>
      )}
    </div>
  );
}
