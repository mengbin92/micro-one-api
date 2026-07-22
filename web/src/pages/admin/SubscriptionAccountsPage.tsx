import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { KeyRound, Pencil, RotateCcw, Save } from 'lucide-react';
import { useMemo, useState } from 'react';
import { toast } from 'sonner';
import { adminApiClient } from '@/lib/api';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { EmptyState } from '@/components/EmptyState';
import { TableSkeleton } from '@/components/LoadingStates';
import { AdminPagination } from '@/components/admin/AdminPagination';
import { AdminTableToolbar } from '@/components/admin/AdminTableToolbar';
import { SortableHeader } from '@/components/admin/SortableHeader';
import { useAdminTableState } from '@/hooks/useAdminTableState';
import { OAuthBindDialog } from '@/pages/admin/OAuthBindDialog';
import { buildAdminListParams } from '@/lib/admin-table-query';
import { ensureApiSuccess } from '@/lib/api-response';
import { sortRows, type SortState } from '@/lib/table-utils';
import { cn } from '@/lib/utils';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog';
import { AccountStatusBadge } from '@/components/admin/AccountStatusBadge';
import {
  normalizeSubscriptionAccount,
  type RawSubscriptionAccount,
  type SubscriptionAccountSummary,
} from '@/lib/subscription-account';

// Mirrors common.v1.SubscriptionAccountInfo JSON tags returned by
// GET /api/subscription-accounts/{id}. The protobuf-generated JSON uses
// snake_case keys, and nullable string fields come back as null, so they are
// typed as optional and coerced to "" in toDraft().
interface SubscriptionAccountInfo {
  id: number;
  name: string;
  platform: string;
  account_type: string;
  status: number;
  group: string;
  models: string;
  priority: number;
  base_url: string | null;
  access_token: string;
  refresh_token: string;
  expires_at: number;
  account_id: string | null;
  fingerprint: string | null;
  metadata: string | null;
  created_at: number;
  updated_at: number;
  quota_limit_usd?: number;
  quota_used_usd?: number;
  quota_5h_limit_usd?: number;
  quota_5h_used_usd?: number;
  quota_5h_window_start?: number;
  quota_daily_limit_usd?: number;
  quota_daily_used_usd?: number;
  quota_daily_window_start?: number;
  quota_weekly_limit_usd?: number;
  quota_weekly_used_usd?: number;
  quota_weekly_window_start?: number;
  rate_multiplier?: number;
  rpm_limit?: number;
  session_window_limit_usd?: number;
  quota_reset_strategy?: string;
  quota_timezone?: string;
}

interface SubscriptionAccountEditDraft {
  id: number;
  name: string;
  accountType: string;
  group: string;
  models: string;
  priority: string;
  baseUrl: string;
  accessToken: string;
  refreshToken: string;
  expiresAt: string;
  accountId: string;
  fingerprint: string;
  metadata: string;
  quotaLimitUsd: string;
  quotaUsedUsd: string;
  quota5hLimitUsd: string;
  quota5hUsedUsd: string;
  quotaDailyLimitUsd: string;
  quotaDailyUsedUsd: string;
  quotaWeeklyLimitUsd: string;
  quotaWeeklyUsedUsd: string;
  rateMultiplier: string;
  rpmLimit: string;
  sessionWindowLimitUsd: string;
  quotaResetStrategy: string;
  quotaTimezone: string;
}

interface CreatePayload {
  name: string;
  platform: string;
  account_type: string;
  group: string;
  models: string;
  priority: number;
  base_url: string;
  access_token: string;
  refresh_token: string;
  expires_at: number;
  account_id: string;
  fingerprint: string;
  metadata: string;
  quota_limit_usd: number;
  quota_used_usd: number;
  quota_5h_limit_usd: number;
  quota_5h_used_usd: number;
  quota_daily_limit_usd: number;
  quota_daily_used_usd: number;
  quota_weekly_limit_usd: number;
  quota_weekly_used_usd: number;
  rate_multiplier: number;
  rpm_limit: number;
  session_window_limit_usd: number;
  quota_reset_strategy: string;
  quota_timezone: string;
}

interface UpdatePayload {
  id: number;
  name: string;
  account_type: string;
  group: string;
  models: string;
  priority: number;
  base_url: string;
  access_token: string;
  refresh_token: string;
  expires_at: number;
  account_id: string;
  fingerprint: string;
  metadata: string;
  quota_limit_usd: number;
  quota_used_usd: number;
  quota_5h_limit_usd: number;
  quota_5h_used_usd: number;
  quota_daily_limit_usd: number;
  quota_daily_used_usd: number;
  quota_weekly_limit_usd: number;
  quota_weekly_used_usd: number;
  rate_multiplier: number;
  rpm_limit: number;
  session_window_limit_usd: number;
  quota_reset_strategy: string;
  quota_timezone: string;
}

interface BatchQuotaTemplateForm {
  quotaLimitUsd: string;
  quota5hLimitUsd: string;
  quotaDailyLimitUsd: string;
  quotaWeeklyLimitUsd: string;
  rateMultiplier: string;
  rpmLimit: string;
  sessionWindowLimitUsd: string;
  quotaResetStrategy: string;
  quotaTimezone: string;
}

// Subscription account platforms supported by the hybrid relay adaptor layer
// (internal/identity + domain/upstream/credential). Keep in sync with the
// PlatformXxx constants. Claude/Codex are OAuth; Zhipu/MiniMax use a static
// Coding-Plan key; Kimi uses OAuth refresh. See
// docs/design/cn-subscription-accounts-roadmap.md.
const PLATFORM_OPTIONS: Array<{ value: string; label: string }> = [
  { value: 'claude', label: 'Claude (Claude Code OAuth)' },
  { value: 'codex', label: 'Codex (ChatGPT OAuth)' },
  { value: 'zhipu', label: 'Zhipu GLM (Coding Plan, 静态 Key)' },
  { value: 'minimax', label: 'MiniMax (Coding Plan, 静态 Key)' },
  { value: 'kimi', label: 'Kimi (Kimi For Coding, 静态 Key)' },
];

// STATIC_KEY_PLATFORMS are platforms whose Coding Plan authenticates with a
// long-lived API key (no refresh token). Their Create form requires only an
// access_token; expires_at is left at 0 (semantic "never expires").
const STATIC_KEY_PLATFORMS = new Set(['zhipu', 'minimax', 'kimi']);

const ACCOUNT_TYPE_OPTIONS: Array<{ value: string; label: string }> = [
  { value: 'oauth', label: 'OAuth 订阅账号' },
  { value: 'static_key', label: '静态 Key 订阅账号 (GLM/MiniMax/Kimi Coding Plan)' },
];

const QUOTA_RESET_STRATEGY_OPTIONS: Array<{ value: string; label: string }> = [
  { value: 'rolling', label: '滚动窗口' },
  { value: 'fixed', label: '固定周期' },
];

function platformLabel(platform: string) {
  return PLATFORM_OPTIONS.find((option) => option.value === platform)?.label ?? platform;
}

function formatTimestamp(unix?: number) {
  if (!unix) return '—';
  return new Date(unix * 1000).toLocaleString();
}

function formatPercent(value: number) {
  if (!Number.isFinite(value)) return '—';
  const rounded = Math.round(value * 10) / 10;
  return `${Number.isInteger(rounded) ? rounded.toFixed(0) : rounded.toFixed(1)}%`;
}

function formatWindowLabel(minutes?: number | null) {
  if (!minutes) return '配额';
  if (minutes === 300) return '5小时';
  if (minutes === 10080) return '7天';
  if (minutes % 1440 === 0) return `${minutes / 1440}天`;
  if (minutes % 60 === 0) return `${minutes / 60}小时`;
  return `${minutes}分钟`;
}

