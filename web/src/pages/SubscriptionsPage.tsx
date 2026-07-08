import { useQuery } from '@tanstack/react-query';
import { Gauge } from 'lucide-react';
import { apiClient } from '@/lib/api';
import { EmptyState } from '@/components/EmptyState';
import { Card, CardContent } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import {
  SubscriptionProgressCard,
  type SubscriptionProgressData,
} from '@/components/SubscriptionProgress';

interface UserLimits {
  user_rpm_limit?: number;
}

function formatRPMLimit(limit?: number) {
  if (!limit || limit <= 0) return '不限';
  return `${limit} 次/分钟`;
}

export function SubscriptionsPage() {
  const userId = localStorage.getItem('userId');

  const { data: progress, isLoading } = useQuery({
    queryKey: ['my-subscription-progress', userId],
    enabled: !!userId && userId !== '0',
    queryFn: async () => {
      const res = await apiClient.get(`/v1/subscriptions/progress?user_id=${userId}`);
      if (res.data?.success === false) return null;
      return (res.data?.data as SubscriptionProgressData | null) ?? null;
    },
  });

  const { data: limits, isLoading: limitsLoading } = useQuery({
    queryKey: ['user-limits'],
    queryFn: async () => {
      const res = await apiClient.get('/user/limits');
      return (res.data?.data as UserLimits | null) ?? null;
    },
    retry: false,
    throwOnError: false,
  });

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-2xl font-semibold">我的订阅</h2>
        <p className="mt-1 text-sm text-muted-foreground">查看当前订阅用量与到期时间。购买套餐请前往「充值 / 订阅」。</p>
      </div>

      <Card className="rounded-lg border bg-card shadow-sm">
        <CardContent className="flex items-center justify-between gap-4 p-4">
          <div className="flex min-w-0 items-center gap-3">
            <span className="grid size-10 shrink-0 place-items-center rounded-lg bg-sky-50 text-sky-600 dark:bg-sky-500/10 dark:text-sky-300">
              <Gauge className="size-5" />
            </span>
            <div className="min-w-0">
              <div className="text-sm font-semibold text-foreground">请求频率</div>
              <div className="text-xs text-muted-foreground">API 请求每分钟上限</div>
            </div>
          </div>
          {limitsLoading ? (
            <Skeleton className="h-6 w-20" />
          ) : (
            <div className="shrink-0 text-right text-lg font-semibold tabular-nums text-slate-900 dark:text-white">
              {formatRPMLimit(limits?.user_rpm_limit)}
            </div>
          )}
        </CardContent>
      </Card>

      {isLoading ? (
        <div className="rounded-xl border bg-card p-4">
          <Skeleton className="mb-3 h-5 w-40" />
          <div className="space-y-2">
            <Skeleton className="h-2 w-full" />
            <Skeleton className="h-2 w-full" />
            <Skeleton className="h-2 w-full" />
          </div>
        </div>
      ) : progress ? (
        <SubscriptionProgressCard progress={progress} title={progress.subscription_name || "当前订阅"} />
      ) : (
        <EmptyState
          title="暂无活跃订阅"
          description="你当前没有生效中的订阅，可前往「充值 / 订阅」选择套餐购买。"
        />
      )}
    </div>
  );
}
