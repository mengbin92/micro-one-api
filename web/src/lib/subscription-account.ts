/**
 * SubscriptionAccountSummary — normalized shape of
 * common.v1.SubscriptionAccountSummary as returned by
 * GET /api/subscription-accounts (alias of /v1/subscription-accounts).
 *
 * The protobuf-JSON encoder emits snake_case keys while some legacy
 * endpoints emit camelCase, and different admin pages historically declared
 * dual camelCase/snake_case interfaces with `a ?? b` fallback chains at
 * every read site. That pattern does not scale: every new field had to be
 * written twice per page.
 *
 * Instead, call sites normalize the raw payload ONCE with
 * `normalizeSubscriptionAccount()` and read only the camelCase shape below.
 */

export interface SubscriptionAccountSummary {
  id: number;
  name?: string;
  platform?: string;
  accountType?: string;
  status: number;
  group?: string;
  models?: string;
  priority?: number;
  accountId?: string;
  expiresAt?: number;
  updatedAt?: number;
  lastUsedAt?: number;
  rateLimitedUntil?: number;
  quotaUsedPercent?: number;
  quotaResetAt?: number;
  primaryQuotaUsedPercent?: number | null;
  primaryQuotaResetAfterSeconds?: number | null;
  primaryQuotaWindowMinutes?: number | null;
  secondaryQuotaUsedPercent?: number | null;
  secondaryQuotaResetAfterSeconds?: number | null;
  secondaryQuotaWindowMinutes?: number | null;
  primaryOverSecondaryPercent?: number | null;
  quotaSnapshotUpdatedAt?: number;
  quotaSnapshotPaused?: boolean;
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
  rateMultiplier?: number;
  rpmLimit?: number;
  sessionWindowLimitUsd?: number;
  quotaResetStrategy?: string;
  quotaTimezone?: string;
  unschedulableReason?: string;
  recoveryPolicy?: string;
  expectedRecoveryAt?: number;
  unschedulableSince?: number;
}

/** Raw payload shape: camelCase and/or snake_case keys. */
export type RawSubscriptionAccount = Partial<
  SubscriptionAccountSummary & {
    account_type: string;
    account_id: string;
    expires_at: number;
    updated_at: number;
    last_used_at: number;
    rate_limited_until: number;
    quota_used_percent: number;
    quota_reset_at: number;
    primary_quota_used_percent: number | null;
    primary_quota_reset_after_seconds: number | null;
    primary_quota_window_minutes: number | null;
    secondary_quota_used_percent: number | null;
    secondary_quota_reset_after_seconds: number | null;
    secondary_quota_window_minutes: number | null;
    primary_over_secondary_percent: number | null;
    quota_snapshot_updated_at: number;
    quota_snapshot_paused: boolean;
    quota_limit_usd: number;
    quota_used_usd: number;
    quota_5h_limit_usd: number;
    quota_5h_used_usd: number;
    quota_5h_window_start: number;
    quota_daily_limit_usd: number;
    quota_daily_used_usd: number;
    quota_daily_window_start: number;
    quota_weekly_limit_usd: number;
    quota_weekly_used_usd: number;
    quota_weekly_window_start: number;
    rpm_limit: number;
    session_window_limit_usd: number;
    quota_reset_strategy: string;
    quota_timezone: string;
    unschedulable_reason: string;
    recovery_policy: string;
    expected_recovery_at: number;
    unschedulable_since: number;
  }
>;

/**
 * Collapse the camelCase/snake_case duality of the wire payload into the
 * normalized camelCase SubscriptionAccountSummary shape.
 */