function formatResetAfter(seconds?: number | null) {
  if (seconds == null || !Number.isFinite(seconds)) return '';
  if (seconds <= 0) return '即将重置';
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  if (days > 0) return `${days}天${hours > 0 ? `${hours}小时` : ''}后`;
  if (hours > 0) return `${hours}小时${minutes > 0 ? `${minutes}分钟` : ''}后`;
  if (minutes > 0) return `${minutes}分钟后`;
  return '1分钟内';
}

function formatUSD(value?: number | null) {
  const n = Number(value ?? 0);
  if (!Number.isFinite(n)) return '$0.00';
  return `$${n.toFixed(2)}`;
}

function parseNumberInput(value: string) {
  const n = Number(value);
  return Number.isFinite(n) && n > 0 ? n : 0;
}

function normalizeQuotaResetStrategy(value?: string | null) {
  return value === 'fixed' ? 'fixed' : 'rolling';
}

function normalizeQuotaTimezone(value?: string | null) {
  const trimmed = (value ?? '').trim();
  return trimmed || 'UTC';
}

function optionalNumberInput(value: string) {
  const trimmed = value.trim();
  if (trimmed === '') return undefined;
  const n = Number(trimmed);
  return Number.isFinite(n) && n >= 0 ? n : null;
}

function localQuotaRows(account: SubscriptionAccountSummary) {
  const quota5hUsedUsd = account.quota5hUsedUsd;
  const quota5hLimitUsd = account.quota5hLimitUsd;
  return [
    { label: '总额', used: account.quotaUsedUsd, limit: account.quotaLimitUsd },
    { label: '5h', used: quota5hUsedUsd, limit: quota5hLimitUsd },
    { label: '24h', used: account.quotaDailyUsedUsd, limit: account.quotaDailyLimitUsd },
    { label: '7d', used: account.quotaWeeklyUsedUsd, limit: account.quotaWeeklyLimitUsd },
  ].filter((row) => (row.used ?? 0) > 0 || (row.limit ?? 0) > 0);
}

function localQuotaState(account: SubscriptionAccountSummary) {
  const rows = localQuotaRows(account).filter((row) => Number(row.limit ?? 0) > 0);
  if (rows.some((row) => Number(row.used ?? 0) >= Number(row.limit ?? 0))) {
    return 'exhausted';
  }
  if (rows.some((row) => Number(row.used ?? 0) / Number(row.limit ?? 1) >= 0.8)) {
    return 'almost';
  }
  if (!account.lastUsedAt && localQuotaRows(account).every((row) => Number(row.used ?? 0) <= 0)) {
    return 'no_usage';
  }
  return 'ok';
}

function matchesLocalQuotaFilter(account: SubscriptionAccountSummary, filter: string) {
  if (!filter) return true;
  return localQuotaState(account) === filter;
}

function resetAfterFromUnix(resetAt?: number) {
  if (!resetAt) return null;
  return Math.max(0, Math.round(resetAt - Date.now() / 1000));
}

function quotaWindows(account: SubscriptionAccountSummary) {
  const windows = [
    {
      key: 'primary',
      label: formatWindowLabel(account.primaryQuotaWindowMinutes),
      usedPercent: account.primaryQuotaUsedPercent,
      resetAfter: account.primaryQuotaResetAfterSeconds,
    },
    {
      key: 'secondary',
      label: formatWindowLabel(account.secondaryQuotaWindowMinutes),
      usedPercent: account.secondaryQuotaUsedPercent,
      resetAfter: account.secondaryQuotaResetAfterSeconds,
    },
  ].filter((item) => item.usedPercent != null || item.resetAfter != null);

  if (windows.length > 0) return windows;
  const usedPercent = account.quotaUsedPercent;
  const resetAt = account.quotaResetAt;
  if (usedPercent != null || resetAt) {
    return [
      {
        key: 'quota',
        label: '配额',
        usedPercent,
        resetAfter: resetAfterFromUnix(resetAt),
      },
    ];
  }
  return [];
}

function QuotaStatusCell({ account, now }: { account: SubscriptionAccountSummary; now: number }) {
  const windows = quotaWindows(account);
  const localRows = localQuotaRows(account);
  const rpmLimit = account.rpmLimit ?? 0;
  const sessionWindowLimitUsd = account.sessionWindowLimitUsd ?? 0;
  const resetStrategy = normalizeQuotaResetStrategy(account.quotaResetStrategy);
  const quotaTimezone = normalizeQuotaTimezone(account.quotaTimezone);
  const snapshotPaused = account.quotaSnapshotPaused;
  if (windows.length === 0 && localRows.length === 0 && rpmLimit <= 0 && sessionWindowLimitUsd <= 0 && resetStrategy !== 'fixed') {
    return <span className="text-sm text-muted-foreground">—</span>;
  }
  return (
    <div className="min-w-[170px] space-y-1">
      {localRows.map((row) => {
        const used = Number(row.used ?? 0);
        const limit = Number(row.limit ?? 0);
        const ratio = limit > 0 ? used / limit : 0;
        const barWidth = limit > 0 ? Math.max(0, Math.min(100, ratio * 100)) : 0;
        const barColor = ratio >= 1 ? 'bg-red-500' : ratio >= 0.8 ? 'bg-amber-500' : 'bg-emerald-500';
        return (
          <div key={row.label} className="space-y-0.5">
            <div className="flex items-center justify-between gap-2 text-xs">
              <span className="font-medium">{row.label}</span>
              <span className="tabular-nums text-muted-foreground">
                {formatUSD(used)}
                {limit > 0 ? ` / ${formatUSD(limit)}` : ''}
              </span>
            </div>
            {limit > 0 && (
              <div className="h-1.5 overflow-hidden rounded-full bg-muted">
                <div className={cn('h-full rounded-full transition-all', barColor)} style={{ width: `${barWidth}%` }} />
              </div>
            )}
          </div>
        );
      })}
      {windows.map((window) => {
        const usedPercent = window.usedPercent ?? 0;
        const barWidth = Math.max(0, Math.min(100, usedPercent));
        const barColor = usedPercent >= 100 ? 'bg-red-500' : usedPercent >= 80 ? 'bg-amber-500' : 'bg-emerald-500';
        const resetAfter = formatResetAfter(window.resetAfter);
        return (
          <div key={window.key} className="space-y-0.5">
            <div className="flex items-center justify-between gap-2 text-xs">
              <span className="font-medium">{window.label}</span>
              <span className="tabular-nums text-muted-foreground">{formatPercent(usedPercent)}</span>
            </div>
            <div className="h-1.5 overflow-hidden rounded-full bg-muted">
              <div
                className={cn('h-full rounded-full transition-all', barColor)}
                style={{ width: `${barWidth}%` }}
              />
            </div>
            {resetAfter && <div className="text-[11px] text-muted-foreground">重置：{resetAfter}</div>}
          </div>
        );
      })}
      {snapshotPaused && (
        <span className="inline-flex rounded bg-amber-100 px-1.5 py-0.5 text-[11px] font-medium text-amber-800 dark:bg-amber-900 dark:text-amber-200">
          已因限额暂停
        </span>
      )}
      {rpmLimit > 0 && (
        <span className="inline-flex rounded bg-slate-100 px-1.5 py-0.5 text-[11px] font-medium text-slate-700 dark:bg-slate-800 dark:text-slate-200">
          RPM {rpmLimit}/min
        </span>
      )}
      {sessionWindowLimitUsd > 0 && (
        <span className="inline-flex rounded bg-slate-100 px-1.5 py-0.5 text-[11px] font-medium text-slate-700 dark:bg-slate-800 dark:text-slate-200">
          Session {formatUSD(sessionWindowLimitUsd)}
        </span>
      )}
      {resetStrategy === 'fixed' && (
        <span className="inline-flex rounded bg-slate-100 px-1.5 py-0.5 text-[11px] font-medium text-slate-700 dark:bg-slate-800 dark:text-slate-200">
          固定周期 {quotaTimezone}
        </span>
      )}
      {(() => {
        const reason = account.unschedulableReason;
        const since = account.unschedulableSince;
        if (!reason || !since || since <= 0) return null;
        const recoveryAt = account.expectedRecoveryAt ?? 0;
        const recoveryLabel = recoveryAt > 0 ? formatResetAfter(Math.max(0, recoveryAt - now)) : '未知';
        return (
          <div className="flex items-center gap-1.5 rounded bg-amber-50 px-1.5 py-0.5 text-[11px] font-medium text-amber-800 dark:bg-amber-900/30 dark:text-amber-200">
            <span className="truncate max-w-[160px]" title={reason}>{reason}</span>
            <span className="text-amber-600 dark:text-amber-400">·</span>
            <span>{recoveryLabel}</span>
          </div>
        );
      })()}
    </div>
  );
}

