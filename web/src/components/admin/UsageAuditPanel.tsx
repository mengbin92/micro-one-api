import { ArrowDown, ArrowUp, Database, FileClock, ShieldAlert } from 'lucide-react';
import { formatUSD } from '@/lib/amount';
import { locale, t } from '@/lib/i18n';

// §9 admin usage-detail contract: the fields the billing ledger exposes for
// the usage-semantics audit (migration 085/086) plus the pricing evidence
// (migration 088). Rows written before the contract surface with
// usage_parse_status 'legacy' (or the field absent) — they are rendered from
// their raw reported fields and tagged 历史口径, never re-derived.
export interface UsageAuditLog {
  promptTokens?: number | string;
  completionTokens?: number | string;
  cacheReadTokens?: number | string;
  cacheCreation5mTokens?: number | string;
  cacheCreation1hTokens?: number | string;
  quota?: number | string;
  promptCost?: number | string;
  completionCost?: number | string;
  cacheReadCost?: number | string;
  cacheCreation5mCost?: number | string;
  cacheCreation1hCost?: number | string;
  shadowCost?: number | string;
  uncachedInputTokens?: number | string;
  reportedPromptTokens?: number | string;
  reportedTotalTokens?: number | string;
  billableTotalTokens?: number | string;
  usageSemantics?: string;
  usageProtocol?: string;
  usageFieldShape?: string;
  usageParseStatus?: string;
  usageContractVersion?: number | string;
  usageDecisionReason?: string;
  subsetCandidateCost?: number | string;
  exclusiveCandidateCost?: number | string;
  pricingConfigHash?: string;
  pricingSnapshot?: PricingSnapshot | null;
}

export interface PricingSnapshot {
  configHash?: string;
  modelName?: string;
  inputPrice?: number;
  outputPrice?: number;
  cacheReadPrice?: number;
  cacheCreation5mPrice?: number;
  cacheCreation1hPrice?: number;
  groupRatio?: number;
  cacheCreationMode?: string;
  snapshotVersion?: number;
}

const PARSE_STATUS_BADGES: Record<string, { label: string; className: string }> = {
  verified: { label: 'verified', className: 'bg-emerald-100 text-emerald-800 dark:bg-emerald-900 dark:text-emerald-200' },
  estimated: { label: 'estimated', className: 'bg-sky-100 text-sky-800 dark:bg-sky-900 dark:text-sky-200' },
  ambiguous: { label: 'ambiguous', className: 'bg-rose-100 text-rose-800 dark:bg-rose-900 dark:text-rose-200' },
  legacy: { label: '历史口径', className: 'bg-amber-100 text-amber-800 dark:bg-amber-900 dark:text-amber-200' },
};

const SEMANTICS_LABELS: Record<string, string> = {
  openai_subset: 'OpenAI 子集（prompt 含 cache）',
  anthropic_exclusive: 'Anthropic 互斥桶',
};

function num(value: number | string | undefined | null): number {
  const parsed = Number(value ?? 0);
  return Number.isFinite(parsed) ? parsed : 0;
}

function isLegacyUsageRow(log: UsageAuditLog) {
  return !log.usageParseStatus || log.usageParseStatus === 'legacy';
}

// §9.1 legacy display contract: rows predating the usage-semantics contract
// have no canonical billable total. Prefer the explicit reported total, then
// the legacy quota (reported-total meaning), then the raw bucket sum — never
// a fabricated canonical value, and never 0 when the row clearly has usage.
function legacyDisplayTotal(log: UsageAuditLog) {
  const reported = num(log.reportedTotalTokens) || num(log.billableTotalTokens);
  if (reported > 0) return reported;
  const quota = num(log.quota);
  if (quota > 0) return quota;
  return num(log.promptTokens) + num(log.completionTokens) + num(log.cacheReadTokens);
}

function formatTokens(value: number) {
  return value.toLocaleString(locale());
}

function formatUnitPrice(value: number | undefined) {
  if (value === undefined || value === null) return '—';
  if (value === 0) return '0';
  // Per-token prices are tiny (e.g. 1.5e-6); keep 4 significant digits for
  // vendor-sheet comparison, without trailing-zero padding.
  return `$${String(Number(value.toPrecision(4)))}`;
}

