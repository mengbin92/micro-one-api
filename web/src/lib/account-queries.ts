import { queryOptions } from '@tanstack/react-query';
import { apiClient } from '@/lib/api';
import { unwrapApiData } from '@/lib/api-response';
import type { SubscriptionProgressData } from '@/components/SubscriptionProgress';

export interface UsageSummaryItem {
  date?: string;
  day?: string;
  count: number;
  amount: number;
  prompt_tokens?: number;
  completion_tokens?: number;
  cache_read_tokens?: number;
  // Canonical billing buckets are zero for legacy rows.
  cache_creation_tokens?: number;
  uncached_input_tokens?: number;
  billable_total_tokens?: number;
}

export interface ModelDistributionItem {
  model: string;
  tokens: number;
}

export interface UserSelf {
  id: number;
  username: string;
  display_name: string;
  email: string;
  group: string;
  status: number;
  role: number;
}

export interface AccountDashboard {
  balance?: number;
  used_amount?: number;
  request_count?: number;
  frozen_amount?: number;
  group?: string;
  group_ratio?: number;
  usage?: UsageSummaryItem[];
  today_amount?: number;
  today_prompt_tokens?: number;
  today_completion_tokens?: number;
  today_cache_read_tokens?: number;
  avg_latency?: number;
  model_distribution?: ModelDistributionItem[];
}

export const userSelfQueryOptions = queryOptions({
  queryKey: ['user-self'] as const,
  queryFn: async ({ signal }) => {
    const response = await apiClient.get('/user/self', { signal });
    return unwrapApiData<UserSelf | null>(response.data);
  },
  staleTime: 5 * 60 * 1000,
  meta: { protected: true },
});

export const accountDashboardQueryOptions = queryOptions({
  queryKey: ['dashboard-summary'] as const,
  queryFn: async ({ signal }) => {
    const response = await apiClient.get('/user/dashboard', { signal });
    return unwrapApiData<AccountDashboard | null>(response.data);
  },
  staleTime: 30 * 1000,
  refetchOnWindowFocus: true,
  refetchInterval: () => {
    const now = new Date();
    return Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate() + 1) - now.getTime();
  },
  meta: { protected: true },
});

export function subscriptionProgressRefreshInterval(progress: SubscriptionProgressData | null | undefined) {
  if (!progress) return false;
  const boundaries = [progress.expires_at, progress.daily_used?.next_refresh, progress.weekly_used?.next_refresh, progress.monthly_used?.next_refresh]
    .filter((value): value is number => typeof value === 'number' && value > 0);
  if (boundaries.length === 0) return false;
  const delay = Math.min(...boundaries) * 1000 - Date.now();
  // Recheck overdue snapshots without spinning if the client clock is ahead.
  return delay > 0 ? Math.min(delay, 86_400_000) : 60_000;
}

export function subscriptionProgressQueryOptions(userId: string | null) {
  return queryOptions({
    queryKey: ['my-subscription-progress', userId] as const,
    enabled: !!userId && userId !== '0',
    queryFn: async ({ signal }) => {
      const response = await apiClient.get(`/v1/subscriptions/progress?user_id=${userId}`, { signal });
      // shortcut: legacy progress signals absence by message; use a typed reason when the endpoint gains one.
      if (response.data?.success === false && response.data.message === 'subscription not found') return null;
      return unwrapApiData<SubscriptionProgressData | null>(response.data) ?? null;
    },
    refetchOnWindowFocus: true,
    refetchInterval: (query) => query.state.status === 'error' ? false : subscriptionProgressRefreshInterval(query.state.data),
    meta: { protected: true },
  });
}