function toDraft(account: SubscriptionAccountInfo): SubscriptionAccountEditDraft {
  return {
    id: account.id,
    name: account.name,
    accountType: account.account_type,
    group: account.group,
    models: account.models,
    priority: String(account.priority ?? 0),
    baseUrl: account.base_url ?? '',
    accessToken: '',
    refreshToken: '',
    expiresAt: account.expires_at ? String(account.expires_at) : '',
    accountId: account.account_id ?? '',
    fingerprint: account.fingerprint ?? '',
    metadata: account.metadata ?? '',
    quotaLimitUsd: String(account.quota_limit_usd ?? 0),
    quotaUsedUsd: String(account.quota_used_usd ?? 0),
    quota5hLimitUsd: String(account.quota_5h_limit_usd ?? 0),
    quota5hUsedUsd: String(account.quota_5h_used_usd ?? 0),
    quotaDailyLimitUsd: String(account.quota_daily_limit_usd ?? 0),
    quotaDailyUsedUsd: String(account.quota_daily_used_usd ?? 0),
    quotaWeeklyLimitUsd: String(account.quota_weekly_limit_usd ?? 0),
    quotaWeeklyUsedUsd: String(account.quota_weekly_used_usd ?? 0),
    rateMultiplier: String(account.rate_multiplier && account.rate_multiplier > 0 ? account.rate_multiplier : 1),
    rpmLimit: String(account.rpm_limit ?? 0),
    sessionWindowLimitUsd: String(account.session_window_limit_usd ?? 0),
    quotaResetStrategy: normalizeQuotaResetStrategy(account.quota_reset_strategy),
    quotaTimezone: normalizeQuotaTimezone(account.quota_timezone),
  };
}

