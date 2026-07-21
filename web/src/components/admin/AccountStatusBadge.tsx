import { cn } from '@/lib/utils';

/**
 * Composite status badge for subscription accounts.
 *
 * The backend (common.v1.SubscriptionAccountSummary) already returns the full
 * set of scheduling + quota + token-health fields. This badge derives a single
 * human-readable status from them, following the priority order used by
 * sub2api's AccountStatusIndicator.vue:
 *
 *   disabled → token_expired → token_expiring → rate_limited
 *     → quota_exceeded → unschedulable → snapshot_paused → active
 *
 * Each state maps to a tailwind colour pair (bg + text, light + dark).
 */

export interface AccountStatusInfo {
  status: number; // 1=enabled, 2=disabled
  expiresAt?: number;
  rateLimitedUntil?: number;
  // Upstream quota snapshot
  quotaUsedPercent?: number;
  primaryQuotaUsedPercent?: number | null;
  secondaryQuotaUsedPercent?: number | null;
  quotaSnapshotPaused?: boolean;
  // Local quota (USD windows)
  quotaLimitUsd?: number;
  quotaUsedUsd?: number;
  quota5hLimitUsd?: number;
  quota5hUsedUsd?: number;
  quota5hWindowStart?: number;
  quotaDailyLimitUsd?: number;
  quotaDailyUsedUsd?: number;
  quotaDailyWindowStart?: number;
  quotaWeeklyLimitUsd?: number;
  quotaWeeklyUsedUsd?: number;
  quotaWeeklyWindowStart?: number;
  // Scheduling / recovery
  unschedulableReason?: string;
  recoveryPolicy?: string;
  expectedRecoveryAt?: number;
  unschedulableSince?: number;
}

type StateKind =
  | 'disabled'
  | 'token_expired'
  | 'token_expiring'
  | 'rate_limited'
  | 'quota_exceeded'
  | 'unschedulable'
  | 'snapshot_paused'
  | 'active';

interface StateMeta {
  label: string;
  badgeClass: string;
  tooltip?: string;
}

const STATE_STYLES: Record<StateKind, Omit<StateMeta, 'tooltip'>> = {
  disabled: {
    label: '已禁用',
    badgeClass: 'bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-300',
  },
  token_expired: {
    label: 'Token 已过期',
    badgeClass: 'bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-200',
  },
  token_expiring: {
    label: 'Token 即将过期',
    badgeClass: 'bg-amber-100 text-amber-800 dark:bg-amber-900 dark:text-amber-200',
  },
  rate_limited: {
    label: '已被限流',
    badgeClass: 'bg-orange-100 text-orange-800 dark:bg-orange-900 dark:text-orange-200',
  },
  quota_exceeded: {
    label: '额度已耗尽',
    badgeClass: 'bg-orange-100 text-orange-800 dark:bg-orange-900 dark:text-orange-200',
  },
  unschedulable: {
    label: '不可调度',
    badgeClass: 'bg-amber-100 text-amber-800 dark:bg-amber-900 dark:text-amber-200',
  },
  snapshot_paused: {
    label: '已暂停采样',
    badgeClass: 'bg-amber-100 text-amber-800 dark:bg-amber-900 dark:text-amber-200',
  },
  active: {
    label: '正常',
    badgeClass: 'bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200',
  },
};

const HOUR_S = 3600;
const DAY_S = 86400;
const WEEK_S = 7 * DAY_S;
const FIVE_H_S = 5 * HOUR_S;

function effectiveWindowUsed(used: number, windowStart: number, nowUnix: number, windowS: number): number {
  if (windowStart <= 0 || nowUnix - windowStart >= windowS) return 0;
  return used;
}

