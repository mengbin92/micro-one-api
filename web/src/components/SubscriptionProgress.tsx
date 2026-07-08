import { Infinity as InfinityIcon } from 'lucide-react';
import { cn } from '@/lib/utils';

// Mirrors subscription.biz.QuotaDimension JSON tags returned inside
// SubscriptionProgress (GET /api/v1/subscriptions/progress). `limit` is a
// nullable *float64 on the backend: null means the dimension is unlimited.
export interface QuotaDimension {
  used: number;
  limit: number | null;
  remaining: number;
  // Unix epoch seconds at which this window resets and usage rolls back to
  // zero. Mirrors QuotaDimension.NextRefresh on the backend. Optional so old
  // cached responses (pre-field) still type-check.
  next_refresh?: number;
}

// Mirrors subscription.biz.SubscriptionProgress JSON tags. The endpoint returns
// the single active subscription for a user (or success:false when none).
export interface SubscriptionProgressData {
  id: number;
  status: string;
  starts_at: number;
  expires_at: number;
  group_id?: number;
  subscription_name?: string;
  daily_used: QuotaDimension | null;
  weekly_used: QuotaDimension | null;
  monthly_used: QuotaDimension | null;
  remaining_seconds: number;
}

function formatUsd(value: number) {
  const safeValue = Number.isFinite(value) ? value : 0;
  const digits = Math.abs(safeValue) > 0 && Math.abs(safeValue) < 1 ? 8 : 2;
  const fixed = safeValue.toFixed(digits);
  const [whole, fraction = ''] = fixed.split('.');
  const trimmed = fraction.replace(/0+$/, '');
  if (trimmed.length <= 2) return `$${whole}.${fraction.slice(0, 2)}`;
  return `$${whole}.${trimmed}`;
}

function usageRatio(used: number, limit: number | null) {
  if (limit == null || limit <= 0) return 0;
  return Math.min(used / limit, 1);
}

// Green under 70%, amber under 90%, red at/above — matches the traffic-light
// convention used by sub2api's progress bars.
function barColorClass(ratio: number) {
  if (ratio >= 0.9) return 'bg-red-500';
  if (ratio >= 0.7) return 'bg-amber-500';
  return 'bg-emerald-500';
}

function formatNextRefresh(ts?: number): string | null {
  if (!ts || ts <= 0) return null;
  const diffMs = ts * 1000 - Date.now();
  if (diffMs <= 0) return null;
  const days = Math.floor(diffMs / (1000 * 60 * 60 * 24));
  const hours = Math.floor((diffMs % (1000 * 60 * 60 * 24)) / (1000 * 60 * 60));
  const minutes = Math.floor((diffMs % (1000 * 60 * 60)) / (1000 * 60));
  if (days >= 1) return `${days}天${hours}h后刷新`;
  if (hours >= 1) return `${hours}h${minutes}m后刷新`;
  return `${minutes}m后刷新`;
}

function QuotaBar({ label, dimension }: { label: string; dimension: QuotaDimension | null }) {
  if (!dimension) return null;
  const unlimited = dimension.limit == null;
  const ratio = usageRatio(dimension.used, dimension.limit);
  const refreshLabel = formatNextRefresh(dimension.next_refresh);

  return (
    <div className="flex items-center gap-3">
      <span className="w-10 shrink-0 text-xs font-medium text-muted-foreground">{label}</span>
      {unlimited ? (
        <div className="flex flex-1 items-center gap-1.5 rounded-full bg-emerald-50 px-2.5 py-1 text-xs font-medium text-emerald-700 dark:bg-emerald-900/20 dark:text-emerald-300">
          <InfinityIcon className="size-3.5" />
          无限制
        </div>
      ) : (
        <>
          <div className="h-2 min-w-0 flex-1 overflow-hidden rounded-full bg-muted">
            <div
              className={cn('h-2 rounded-full transition-all', barColorClass(ratio))}
              style={{ width: `${Math.round(ratio * 100)}%` }}
            />
          </div>
          <span className="w-36 shrink-0 whitespace-nowrap text-right text-xs text-muted-foreground">
            {formatUsd(dimension.used)} / {formatUsd(dimension.limit ?? 0)}
          </span>
        </>
      )}
      {refreshLabel && (
        <span className="w-24 shrink-0 whitespace-nowrap text-right text-[10px] text-muted-foreground/70">
          {refreshLabel}
        </span>
      )}
    </div>
  );
}

function formatRemaining(seconds: number) {
  if (seconds <= 0) return '已过期';
  const days = Math.floor(seconds / 86400);
  if (days >= 1) return `剩余 ${days} 天`;
  const hours = Math.floor(seconds / 3600);
  if (hours >= 1) return `剩余 ${hours} 小时`;
  const minutes = Math.floor(seconds / 60);
  return `剩余 ${minutes} 分钟`;
}

function statusBadgeClass(status: string) {
  switch (status) {
    case 'active':
      return 'bg-emerald-100 text-emerald-800 dark:bg-emerald-900 dark:text-emerald-200';
    case 'revoked':
      return 'bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-200';
    default: // expired / Expired
      return 'bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-300';
  }
}

interface SubscriptionProgressCardProps {
  progress: SubscriptionProgressData;
  title?: string;
  className?: string;
}

/**
 * Presentational card for a single subscription's daily/weekly/monthly USD quota
 * plus its expiry countdown. Shared by the admin user-subscription page and the
 * user-facing "我的订阅" page so both render quota identically.
 */
export function SubscriptionProgressCard({ progress, title, className }: SubscriptionProgressCardProps) {
  const expiresLabel = progress.expires_at
    ? new Date(progress.expires_at * 1000).toLocaleString()
    : '—';

  return (
    <div className={cn('rounded-xl border bg-card p-4 shadow-sm', className)}>
      <div className="mb-3 flex items-center justify-between gap-2">
        <div className="min-w-0">
          <p className="truncate text-sm font-semibold">{title ?? `订阅 #${progress.id}`}</p>
          <p className="text-xs text-muted-foreground">到期：{expiresLabel}</p>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <span
            className={cn(
              'inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium',
              statusBadgeClass(progress.status)
            )}
          >
            {progress.status}
          </span>
          <span className="text-xs font-medium text-muted-foreground">
            {formatRemaining(progress.remaining_seconds)}
          </span>
        </div>
      </div>
      <div className="space-y-2">
        <QuotaBar label="日" dimension={progress.daily_used} />
        <QuotaBar label="周" dimension={progress.weekly_used} />
        <QuotaBar label="月" dimension={progress.monthly_used} />
      </div>
    </div>
  );
}