function ParseStatusBadge({ log }: { log: UsageAuditLog }) {
  const status = log.usageParseStatus || 'legacy';
  const badge = PARSE_STATUS_BADGES[status] ?? {
    label: status,
    className: 'bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-300',
  };
  return (
    <span className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium ${badge.className}`}>
      {badge.label}
    </span>
  );
}

function BucketRow({
  label,
  tokens,
  unitPrice,
  cost,
  strong,
}: {
  label: string;
  tokens: number;
  unitPrice?: number;
  cost?: number | string;
  strong?: boolean;
}) {
  return (
    <tr className={strong ? 'font-semibold' : undefined}>
      <td className="py-1.5 pr-3 text-muted-foreground">{label}</td>
      <td className="py-1.5 pr-3 text-right tabular-nums">{formatTokens(tokens)}</td>
      <td className="py-1.5 pr-3 text-right tabular-nums text-muted-foreground">
        {unitPrice !== undefined ? formatUnitPrice(unitPrice) : '—'}
      </td>
      <td className="py-1.5 text-right tabular-nums">{cost === undefined ? '—' : formatUSD(cost)}</td>
    </tr>
  );
}

// The full usage/billing audit panel for the admin ledger detail view:
// five canonical buckets with per-bucket cost and the frozen unit prices,
// reported vs billable totals, the parse verdict, and the pricing snapshot.
export function UsageAuditPanel({ log }: { log: UsageAuditLog }) {
  const legacy = isLegacyUsageRow(log);
  const ambiguous = log.usageParseStatus === 'ambiguous';
  const snapshot = log.pricingSnapshot ?? null;

  // Legacy rows have no canonical buckets: prompt semantics are unknowable, so
  // only the raw reported buckets are shown (never a fabricated uncached值).
  const uncached = legacy ? 0 : num(log.uncachedInputTokens);
  const cacheRead = num(log.cacheReadTokens);
  const creation5m = num(log.cacheCreation5mTokens);
  const creation1h = num(log.cacheCreation1hTokens);
  const output = num(log.completionTokens);
  const billableTotal = legacy ? legacyDisplayTotal(log) : num(log.billableTotalTokens);
  const bucketCostTotal =
    num(log.promptCost) + num(log.completionCost) + num(log.cacheReadCost) +
    num(log.cacheCreation5mCost) + num(log.cacheCreation1hCost);

  const hasUsage =
    uncached > 0 || cacheRead > 0 || creation5m > 0 || creation1h > 0 || output > 0 ||
    num(log.promptTokens) > 0 || num(log.reportedTotalTokens) > 0;
  if (!hasUsage) {
    return (
      <div className="rounded-lg border bg-muted/30 p-3 text-xs text-muted-foreground">
        {t('该记录没有 token 用量数据（非 consume 记录或早期数据）。')}
      </div>
    );
  }

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <ParseStatusBadge log={log} />
        {log.usageSemantics ? (
          <span className="rounded-full bg-slate-100 px-2 py-0.5 text-xs font-medium text-slate-700 dark:bg-slate-800 dark:text-slate-300">
            {t(SEMANTICS_LABELS[log.usageSemantics] ?? log.usageSemantics)}
          </span>
        ) : null}
        {log.usageProtocol ? (
          <span className="rounded-full bg-slate-100 px-2 py-0.5 text-xs font-medium text-slate-700 dark:bg-slate-800 dark:text-slate-300">
            {log.usageProtocol}
          </span>
        ) : null}
        {log.usageContractVersion ? (
          <span className="text-xs text-muted-foreground">contract v{log.usageContractVersion}</span>
        ) : null}
      </div>

      {legacy ? (
        <div className="flex items-start gap-2 rounded-lg border border-amber-300/60 bg-amber-500/10 p-3 text-xs text-amber-800 dark:text-amber-200">
          <FileClock className="mt-0.5 size-4 shrink-0" />
          <div>
            {t('历史口径：该记录早于用量语义契约，prompt 与 cache 的关系不可考。以下展示原始上报字段，不代表五桶计费口径，也不能据此推断单价。')}
          </div>
        </div>
      ) : null}

      {ambiguous ? (
        <div className="flex items-start gap-2 rounded-lg border border-rose-300/60 bg-rose-500/10 p-3 text-xs text-rose-800 dark:text-rose-200">
          <ShieldAlert className="mt-0.5 size-4 shrink-0" />
          <div className="space-y-1">
            <div>
              {t('口径存疑：按两种候选中的较低值结算，未多扣用户。')}
              {log.usageDecisionReason ? <span className="font-mono"> ({log.usageDecisionReason})</span> : null}
            </div>
            <div className="flex flex-wrap gap-x-6 gap-y-1 tabular-nums">
              <span>{t('subset 候选成本')}: {formatUSD(log.subsetCandidateCost)}</span>
              <span>{t('exclusive 候选成本')}: {formatUSD(log.exclusiveCandidateCost)}</span>
            </div>
          </div>
        </div>
      ) : null}

      <div className="overflow-x-auto rounded-lg border">
        <table className="w-full text-xs">
          <thead>
            <tr className="border-b bg-muted/40 text-muted-foreground">
              <th className="px-3 py-2 text-left font-medium">{t('计费桶')}</th>
              <th className="px-3 py-2 text-right font-medium">{t('Token')}</th>
              <th className="px-3 py-2 text-right font-medium">{t('单价（快照）')}</th>
              <th className="px-3 py-2 text-right font-medium">{t('成本')}</th>
            </tr>
          </thead>
          <tbody className="px-3 py-2">
            {legacy ? (
              <>
                <BucketRow label={t('上报输入 Token')} tokens={num(log.promptTokens)} cost={log.promptCost} />
                <BucketRow label={t('输出 Token')} tokens={output} unitPrice={snapshot?.outputPrice} cost={log.completionCost} />
                <BucketRow label={t('缓存读取 Token（上报）')} tokens={cacheRead} cost={log.cacheReadCost} />
              </>
            ) : (
              <>
                <BucketRow label={t('非缓存输入')} tokens={uncached} unitPrice={snapshot?.inputPrice} cost={log.promptCost} />
                <BucketRow label={t('缓存读取')} tokens={cacheRead} unitPrice={snapshot?.cacheReadPrice} cost={log.cacheReadCost} />
                <BucketRow label={t('缓存创建 5m')} tokens={creation5m} unitPrice={snapshot?.cacheCreation5mPrice} cost={log.cacheCreation5mCost} />
                <BucketRow label={t('缓存创建 1h')} tokens={creation1h} unitPrice={snapshot?.cacheCreation1hPrice} cost={log.cacheCreation1hCost} />
                <BucketRow label={t('输出')} tokens={output} unitPrice={snapshot?.outputPrice} cost={log.completionCost} />
              </>
            )}
            <tr className="border-t bg-muted/30">
              <td className="px-3 py-2 font-medium">{legacy ? t('上报总量（参考）') : t('计费总 Token（含全部缓存桶）')}</td>
              <td className="px-3 py-2 text-right font-semibold tabular-nums text-sky-700 dark:text-sky-300">
                {formatTokens(billableTotal)}
              </td>
              <td className="px-3 py-2 text-right text-muted-foreground">{t('成本合计')}</td>
              <td className="px-3 py-2 text-right font-semibold tabular-nums">{formatUSD(bucketCostTotal)}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <div className="grid gap-2 text-xs sm:grid-cols-2">
        <div className="rounded-lg border bg-muted/30 p-3">
          <div className="mb-1 font-medium text-muted-foreground">{t('上游上报')}</div>
          <div className="grid grid-cols-2 gap-y-1 tabular-nums">
            <span className="text-muted-foreground">{t('上报 prompt/input')}</span>
            <span className="text-right font-medium">{formatTokens(num(log.reportedPromptTokens) || num(log.promptTokens))}</span>
            <span className="text-muted-foreground">{t('上报 total')}</span>
            <span className="text-right font-medium">{formatTokens(num(log.reportedTotalTokens))}</span>
            {log.usageFieldShape ? (
              <>
                <span className="text-muted-foreground">{t('字段形态')}</span>
                <span className="text-right font-mono">{log.usageFieldShape}</span>
              </>
            ) : null}
          </div>
        </div>
        <div className="rounded-lg border bg-muted/30 p-3">
          <div className="mb-1 font-medium text-muted-foreground">{t('定价快照')}</div>
          {snapshot ? (
            <div className="grid grid-cols-2 gap-y-1 tabular-nums">
              <span className="text-muted-foreground">{t('模型（定价键）')}</span>
              <span className="text-right font-mono">{snapshot.modelName || '—'}</span>
              <span className="text-muted-foreground">{t('分组倍率')}</span>
              <span className="text-right font-medium">×{snapshot.groupRatio ?? 1}</span>
              <span className="text-muted-foreground">{t('缓存创建模式')}</span>
              <span className="text-right font-medium">{snapshot.cacheCreationMode || '—'}</span>
              <span className="col-span-2 truncate font-mono text-[10px] text-muted-foreground" title={snapshot.configHash || log.pricingConfigHash}>
                {snapshot.configHash || log.pricingConfigHash}
              </span>
            </div>
          ) : (
            <div className="text-muted-foreground">
              {log.pricingConfigHash
                ? t('快照数据缺失（hash 存在但快照不可用）。')
                : t('无定价快照：该记录早于 088 或按倍率计价（无五桶单价），不能从账本金额反推单价。')}
            </div>
          )}
          {num(log.shadowCost) > 0 ? (
            <div className="mt-1 tabular-nums text-muted-foreground">
              {t('缓存创建影子成本')}: {formatUSD(log.shadowCost)}
            </div>
          ) : null}
        </div>
      </div>
    </div>
  );
}

// Compact list cell: input/output/cache totals with the parse-status hint.
export function UsageSummaryCell({ log }: { log: UsageAuditLog }) {
  const legacy = isLegacyUsageRow(log);
  const ambiguous = log.usageParseStatus === 'ambiguous';
  // Non-legacy rows: the canonical uncached bucket is authoritative — never
  // fall back to the (possibly inclusive) prompt_tokens, which would
  // double-count a fully-cached request's input.
  const input = legacy ? num(log.promptTokens) : num(log.uncachedInputTokens);
  const cacheRead = num(log.cacheReadTokens);
  const creation = num(log.cacheCreation5mTokens) + num(log.cacheCreation1hTokens);
  const total = legacy
    ? legacyDisplayTotal(log)
    : num(log.billableTotalTokens) || num(log.reportedTotalTokens);

  if (input <= 0 && num(log.completionTokens) <= 0 && cacheRead <= 0 && creation <= 0 && total <= 0) {
    return <span className="text-xs text-muted-foreground">—</span>;
  }

  return (
    <div className="flex flex-col gap-0.5">
      <div className="flex items-center gap-2 text-xs font-semibold tabular-nums">
        <span className="inline-flex items-center gap-0.5">
          <ArrowDown className="size-3 text-emerald-500" />
          {input.toLocaleString(locale())}
        </span>
        <span className="inline-flex items-center gap-0.5">
          <ArrowUp className="size-3 text-violet-500" />
          {num(log.completionTokens).toLocaleString(locale())}
        </span>
        {cacheRead > 0 || creation > 0 ? (
          <span className="inline-flex items-center gap-0.5 text-sky-600 dark:text-sky-300">
            <Database className="size-3" />
            {(cacheRead + creation).toLocaleString(locale())}
          </span>
        ) : null}
      </div>
      <div className="flex items-center gap-1.5 text-[10px] text-muted-foreground">
        <span className="tabular-nums">Σ {total.toLocaleString(locale())}</span>
        {legacy ? <span className="rounded bg-amber-500/15 px-1 py-px text-amber-700 dark:text-amber-300">{t('历史')}</span> : null}
        {ambiguous ? <span className="rounded bg-rose-500/15 px-1 py-px text-rose-700 dark:text-rose-300">{t('存疑')}</span> : null}
      </div>
    </div>
  );
}