export function AdminSubscriptionAccountsPage() {
  const {
    page,
    pageSize,
    search,
    sortKey,
    sortDirection,
    filters,
    setPage,
    setPageSize,
    setSearch,
    clearSearch,
    setSort,
    setFilter,
  } = useAdminTableState({
    storageKey: 'subscription-accounts',
    filters: ['status', 'platform', 'quota'],
  });
  const [isCreateOpen, setIsCreateOpen] = useState(false);
  const [editingAccount, setEditingAccount] = useState<SubscriptionAccountEditDraft | null>(null);
  const [selectedAccountIDs, setSelectedAccountIDs] = useState<Set<number>>(() => new Set());
  const [batchResetScope, setBatchResetScope] = useState('daily');
  const [isBatchTemplateOpen, setIsBatchTemplateOpen] = useState(false);
  const [batchTemplate, setBatchTemplate] = useState<BatchQuotaTemplateForm>({
    quotaLimitUsd: '',
    quota5hLimitUsd: '',
    quotaDailyLimitUsd: '',
    quotaWeeklyLimitUsd: '',
    rateMultiplier: '',
    rpmLimit: '',
    sessionWindowLimitUsd: '',
    quotaResetStrategy: '',
    quotaTimezone: '',
  });
  const queryClient = useQueryClient();

  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: ['admin-subscription-accounts'] });
  };

  const sort = {
    key: sortKey as keyof SubscriptionAccountSummary | null,
    direction: sortDirection,
  } satisfies SortState<SubscriptionAccountSummary>;
  const statusFilter = filters.status ?? '';
  const platformFilter = filters.platform ?? '';
  const quotaFilter = filters.quota ?? '';

  const { data: accounts, isLoading, dataUpdatedAt } = useQuery({
    queryKey: ['admin-subscription-accounts', page, pageSize, search, statusFilter, platformFilter, sortKey, sortDirection],
    queryFn: async () => {
      const params = buildAdminListParams({
        page,
        pageSize,
        search,
        sortKey,
        sortDirection,
        filters: { status: statusFilter, platform: platformFilter },
      });
      const res = await adminApiClient.get(`/subscription-accounts?${params}`);
      const payload = res.data as { accounts?: RawSubscriptionAccount[]; total?: number };
      return (payload.accounts ?? []).map(normalizeSubscriptionAccount);
    },
  });

  const createMutation = useMutation({
    mutationFn: async (payload: CreatePayload) => {
      const res = await adminApiClient.post('/subscription-accounts', payload);
      ensureApiSuccess(res.data, 'Subscription account create failed');
    },
    onSuccess: () => {
      invalidate();
      setIsCreateOpen(false);
      toast.success('订阅账号已创建');
    },
  });

  const updateMutation = useMutation({
    mutationFn: async (draft: SubscriptionAccountEditDraft) => {
      // Only send token fields when the admin entered a new value; otherwise
      // leave them empty so the backend keeps the stored (encrypted) value.
      const payload: UpdatePayload = {
        id: draft.id,
        name: draft.name.trim(),
        account_type: draft.accountType,
        group: draft.group.trim(),
        models: draft.models.trim(),
        priority: parseInt(draft.priority || '0', 10),
        base_url: draft.baseUrl.trim(),
        access_token: draft.accessToken.trim(),
        refresh_token: draft.refreshToken.trim(),
        expires_at: draft.expiresAt ? parseInt(draft.expiresAt, 10) : 0,
        account_id: draft.accountId.trim(),
        fingerprint: draft.fingerprint,
        metadata: draft.metadata,
        quota_limit_usd: parseNumberInput(draft.quotaLimitUsd),
        quota_used_usd: parseNumberInput(draft.quotaUsedUsd),
        quota_5h_limit_usd: parseNumberInput(draft.quota5hLimitUsd),
        quota_5h_used_usd: parseNumberInput(draft.quota5hUsedUsd),
        quota_daily_limit_usd: parseNumberInput(draft.quotaDailyLimitUsd),
        quota_daily_used_usd: parseNumberInput(draft.quotaDailyUsedUsd),
        quota_weekly_limit_usd: parseNumberInput(draft.quotaWeeklyLimitUsd),
        quota_weekly_used_usd: parseNumberInput(draft.quotaWeeklyUsedUsd),
        rate_multiplier: parseNumberInput(draft.rateMultiplier) || 1,
        rpm_limit: Math.max(0, parseInt(draft.rpmLimit || '0', 10) || 0),
        session_window_limit_usd: parseNumberInput(draft.sessionWindowLimitUsd),
        quota_reset_strategy: normalizeQuotaResetStrategy(draft.quotaResetStrategy),
        quota_timezone: normalizeQuotaTimezone(draft.quotaTimezone),
      };
      const res = await adminApiClient.put(`/subscription-accounts/${draft.id}`, payload);
      ensureApiSuccess(res.data, 'Subscription account update failed');
    },
    onSuccess: () => {
      invalidate();
      setEditingAccount(null);
      toast.success('订阅账号配置已保存');
    },
  });

  const toggleStatusMutation = useMutation({
    mutationFn: async ({ id, currentStatus }: { id: number; currentStatus: number }) => {
      const nextStatus = currentStatus === 1 ? 2 : 1;
      const res = await adminApiClient.put(`/subscription-accounts/${id}/status`, {
        account_id: id,
        status: nextStatus,
      });
      ensureApiSuccess(res.data, 'Subscription account status update failed');
    },
    onSuccess: () => {
      invalidate();
      toast.success('订阅账号状态已更新');
    },
  });

  const resetQuotaMutation = useMutation({
    mutationFn: async ({ id, scope }: { id: number; scope: string }) => {
      const res = await adminApiClient.post(`/subscription-accounts/${id}/reset-quota`, { account_id: id, scope });
      ensureApiSuccess(res.data, 'Subscription account quota reset failed');
    },
    onSuccess: () => {
      invalidate();
      toast.success('订阅账号用量已重置');
    },
  });

  const batchResetQuotaMutation = useMutation({
    mutationFn: async ({ ids, scope }: { ids: number[]; scope: string }) => {
      const res = await adminApiClient.post('/subscription-accounts/batch-reset-quota', {
        account_ids: ids,
        scope,
      });
      ensureApiSuccess(res.data, 'Subscription account batch quota reset failed');
    },
    onSuccess: () => {
      invalidate();
      setSelectedAccountIDs(new Set());
      toast.success('已批量重置订阅账号用量');
    },
  });

  const batchQuotaTemplateMutation = useMutation({
    mutationFn: async ({ ids, template }: { ids: number[]; template: Record<string, unknown> }) => {
      const res = await adminApiClient.post('/subscription-accounts/batch-quota-template', {
        account_ids: ids,
        template,
      });
      ensureApiSuccess(res.data, 'Subscription account batch quota template failed');
    },
    onSuccess: () => {
      invalidate();
      setSelectedAccountIDs(new Set());
      setIsBatchTemplateOpen(false);
      toast.success('已批量应用额度模板');
    },
  });

  const deleteMutation = useMutation({
    mutationFn: async (id: number) => {
      const res = await adminApiClient.delete(`/subscription-accounts/${id}`);
      ensureApiSuccess(res.data, 'Subscription account delete failed');
    },
    onSuccess: () => {
      invalidate();
      toast.success('订阅账号已删除');
    },
  });

  const openEdit = async (account: SubscriptionAccountSummary) => {
    try {
      const res = await adminApiClient.get(`/subscription-accounts/${account.id}`);
      const info = res.data as SubscriptionAccountInfo;
      setEditingAccount(toDraft(info));
    } catch {
      toast.error('加载订阅账号详情失败');
    }
  };

  // Render clock: driven by the query's fetch time so render stays pure
  // (no Date.now() during render) and countdowns reflect data freshness.
  const nowUnix = dataUpdatedAt ? Math.floor(dataUpdatedAt / 1000) : 0;

  const visibleAccounts = useMemo(() => {
    return sortRows((accounts ?? []).filter((account) => matchesLocalQuotaFilter(account, quotaFilter)), sort);
  }, [accounts, quotaFilter, sort]);

  const selectedIDs = useMemo(() => Array.from(selectedAccountIDs), [selectedAccountIDs]);
  const visibleAccountIDs = useMemo(() => visibleAccounts.map((account) => account.id), [visibleAccounts]);
  const allVisibleSelected = visibleAccountIDs.length > 0 && visibleAccountIDs.every((id) => selectedAccountIDs.has(id));

  const toggleAccountSelected = (id: number, checked: boolean) => {
    setSelectedAccountIDs((prev) => {
      const next = new Set(prev);
      if (checked) {
        next.add(id);
      } else {
        next.delete(id);
      }
      return next;
    });
  };

  const toggleVisibleSelected = (checked: boolean) => {
    setSelectedAccountIDs((prev) => {
      const next = new Set(prev);
      for (const id of visibleAccountIDs) {
        if (checked) {
          next.add(id);
        } else {
          next.delete(id);
        }
      }
      return next;
    });
  };

  const submitBatchTemplate = () => {
    const template: Record<string, unknown> = {};
    const numberFields: Array<[keyof BatchQuotaTemplateForm, string]> = [
      ['quotaLimitUsd', 'quota_limit_usd'],
      ['quota5hLimitUsd', 'quota_5h_limit_usd'],
      ['quotaDailyLimitUsd', 'quota_daily_limit_usd'],
      ['quotaWeeklyLimitUsd', 'quota_weekly_limit_usd'],
      ['rateMultiplier', 'rate_multiplier'],
      ['sessionWindowLimitUsd', 'session_window_limit_usd'],
    ];
    for (const [formKey, payloadKey] of numberFields) {
      const value = optionalNumberInput(batchTemplate[formKey]);
      if (value === null) {
        toast.error('批量额度模板包含无效数字');
        return;
      }
      if (value !== undefined) {
        template[payloadKey] = value;
      }
    }
    const rpmLimit = optionalNumberInput(batchTemplate.rpmLimit);
    if (rpmLimit === null) {
      toast.error('RPM 限制必须是非负整数');
      return;
    }
    if (rpmLimit !== undefined) {
      template.rpm_limit = Math.floor(rpmLimit);
    }
    if (batchTemplate.quotaResetStrategy) {
      template.quota_reset_strategy = normalizeQuotaResetStrategy(batchTemplate.quotaResetStrategy);
    }
    if (batchTemplate.quotaTimezone.trim()) {
      template.quota_timezone = normalizeQuotaTimezone(batchTemplate.quotaTimezone);
    }
    if (Object.keys(template).length === 0) {
      toast.error('请至少填写一个额度模板字段');
      return;
    }
    batchQuotaTemplateMutation.mutate({ ids: selectedIDs, template });
  };

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-2xl font-semibold">订阅账号管理</h2>
        <div className="flex items-center gap-2">
          <OAuthBindDialog onBound={invalidate} />
          <CreateAccountDialog
            open={isCreateOpen}
            onOpenChange={setIsCreateOpen}
            onSubmit={(payload) => createMutation.mutate(payload)}
            pending={createMutation.isPending}
          />
        </div>
      </div>

      <AdminTableToolbar
        search={search}
        searchPlaceholder="按名称搜索..."
        onSearchChange={setSearch}
        onClear={clearSearch}
        actions={
          <div className="flex items-center gap-2">
            <select
              aria-label="按平台筛选"
              value={platformFilter}
              onChange={(event) => setFilter('platform', event.target.value)}
              className="h-8 rounded-md border bg-background px-2 text-sm"
            >
              <option value="">全部平台</option>
              {PLATFORM_OPTIONS.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </select>
            <select
              aria-label="按状态筛选"
              value={statusFilter}
              onChange={(event) => setFilter('status', event.target.value)}
              className="h-8 rounded-md border bg-background px-2 text-sm"
            >
              <option value="">全部状态</option>
              <option value="1">Active</option>
              <option value="2">Disabled</option>
            </select>
            <select
              aria-label="按本地额度筛选"
              value={quotaFilter}
              onChange={(event) => setFilter('quota', event.target.value)}
              className="h-8 rounded-md border bg-background px-2 text-sm"
            >
              <option value="">全部额度</option>
              <option value="exhausted">本地额度耗尽</option>
              <option value="almost">即将耗尽</option>
              <option value="no_usage">最近无用量</option>
            </select>
          </div>
        }
      />

      {selectedIDs.length > 0 && (
        <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border bg-muted/30 px-3 py-2">
          <div className="text-sm text-muted-foreground">已选择 {selectedIDs.length} 个订阅账号</div>
          <div className="flex flex-wrap items-center gap-2">
            <select
              aria-label="批量重置范围"
              value={batchResetScope}
              onChange={(event) => setBatchResetScope(event.target.value)}
              className="h-8 rounded-md border bg-background px-2 text-sm"
            >
              <option value="daily">重置 24h</option>
              <option value="weekly">重置 7d</option>
              <option value="5h">重置 5h</option>
              <option value="total">重置总额</option>
              <option value="all">重置全部</option>
            </select>
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                if (confirm(`确认批量重置 ${selectedIDs.length} 个订阅账号的用量？`)) {
                  batchResetQuotaMutation.mutate({ ids: selectedIDs, scope: batchResetScope });
                }
              }}
              disabled={batchResetQuotaMutation.isPending}
            >
              <RotateCcw className="size-3.5" />
              批量重置
            </Button>
            <Button variant="outline" size="sm" onClick={() => setIsBatchTemplateOpen(true)}>
              <Save className="size-3.5" />
              应用额度模板
            </Button>
            <Button variant="ghost" size="sm" onClick={() => setSelectedAccountIDs(new Set())}>
              取消选择
            </Button>
          </div>
        </div>
      )}

      {isLoading ? (
        <TableSkeleton columns={['ID', '名称', '平台', '分组', '优先级', '过期时间', '状态', '操作']} />
      ) : !accounts || accounts.length === 0 ? (
        <EmptyState title="暂无订阅账号" description="新建一个 Claude / Codex 订阅账号以启用混合中继。" />
      ) : visibleAccounts.length === 0 ? (
        <EmptyState title="没有匹配的订阅账号" description="清除筛选条件以查看已加载的账号。" />
      ) : (
        <>
          <div className="border rounded-lg">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-10">
                    <input
                      aria-label="选择当前页订阅账号"
                      type="checkbox"
                      checked={allVisibleSelected}
                      onChange={(event) => toggleVisibleSelected(event.target.checked)}
                    />
                  </TableHead>
                  <TableHead>ID</TableHead>
                  <SortableHeader<SubscriptionAccountSummary> columnKey="name" sort={sort} onSortChange={setSort}>
                    名称
                  </SortableHeader>
                  <SortableHeader<SubscriptionAccountSummary> columnKey="platform" sort={sort} onSortChange={setSort}>
                    平台
                  </SortableHeader>
                  <SortableHeader<SubscriptionAccountSummary> columnKey="group" sort={sort} onSortChange={setSort}>
                    分组
                  </SortableHeader>
                  <SortableHeader<SubscriptionAccountSummary> columnKey="priority" sort={sort} onSortChange={setSort} className="hidden lg:table-cell">
                    优先级
                  </SortableHeader>
                  <SortableHeader<SubscriptionAccountSummary> columnKey="expiresAt" sort={sort} onSortChange={setSort} className="hidden xl:table-cell">
                    过期时间
                  </SortableHeader>
                  <SortableHeader<SubscriptionAccountSummary> columnKey="status" sort={sort} onSortChange={setSort}>
                    状态
                  </SortableHeader>
                  <TableHead className="hidden md:table-cell">限额状态</TableHead>
                  <TableHead className="text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {visibleAccounts.map((account) => (
                  <TableRow key={account.id}>
                    <TableCell>
                      <input
                        aria-label={`选择订阅账号 ${account.name}`}
                        type="checkbox"
                        checked={selectedAccountIDs.has(account.id)}
                        onChange={(event) => toggleAccountSelected(account.id, event.target.checked)}
                      />
                    </TableCell>
                    <TableCell className="font-mono text-sm">{account.id}</TableCell>
                    <TableCell className="font-medium">{account.name}</TableCell>
                    <TableCell>{platformLabel(account.platform ?? '')}</TableCell>
                    <TableCell>{account.group}</TableCell>
                    <TableCell className="hidden lg:table-cell">{account.priority ?? 0}</TableCell>
                    <TableCell className="hidden xl:table-cell">{formatTimestamp(account.expiresAt)}</TableCell>
                    <TableCell>
                      <AccountStatusBadge
                        info={{
                          status: account.status,
                          expiresAt: account.expiresAt,
                          rateLimitedUntil: account.rateLimitedUntil,
                          quotaUsedPercent: account.quotaUsedPercent,
                          primaryQuotaUsedPercent: account.primaryQuotaUsedPercent,
                          secondaryQuotaUsedPercent: account.secondaryQuotaUsedPercent,
                          quotaSnapshotPaused: account.quotaSnapshotPaused,
                          quotaLimitUsd: account.quotaLimitUsd,
                          quotaUsedUsd: account.quotaUsedUsd,
                          quota5hLimitUsd: account.quota5hLimitUsd,
                          quota5hUsedUsd: account.quota5hUsedUsd,
                          quota5hWindowStart: account.quota5hWindowStart,
                          quotaDailyLimitUsd: account.quotaDailyLimitUsd,
                          quotaDailyUsedUsd: account.quotaDailyUsedUsd,
                          quotaDailyWindowStart: account.quotaDailyWindowStart,
                          quotaWeeklyLimitUsd: account.quotaWeeklyLimitUsd,
                          quotaWeeklyUsedUsd: account.quotaWeeklyUsedUsd,
                          quotaWeeklyWindowStart: account.quotaWeeklyWindowStart,
                          unschedulableReason: account.unschedulableReason,
                          recoveryPolicy: account.recoveryPolicy,
                          expectedRecoveryAt: account.expectedRecoveryAt,
                          unschedulableSince: account.unschedulableSince,
                        }}
                      />
                    </TableCell>
                    <TableCell className="hidden md:table-cell">
                      <QuotaStatusCell account={account} now={nowUnix} />
                    </TableCell>
                    <TableCell className="text-right space-x-2">
                      <Button variant="outline" size="sm" onClick={() => openEdit(account)}>
                        <Pencil className="size-3.5" />
                        编辑
                      </Button>
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => toggleStatusMutation.mutate({ id: account.id, currentStatus: account.status })}
                        disabled={toggleStatusMutation.isPending}
                      >
                        {account.status === 1 ? '停用' : '启用'}
                      </Button>
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => {
                          if (confirm(`确认重置订阅账号「${account.name}」的本地用量？`)) {
                            resetQuotaMutation.mutate({ id: account.id, scope: 'all' });
                          }
                        }}
                        disabled={resetQuotaMutation.isPending}
                      >
                        <RotateCcw className="size-3.5" />
                        重置
                      </Button>
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => {
                          if (confirm(`确认删除订阅账号「${account.name}」？`)) {
                            deleteMutation.mutate(account.id);
                          }
                        }}
                        disabled={deleteMutation.isPending}
                      >
                        删除
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>

          <AdminPagination
            page={page}
            pageSize={pageSize}
            hasNextPage={!!accounts && accounts.length >= pageSize}
            onPageChange={setPage}
            onPageSizeChange={setPageSize}
          />
        </>
      )}

      <EditAccountDialog
        draft={editingAccount}
        onDraftChange={setEditingAccount}
        onSubmit={() => editingAccount && updateMutation.mutate(editingAccount)}
        pending={updateMutation.isPending}
        platform={editingAccount?.accountType ? editingAccount.accountType : 'oauth'}
      />

      <Dialog open={isBatchTemplateOpen} onOpenChange={setIsBatchTemplateOpen}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>批量应用额度模板</DialogTitle>
            <DialogDescription>
              空字段不会覆盖现有账号配置；填写 0 可清空对应限额。
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-4 pt-2 sm:grid-cols-2">
            <div className="space-y-2">
              <Label htmlFor="batch-quota-limit">总额度 USD</Label>
              <Input id="batch-quota-limit" type="number" min="0" step="0.01" value={batchTemplate.quotaLimitUsd} onChange={(e) => setBatchTemplate({ ...batchTemplate, quotaLimitUsd: e.target.value })} />
            </div>
            <div className="space-y-2">
              <Label htmlFor="batch-5h-limit">5h 额度 USD</Label>
              <Input id="batch-5h-limit" type="number" min="0" step="0.01" value={batchTemplate.quota5hLimitUsd} onChange={(e) => setBatchTemplate({ ...batchTemplate, quota5hLimitUsd: e.target.value })} />
            </div>
            <div className="space-y-2">
              <Label htmlFor="batch-daily-limit">24h 额度 USD</Label>
              <Input id="batch-daily-limit" type="number" min="0" step="0.01" value={batchTemplate.quotaDailyLimitUsd} onChange={(e) => setBatchTemplate({ ...batchTemplate, quotaDailyLimitUsd: e.target.value })} />
            </div>
            <div className="space-y-2">
              <Label htmlFor="batch-weekly-limit">7d 额度 USD</Label>
              <Input id="batch-weekly-limit" type="number" min="0" step="0.01" value={batchTemplate.quotaWeeklyLimitUsd} onChange={(e) => setBatchTemplate({ ...batchTemplate, quotaWeeklyLimitUsd: e.target.value })} />
            </div>
            <div className="space-y-2">
              <Label htmlFor="batch-rate-multiplier">用量倍率</Label>
              <Input id="batch-rate-multiplier" type="number" min="0" step="0.01" value={batchTemplate.rateMultiplier} onChange={(e) => setBatchTemplate({ ...batchTemplate, rateMultiplier: e.target.value })} />
            </div>
            <div className="space-y-2">
              <Label htmlFor="batch-rpm-limit">RPM 限制</Label>
              <Input id="batch-rpm-limit" type="number" min="0" step="1" value={batchTemplate.rpmLimit} onChange={(e) => setBatchTemplate({ ...batchTemplate, rpmLimit: e.target.value })} />
            </div>
            <div className="space-y-2">
              <Label htmlFor="batch-session-window-limit">Session 额度 USD</Label>
              <Input id="batch-session-window-limit" type="number" min="0" step="0.01" value={batchTemplate.sessionWindowLimitUsd} onChange={(e) => setBatchTemplate({ ...batchTemplate, sessionWindowLimitUsd: e.target.value })} />
            </div>
            <div className="space-y-2">
              <Label htmlFor="batch-quota-reset-strategy">重置周期</Label>
              <select
                id="batch-quota-reset-strategy"
                value={batchTemplate.quotaResetStrategy}
                onChange={(e) => setBatchTemplate({ ...batchTemplate, quotaResetStrategy: e.target.value })}
                className="h-8 w-full rounded-lg border border-input bg-background px-2.5 text-sm"
              >
                <option value="">不修改</option>
                {QUOTA_RESET_STRATEGY_OPTIONS.map((option) => (
                  <option key={option.value} value={option.value}>
                    {option.label}
                  </option>
                ))}
              </select>
            </div>
            <div className="space-y-2 sm:col-span-2">
              <Label htmlFor="batch-quota-timezone">额度时区</Label>
              <Input id="batch-quota-timezone" value={batchTemplate.quotaTimezone} onChange={(e) => setBatchTemplate({ ...batchTemplate, quotaTimezone: e.target.value })} placeholder="留空不修改" />
            </div>
            <Button
              onClick={submitBatchTemplate}
              disabled={batchQuotaTemplateMutation.isPending || selectedIDs.length === 0}
              className="sm:col-span-2"
            >
              {batchQuotaTemplateMutation.isPending ? '应用中...' : `应用到 ${selectedIDs.length} 个账号`}
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}