function localQuotaExceeded(info: AccountStatusInfo, nowUnix: number): boolean {
  if (info.quotaLimitUsd && info.quotaLimitUsd > 0 && (info.quotaUsedUsd ?? 0) >= info.quotaLimitUsd) return true;
  if (info.quota5hLimitUsd && info.quota5hLimitUsd > 0) {
    const used = effectiveWindowUsed(info.quota5hUsedUsd ?? 0, info.quota5hWindowStart ?? 0, nowUnix, FIVE_H_S);
    if (used >= info.quota5hLimitUsd) return true;
  }
  if (info.quotaDailyLimitUsd && info.quotaDailyLimitUsd > 0) {
    const used = effectiveWindowUsed(info.quotaDailyUsedUsd ?? 0, info.quotaDailyWindowStart ?? 0, nowUnix, DAY_S);
    if (used >= info.quotaDailyLimitUsd) return true;
  }
  if (info.quotaWeeklyLimitUsd && info.quotaWeeklyLimitUsd > 0) {
    const used = effectiveWindowUsed(info.quotaWeeklyUsedUsd ?? 0, info.quotaWeeklyWindowStart ?? 0, nowUnix, WEEK_S);
    if (used >= info.quotaWeeklyLimitUsd) return true;
  }
  return false;
}

function upstreamQuotaExceeded(info: AccountStatusInfo): boolean {
  if (info.primaryQuotaUsedPercent != null && info.primaryQuotaUsedPercent >= 100) return true;
  if (info.secondaryQuotaUsedPercent != null && info.secondaryQuotaUsedPercent >= 100) return true;
  if (info.quotaUsedPercent != null && info.quotaUsedPercent >= 100) return true;
  return false;
}

function formatCountdown(targetUnix: number, nowUnix: number): string {
  const diff = targetUnix - nowUnix;
  if (diff <= 0) return '即将恢复';
  const days = Math.floor(diff / DAY_S);
  const hours = Math.floor((diff % DAY_S) / HOUR_S);
  const minutes = Math.floor((diff % HOUR_S) / 60);
  if (days > 0) return `${days}天${hours}h后恢复`;
  if (hours > 0) return `${hours}h${minutes}m后恢复`;
  if (minutes > 0) return `${minutes}m后恢复`;
  return '即将恢复';
}

export function deriveAccountStatus(info: AccountStatusInfo, nowUnix: number): StateKind {
  if (info.status !== 1) return 'disabled';

  // Token expiry
  if (info.expiresAt && info.expiresAt > 0 && info.expiresAt <= nowUnix) return 'token_expired';
  if (info.expiresAt && info.expiresAt > nowUnix && info.expiresAt - nowUnix < HOUR_S) return 'token_expiring';

  // Rate limited
  if (info.rateLimitedUntil && info.rateLimitedUntil > nowUnix) return 'rate_limited';

  // Quota exceeded (local or upstream)
  if (localQuotaExceeded(info, nowUnix) || upstreamQuotaExceeded(info)) return 'quota_exceeded';

  // Unschedulable (has reason + since)
  if (info.unschedulableReason && info.unschedulableSince && info.unschedulableSince > 0) return 'unschedulable';

  // Snapshot paused
  if (info.quotaSnapshotPaused) return 'snapshot_paused';

  return 'active';
}

export function AccountStatusBadge({
  info,
  now,
  className,
}: {
  info: AccountStatusInfo;
  now?: number; // unix seconds; defaults to Date.now()
  className?: string;
}) {
  const nowUnix = now ?? Math.floor(Date.now() / 1000);
  const kind = deriveAccountStatus(info, nowUnix);
  const meta = STATE_STYLES[kind];

  // Build tooltip for states that have extra context
  let tooltip: string | undefined;
  if (kind === 'rate_limited' && info.rateLimitedUntil) {
    tooltip = formatCountdown(info.rateLimitedUntil, nowUnix);
  } else if (kind === 'unschedulable') {
    const parts: string[] = [info.unschedulableReason ?? '不可调度'];
    if (info.recoveryPolicy) parts.push(`策略: ${info.recoveryPolicy}`);
    if (info.expectedRecoveryAt && info.expectedRecoveryAt > 0) parts.push(formatCountdown(info.expectedRecoveryAt, nowUnix));
    tooltip = parts.join(' · ');
  } else if (kind === 'token_expiring' && info.expiresAt) {
    tooltip = formatCountdown(info.expiresAt, nowUnix);
  }

  return (
    <span
      className={cn('inline-flex items-center rounded-full px-2 py-1 text-xs font-medium', meta.badgeClass, className)}
      title={tooltip}
    >
      {meta.label}
    </span>
  );
}