export function normalizeSubscriptionAccount(raw: RawSubscriptionAccount): SubscriptionAccountSummary {
  return {
    id: raw.id ?? 0,
    name: raw.name,
    platform: raw.platform,
    accountType: raw.accountType ?? raw.account_type,
    status: raw.status ?? 0,
    group: raw.group,
    models: raw.models,
    priority: raw.priority,
    accountId: raw.accountId ?? raw.account_id,
    expiresAt: raw.expiresAt ?? raw.expires_at,
    updatedAt: raw.updatedAt ?? raw.updated_at,
    lastUsedAt: raw.lastUsedAt ?? raw.last_used_at,
    rateLimitedUntil: raw.rateLimitedUntil ?? raw.rate_limited_until,
    quotaUsedPercent: raw.quotaUsedPercent ?? raw.quota_used_percent,
    quotaResetAt: raw.quotaResetAt ?? raw.quota_reset_at,
    primaryQuotaUsedPercent: raw.primaryQuotaUsedPercent ?? raw.primary_quota_used_percent,
    primaryQuotaResetAfterSeconds: raw.primaryQuotaResetAfterSeconds ?? raw.primary_quota_reset_after_seconds,
    primaryQuotaWindowMinutes: raw.primaryQuotaWindowMinutes ?? raw.primary_quota_window_minutes,
    secondaryQuotaUsedPercent: raw.secondaryQuotaUsedPercent ?? raw.secondary_quota_used_percent,
    secondaryQuotaResetAfterSeconds: raw.secondaryQuotaResetAfterSeconds ?? raw.secondary_quota_reset_after_seconds,
    secondaryQuotaWindowMinutes: raw.secondaryQuotaWindowMinutes ?? raw.secondary_quota_window_minutes,
    primaryOverSecondaryPercent: raw.primaryOverSecondaryPercent ?? raw.primary_over_secondary_percent,
    quotaSnapshotUpdatedAt: raw.quotaSnapshotUpdatedAt ?? raw.quota_snapshot_updated_at,
    quotaSnapshotPaused: raw.quotaSnapshotPaused ?? raw.quota_snapshot_paused,
    quotaLimitUsd: raw.quotaLimitUsd ?? raw.quota_limit_usd,
    quotaUsedUsd: raw.quotaUsedUsd ?? raw.quota_used_usd,
    quota5hLimitUsd: raw.quota5hLimitUsd ?? raw.quota_5h_limit_usd,
    quota5hUsedUsd: raw.quota5hUsedUsd ?? raw.quota_5h_used_usd,
    quota5hWindowStart: raw.quota5hWindowStart ?? raw.quota_5h_window_start,
    quotaDailyLimitUsd: raw.quotaDailyLimitUsd ?? raw.quota_daily_limit_usd,
    quotaDailyUsedUsd: raw.quotaDailyUsedUsd ?? raw.quota_daily_used_usd,
    quotaDailyWindowStart: raw.quotaDailyWindowStart ?? raw.quota_daily_window_start,
    quotaWeeklyLimitUsd: raw.quotaWeeklyLimitUsd ?? raw.quota_weekly_limit_usd,
    quotaWeeklyUsedUsd: raw.quotaWeeklyUsedUsd ?? raw.quota_weekly_used_usd,
    quotaWeeklyWindowStart: raw.quotaWeeklyWindowStart ?? raw.quota_weekly_window_start,
    rateMultiplier: raw.rateMultiplier,
    rpmLimit: raw.rpmLimit ?? raw.rpm_limit,
    sessionWindowLimitUsd: raw.sessionWindowLimitUsd ?? raw.session_window_limit_usd,
    quotaResetStrategy: raw.quotaResetStrategy ?? raw.quota_reset_strategy,
    quotaTimezone: raw.quotaTimezone ?? raw.quota_timezone,
    unschedulableReason: raw.unschedulableReason ?? raw.unschedulable_reason,
    recoveryPolicy: raw.recoveryPolicy ?? raw.recovery_policy,
    expectedRecoveryAt: raw.expectedRecoveryAt ?? raw.expected_recovery_at,
    unschedulableSince: raw.unschedulableSince ?? raw.unschedulable_since,
  };
}