interface CreateAccountDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSubmit: (payload: CreatePayload) => void;
  pending: boolean;
}

const emptyCreateState = {
  name: '',
  platform: 'claude',
  accountType: 'oauth',
  group: 'default',
  models: '',
  priority: '0',
  baseUrl: '',
  accessToken: '',
  refreshToken: '',
  expiresAt: '',
  accountId: '',
  fingerprint: '',
  metadata: '',
  quotaLimitUsd: '',
  quotaUsedUsd: '',
  quota5hLimitUsd: '',
  quota5hUsedUsd: '',
  quotaDailyLimitUsd: '',
  quotaDailyUsedUsd: '',
  quotaWeeklyLimitUsd: '',
  quotaWeeklyUsedUsd: '',
  rateMultiplier: '1',
  rpmLimit: '',
  sessionWindowLimitUsd: '',
  quotaResetStrategy: 'rolling',
  quotaTimezone: 'UTC',
};

function CreateAccountDialog({ open, onOpenChange, onSubmit, pending }: CreateAccountDialogProps) {
  const [form, setForm] = useState({ ...emptyCreateState });

  // When a static-key platform is selected, default account_type to
  // static_key (it marks the credential shape as a Coding-Plan key rather
  // than an OAuth refresh pair; GLM/MiniMax still get mimicry like Claude)
  // and clear the refresh-token requirement.
  const isStaticKey = STATIC_KEY_PLATFORMS.has(form.platform);

  const handleSubmit = () => {
    if (!form.name.trim() || !form.accessToken.trim()) {
      toast.error('名称、access_token 为必填项');
      return;
    }
    if (!isStaticKey && !form.refreshToken.trim()) {
      toast.error('OAuth 平台需要 refresh_token');
      return;
    }
    const accountType = isStaticKey && form.accountType === 'oauth' ? 'static_key' : form.accountType;
    onSubmit({
      name: form.name.trim(),
      platform: form.platform,
      account_type: accountType,
      group: form.group.trim(),
      models: form.models.trim(),
      priority: parseInt(form.priority || '0', 10),
      base_url: form.baseUrl.trim(),
      access_token: form.accessToken.trim(),
      refresh_token: form.refreshToken.trim(),
      expires_at: form.expiresAt ? parseInt(form.expiresAt, 10) : 0,
      account_id: form.accountId.trim(),
      fingerprint: form.fingerprint,
      metadata: form.metadata,
      quota_limit_usd: parseNumberInput(form.quotaLimitUsd),
      quota_used_usd: parseNumberInput(form.quotaUsedUsd),
      quota_5h_limit_usd: parseNumberInput(form.quota5hLimitUsd),
      quota_5h_used_usd: parseNumberInput(form.quota5hUsedUsd),
      quota_daily_limit_usd: parseNumberInput(form.quotaDailyLimitUsd),
      quota_daily_used_usd: parseNumberInput(form.quotaDailyUsedUsd),
      quota_weekly_limit_usd: parseNumberInput(form.quotaWeeklyLimitUsd),
      quota_weekly_used_usd: parseNumberInput(form.quotaWeeklyUsedUsd),
      rate_multiplier: parseNumberInput(form.rateMultiplier) || 1,
      rpm_limit: Math.max(0, parseInt(form.rpmLimit || '0', 10) || 0),
      session_window_limit_usd: parseNumberInput(form.sessionWindowLimitUsd),
      quota_reset_strategy: normalizeQuotaResetStrategy(form.quotaResetStrategy),
      quota_timezone: normalizeQuotaTimezone(form.quotaTimezone),
    });
    setForm({ ...emptyCreateState });
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger render={<Button />}>
        <KeyRound className="size-4" />
        新建订阅账号
      </DialogTrigger>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>新建订阅账号</DialogTitle>
          <DialogDescription>
            添加订阅账号（Claude / Codex / 智谱 GLM / MiniMax / Kimi），用于混合中继的身份伪装与协议转换。GLM/MiniMax/Kimi 填静态 Key 即可，Claude/Codex 走 OAuth。
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-4 pt-2 sm:grid-cols-2">
          <div className="space-y-2">
            <Label htmlFor="sub-name">名称</Label>
            <Input
              id="sub-name"
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
              placeholder="claude-pro-1"
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="sub-platform">平台</Label>
            <select
              id="sub-platform"
              value={form.platform}
              onChange={(e) => {
                const next = e.target.value;
                const patch: { platform: string; accountType?: string } = { platform: next };
                if (STATIC_KEY_PLATFORMS.has(next)) {
                  patch.accountType = 'static_key';
                } else if (next === 'claude' || next === 'codex') {
                  patch.accountType = 'oauth';
                }
                setForm({ ...form, ...patch });
              }}
              className="h-8 w-full rounded-lg border border-input bg-background px-2.5 text-sm"
            >
              {PLATFORM_OPTIONS.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-2">
            <Label htmlFor="sub-account-type">账号类型</Label>
            <select
              id="sub-account-type"
              value={form.accountType}
              onChange={(e) => setForm({ ...form, accountType: e.target.value })}
              className="h-8 w-full rounded-lg border border-input bg-background px-2.5 text-sm"
            >
              {ACCOUNT_TYPE_OPTIONS.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-2">
            <Label htmlFor="sub-group">分组</Label>
            <Input
              id="sub-group"
              value={form.group}
              onChange={(e) => setForm({ ...form, group: e.target.value })}
            />
          </div>
          <div className="space-y-2 sm:col-span-2">
            <Label htmlFor="sub-models">模型（逗号分隔）</Label>
            <Input
              id="sub-models"
              value={form.models}
              onChange={(e) => setForm({ ...form, models: e.target.value })}
              placeholder="claude-sonnet-4-5,claude-opus-4-1"
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="sub-priority">优先级</Label>
            <Input
              id="sub-priority"
              type="number"
              value={form.priority}
              onChange={(e) => setForm({ ...form, priority: e.target.value })}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="sub-expires">过期时间（Unix 秒）</Label>
            <Input
              id="sub-expires"
              type="number"
              value={form.expiresAt}
              onChange={(e) => setForm({ ...form, expiresAt: e.target.value })}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="sub-quota-limit">总额度 USD</Label>
            <Input
              id="sub-quota-limit"
              type="number"
              min="0"
              step="0.01"
              value={form.quotaLimitUsd}
              onChange={(e) => setForm({ ...form, quotaLimitUsd: e.target.value })}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="sub-5h-limit">5h 额度 USD</Label>
            <Input
              id="sub-5h-limit"
              type="number"
              min="0"
              step="0.01"
              value={form.quota5hLimitUsd}
              onChange={(e) => setForm({ ...form, quota5hLimitUsd: e.target.value })}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="sub-daily-limit">24h 额度 USD</Label>
            <Input
              id="sub-daily-limit"
              type="number"
              min="0"
              step="0.01"
              value={form.quotaDailyLimitUsd}
              onChange={(e) => setForm({ ...form, quotaDailyLimitUsd: e.target.value })}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="sub-weekly-limit">7d 额度 USD</Label>
            <Input
              id="sub-weekly-limit"
              type="number"
              min="0"
              step="0.01"
              value={form.quotaWeeklyLimitUsd}
              onChange={(e) => setForm({ ...form, quotaWeeklyLimitUsd: e.target.value })}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="sub-rate-multiplier">用量倍率</Label>
            <Input
              id="sub-rate-multiplier"
              type="number"
              min="0"
              step="0.01"
              value={form.rateMultiplier}
              onChange={(e) => setForm({ ...form, rateMultiplier: e.target.value })}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="sub-rpm-limit">RPM 限制</Label>
            <Input
              id="sub-rpm-limit"
              type="number"
              min="0"
              step="1"
              value={form.rpmLimit}
              onChange={(e) => setForm({ ...form, rpmLimit: e.target.value })}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="sub-session-window-limit">Session 额度 USD</Label>
            <Input
              id="sub-session-window-limit"
              type="number"
              min="0"
              step="0.01"
              value={form.sessionWindowLimitUsd}
              onChange={(e) => setForm({ ...form, sessionWindowLimitUsd: e.target.value })}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="sub-quota-reset-strategy">重置周期</Label>
            <select
              id="sub-quota-reset-strategy"
              value={form.quotaResetStrategy}
              onChange={(e) => setForm({ ...form, quotaResetStrategy: e.target.value })}
              className="h-8 w-full rounded-lg border border-input bg-background px-2.5 text-sm"
            >
              {QUOTA_RESET_STRATEGY_OPTIONS.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-2">
            <Label htmlFor="sub-quota-timezone">额度时区</Label>
            <Input
              id="sub-quota-timezone"
              value={form.quotaTimezone}
              onChange={(e) => setForm({ ...form, quotaTimezone: e.target.value })}
              placeholder="UTC"
            />
          </div>
          <div className="space-y-2 sm:col-span-2">
            <Label htmlFor="sub-base-url">Base URL（可选，留空走默认上游）</Label>
            <Input
              id="sub-base-url"
              value={form.baseUrl}
              onChange={(e) => setForm({ ...form, baseUrl: e.target.value })}
              placeholder="https://api.anthropic.com"
            />
          </div>
          <div className="space-y-2 sm:col-span-2">
            <Label htmlFor="sub-access-token">
              {isStaticKey ? 'Coding Plan Key（即 Access Token）' : 'Access Token'}
            </Label>
            <Input
              id="sub-access-token"
              type="password"
              value={form.accessToken}
              onChange={(e) => setForm({ ...form, accessToken: e.target.value })}
              placeholder={isStaticKey ? 'GLM/MiniMax Coding Plan Key' : 'sk-ant-...'}
            />
          </div>
          <div className="space-y-2 sm:col-span-2">
            <Label htmlFor="sub-refresh-token">
              Refresh Token{isStaticKey ? '（静态 Key 平台无需填写）' : ''}
            </Label>
            <Input
              id="sub-refresh-token"
              type="password"
              value={form.refreshToken}
              onChange={(e) => setForm({ ...form, refreshToken: e.target.value })}
              placeholder={isStaticKey ? 'GLM/MiniMax 留空' : 'sk-ant-oat-...'}
              disabled={isStaticKey}
            />
          </div>
          <div className="space-y-2 sm:col-span-2">
            <Label htmlFor="sub-account-id">上游 Account ID（可选）</Label>
            <Input
              id="sub-account-id"
              value={form.accountId}
              onChange={(e) => setForm({ ...form, accountId: e.target.value })}
              placeholder="chatgpt-account-id / claude account uuid"
            />
          </div>
          <div className="space-y-2 sm:col-span-2">
            <Label htmlFor="sub-fingerprint">指纹（JSON，可选）</Label>
            <Input
              id="sub-fingerprint"
              value={form.fingerprint}
              onChange={(e) => setForm({ ...form, fingerprint: e.target.value })}
              placeholder='{"user_agent":"..."}'
            />
          </div>
          <div className="space-y-2 sm:col-span-2">
            <Label htmlFor="sub-metadata">Metadata（JSON，可选）</Label>
            <Input
              id="sub-metadata"
              value={form.metadata}
              onChange={(e) => setForm({ ...form, metadata: e.target.value })}
            />
          </div>
          <Button onClick={handleSubmit} disabled={pending} className="sm:col-span-2">
            {pending ? '创建中...' : '创建'}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

interface EditAccountDialogProps {
  draft: SubscriptionAccountEditDraft | null;
  onDraftChange: (draft: SubscriptionAccountEditDraft | null) => void;
  onSubmit: () => void;
  pending: boolean;
  platform: string;
}

function EditAccountDialog({ draft, onDraftChange, onSubmit, pending }: EditAccountDialogProps) {
  const handleUpdate = () => {
    if (!draft) return;
    if (!draft.name.trim() || !draft.group.trim() || !draft.models.trim()) {
      toast.error('名称、模型、分组为必填项');
      return;
    }
    onSubmit();
  };

  return (
    <Dialog open={!!draft} onOpenChange={(open) => !open && onDraftChange(null)}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>编辑订阅账号</DialogTitle>
          <DialogDescription>
            修改路由与凭证配置。access_token / refresh_token 留空则保留原值（服务端已脱敏，无法回显）。
          </DialogDescription>
        </DialogHeader>
        {draft && (
          <div className="grid gap-4 pt-2 sm:grid-cols-2">
            <div className="space-y-2 sm:col-span-2">
              <Label htmlFor="edit-sub-name">名称</Label>
              <Input
                id="edit-sub-name"
                value={draft.name}
                onChange={(e) => onDraftChange({ ...draft, name: e.target.value })}
              />
            </div>
            <div className="space-y-2 sm:col-span-2">
              <Label htmlFor="edit-sub-models">模型（逗号分隔）</Label>
              <Input
                id="edit-sub-models"
                value={draft.models}
                onChange={(e) => onDraftChange({ ...draft, models: e.target.value })}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="edit-sub-group">分组</Label>
              <Input
                id="edit-sub-group"
                value={draft.group}
                onChange={(e) => onDraftChange({ ...draft, group: e.target.value })}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="edit-sub-priority">优先级</Label>
              <Input
                id="edit-sub-priority"
                type="number"
                value={draft.priority}
                onChange={(e) => onDraftChange({ ...draft, priority: e.target.value })}
              />
            </div>
            <div className="space-y-2 sm:col-span-2">
              <Label htmlFor="edit-sub-base-url">Base URL</Label>
              <Input
                id="edit-sub-base-url"
                value={draft.baseUrl}
                onChange={(e) => onDraftChange({ ...draft, baseUrl: e.target.value })}
              />
            </div>
            <div className="space-y-2 sm:col-span-2">
              <Label htmlFor="edit-sub-access-token">Access Token（留空保留原值）</Label>
              <Input
                id="edit-sub-access-token"
                type="password"
                value={draft.accessToken}
                onChange={(e) => onDraftChange({ ...draft, accessToken: e.target.value })}
              />
            </div>
            <div className="space-y-2 sm:col-span-2">
              <Label htmlFor="edit-sub-refresh-token">Refresh Token（留空保留原值）</Label>
              <Input
                id="edit-sub-refresh-token"
                type="password"
                value={draft.refreshToken}
                onChange={(e) => onDraftChange({ ...draft, refreshToken: e.target.value })}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="edit-sub-expires">过期时间（Unix 秒）</Label>
              <Input
                id="edit-sub-expires"
                type="number"
                value={draft.expiresAt}
                onChange={(e) => onDraftChange({ ...draft, expiresAt: e.target.value })}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="edit-sub-account-id">上游 Account ID</Label>
              <Input
                id="edit-sub-account-id"
                value={draft.accountId}
                onChange={(e) => onDraftChange({ ...draft, accountId: e.target.value })}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="edit-sub-quota-limit">总额度 USD</Label>
              <Input
                id="edit-sub-quota-limit"
                type="number"
                min="0"
                step="0.01"
                value={draft.quotaLimitUsd}
                onChange={(e) => onDraftChange({ ...draft, quotaLimitUsd: e.target.value })}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="edit-sub-quota-used">总已用 USD</Label>
              <Input
                id="edit-sub-quota-used"
                type="number"
                min="0"
                step="0.01"
                value={draft.quotaUsedUsd}
                onChange={(e) => onDraftChange({ ...draft, quotaUsedUsd: e.target.value })}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="edit-sub-5h-limit">5h 额度 USD</Label>
              <Input
                id="edit-sub-5h-limit"
                type="number"
                min="0"
                step="0.01"
                value={draft.quota5hLimitUsd}
                onChange={(e) => onDraftChange({ ...draft, quota5hLimitUsd: e.target.value })}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="edit-sub-5h-used">5h 已用 USD</Label>
              <Input
                id="edit-sub-5h-used"
                type="number"
                min="0"
                step="0.01"
                value={draft.quota5hUsedUsd}
                onChange={(e) => onDraftChange({ ...draft, quota5hUsedUsd: e.target.value })}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="edit-sub-daily-limit">24h 额度 USD</Label>
              <Input
                id="edit-sub-daily-limit"
                type="number"
                min="0"
                step="0.01"
                value={draft.quotaDailyLimitUsd}
                onChange={(e) => onDraftChange({ ...draft, quotaDailyLimitUsd: e.target.value })}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="edit-sub-daily-used">24h 已用 USD</Label>
              <Input
                id="edit-sub-daily-used"
                type="number"
                min="0"
                step="0.01"
                value={draft.quotaDailyUsedUsd}
                onChange={(e) => onDraftChange({ ...draft, quotaDailyUsedUsd: e.target.value })}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="edit-sub-weekly-limit">7d 额度 USD</Label>
              <Input
                id="edit-sub-weekly-limit"
                type="number"
                min="0"
                step="0.01"
                value={draft.quotaWeeklyLimitUsd}
                onChange={(e) => onDraftChange({ ...draft, quotaWeeklyLimitUsd: e.target.value })}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="edit-sub-weekly-used">7d 已用 USD</Label>
              <Input
                id="edit-sub-weekly-used"
                type="number"
                min="0"
                step="0.01"
                value={draft.quotaWeeklyUsedUsd}
                onChange={(e) => onDraftChange({ ...draft, quotaWeeklyUsedUsd: e.target.value })}
              />
            </div>
            <div className="space-y-2 sm:col-span-2">
              <Label htmlFor="edit-sub-rate-multiplier">用量倍率</Label>
              <Input
                id="edit-sub-rate-multiplier"
                type="number"
                min="0"
                step="0.01"
                value={draft.rateMultiplier}
                onChange={(e) => onDraftChange({ ...draft, rateMultiplier: e.target.value })}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="edit-sub-rpm-limit">RPM 限制</Label>
              <Input
                id="edit-sub-rpm-limit"
                type="number"
                min="0"
                step="1"
                value={draft.rpmLimit}
                onChange={(e) => onDraftChange({ ...draft, rpmLimit: e.target.value })}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="edit-sub-session-window-limit">Session 额度 USD</Label>
              <Input
                id="edit-sub-session-window-limit"
                type="number"
                min="0"
                step="0.01"
                value={draft.sessionWindowLimitUsd}
                onChange={(e) => onDraftChange({ ...draft, sessionWindowLimitUsd: e.target.value })}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="edit-sub-quota-reset-strategy">重置周期</Label>
              <select
                id="edit-sub-quota-reset-strategy"
                value={draft.quotaResetStrategy}
                onChange={(e) => onDraftChange({ ...draft, quotaResetStrategy: e.target.value })}
                className="h-8 w-full rounded-lg border border-input bg-background px-2.5 text-sm"
              >
                {QUOTA_RESET_STRATEGY_OPTIONS.map((option) => (
                  <option key={option.value} value={option.value}>
                    {option.label}
                  </option>
                ))}
              </select>
            </div>
            <div className="space-y-2">
              <Label htmlFor="edit-sub-quota-timezone">额度时区</Label>
              <Input
                id="edit-sub-quota-timezone"
                value={draft.quotaTimezone}
                onChange={(e) => onDraftChange({ ...draft, quotaTimezone: e.target.value })}
                placeholder="UTC"
              />
            </div>
            <div className="space-y-2 sm:col-span-2">
              <Label htmlFor="edit-sub-fingerprint">指纹（JSON）</Label>
              <Input
                id="edit-sub-fingerprint"
                value={draft.fingerprint}
                onChange={(e) => onDraftChange({ ...draft, fingerprint: e.target.value })}
              />
            </div>
            <div className="space-y-2 sm:col-span-2">
              <Label htmlFor="edit-sub-metadata">Metadata（JSON）</Label>
              <Input
                id="edit-sub-metadata"
                value={draft.metadata}
                onChange={(e) => onDraftChange({ ...draft, metadata: e.target.value })}
              />
            </div>
            <Button
              onClick={handleUpdate}
              disabled={pending || !draft.name.trim() || !draft.models.trim() || !draft.group.trim()}
              className="sm:col-span-2"
            >
              <Save className="size-4" />
              {pending ? '保存中...' : '保存配置'}
            </Button>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
