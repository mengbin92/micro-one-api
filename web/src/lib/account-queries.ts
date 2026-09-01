import { queryOptions } from '@tanstack/react-query';
import { apiClient } from '@/lib/api';
import { unwrapApiData } from '@/lib/api-response';

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
  queryFn: async () => {
    const response = await apiClient.get('/user/self');
    return unwrapApiData<UserSelf | null>(response.data);
  },
  staleTime: 5 * 60 * 1000,
});

export const accountDashboardQueryOptions = queryOptions({
  queryKey: ['dashboard-summary'] as const,
  queryFn: async () => {
    const response = await apiClient.get('/user/dashboard');
    return unwrapApiData<AccountDashboard | null>(response.data);
  },
  staleTime: 30 * 1000,
});
