import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { CalendarClock, Check, Loader2, Wallet } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import { apiClient } from '@/lib/api';
import { EmptyState } from '@/components/EmptyState';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import {
  SubscriptionProgressCard,
  type SubscriptionProgressData,
} from '@/components/SubscriptionProgress';

// Mirrors the subscription-group DTO returned by /api/v1/subscriptions/groups.
// Only enabled groups with price_quota>0 and duration_days>0 are returned there.
interface PurchasableGroup {
  id: number;
  name: string;
  display_name: string;
  platform: string;
  daily_limit_usd: number | null;
  weekly_limit_usd: number | null;
  monthly_limit_usd: number | null;
  price_quota: number;
  duration_days: number;
}

interface PaymentOrder {
  trade_no?: string;
  pay_url?: string;
}

interface PurchaseResponse {
  subscription?: SubscriptionProgressData | null;
  payment?: PaymentOrder | null;
}

interface PurchaseVariables {
  groupId: number;
  paymentWindow?: Window | null;
}

function formatUsd(value: number) {
  return `$${value.toFixed(2)}`;
}

function limitLabel(prefix: string, limit: number | null) {
  return `${prefix} ${limit == null ? '不限' : formatUsd(limit)}`;
}

export function SubscriptionsPage() {
  const userId = localStorage.getItem('userId');
  const queryClient = useQueryClient();
  const [pendingPlan, setPendingPlan] = useState<PurchasableGroup | null>(null);

  const { data: progress, isLoading } = useQuery({
    queryKey: ['my-subscription-progress', userId],
    enabled: !!userId && userId !== '0',
    queryFn: async () => {
      const res = await apiClient.get(`/v1/subscriptions/progress?user_id=${userId}`);
      if (res.data?.success === false) return null;
      return (res.data?.data as SubscriptionProgressData | null) ?? null;
    },
  });

  const { data: plans, isLoading: plansLoading } = useQuery({
    queryKey: ['purchasable-subscription-groups'],
    queryFn: async () => {
      const res = await apiClient.get('/v1/subscriptions/groups');
      if (res.data?.success === false) return [];
      return (res.data?.data as PurchasableGroup[] | null) ?? [];
    },
  });

  const purchase = useMutation({
    mutationFn: async ({ groupId }: PurchaseVariables) => {
      const res = await apiClient.post('/v1/subscriptions/purchase/payment', {
        group_id: groupId,
        channel: 'alipay',
      });
      if (res.data?.success === false) {
        throw new Error(res.data?.message || '购买失败');
      }
      return (res.data?.data ?? {}) as PurchaseResponse;
    },
    onSuccess: (data, variables) => {
      if (data.subscription) {
        variables.paymentWindow?.close();
        toast.success('订阅购买成功');
        setPendingPlan(null);
        void queryClient.invalidateQueries({ queryKey: ['my-subscription-progress'] });
        void queryClient.invalidateQueries({ queryKey: ['user-dashboard'] });
        return;
      }

      const payURL = data.payment?.pay_url;
      if (!payURL) {
        variables.paymentWindow?.close();
        toast.success('支付订单已创建，请在我的订单中查看状态');
        setPendingPlan(null);
        return;
      }
      if (payURL.startsWith('mock://')) {
        variables.paymentWindow?.close();
        toast.success(`测试支付订单已创建：${data.payment?.trade_no || '-'}`);
        setPendingPlan(null);
        return;
      }
      if (variables.paymentWindow) {
        variables.paymentWindow.location.href = payURL;
      } else {
        window.open(payURL, '_blank', 'noopener,noreferrer');
      }
      setPendingPlan(null);
      toast.success('支付订单已创建，请在新打开的页面完成支付');
    },
    onError: (error: Error, variables) => {
      variables.paymentWindow?.close();
      toast.error(error.message || '购买失败');
    },
  });

  const hasActive = !!progress;

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-2xl font-semibold">我的订阅</h2>
        <p className="mt-1 text-sm text-muted-foreground">查看当前订阅用量，或选择套餐创建支付订单。</p>
      </div>

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
        <SubscriptionProgressCard progress={progress} title="当前订阅" />
      ) : (
        <EmptyState title="暂无活跃订阅" description="你当前没有生效中的订阅，可在下方选择套餐购买。" />
      )}

      <div className="space-y-3">
        <div className="flex items-baseline justify-between">
          <h3 className="text-lg font-semibold">可购买套餐</h3>
          {hasActive ? (
            <span className="text-xs text-muted-foreground">已有生效订阅，到期后可再次购买</span>
          ) : null}
        </div>

        {plansLoading ? (
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            <Skeleton className="h-48 w-full" />
            <Skeleton className="h-48 w-full" />
            <Skeleton className="h-48 w-full" />
          </div>
        ) : !plans || plans.length === 0 ? (
          <EmptyState title="暂无可购买套餐" description="管理员尚未上架任何可自助购买的订阅套餐。" />
        ) : (
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {plans.map((plan) => (
              <Card key={plan.id} className="flex flex-col">
                <CardHeader>
                  <CardTitle className="flex items-center justify-between gap-2">
                    <span>{plan.display_name || plan.name}</span>
                    <span className="text-xs font-normal uppercase text-muted-foreground">
                      {plan.platform}
                    </span>
                  </CardTitle>
                </CardHeader>
                <CardContent className="flex flex-1 flex-col gap-3">
                  <div className="text-2xl font-bold">
                    {formatUsd(plan.price_quota)}
                  </div>
                  <ul className="space-y-1.5 text-sm text-muted-foreground">
                    <li className="flex items-center gap-2">
                      <CalendarClock className="size-4" /> 有效期 {plan.duration_days} 天
                    </li>
                    <li className="flex items-center gap-2">
                      <Check className="size-4" /> {limitLabel('每日额度', plan.daily_limit_usd)}
                    </li>
                    <li className="flex items-center gap-2">
                      <Check className="size-4" /> {limitLabel('每月额度', plan.monthly_limit_usd)}
                    </li>
                  </ul>
                  <Button
                    className="mt-auto"
                    disabled={hasActive || purchase.isPending}
                    onClick={() => setPendingPlan(plan)}
                  >
                    <Wallet className="size-4" />
                    {hasActive ? '已有生效订阅' : '购买订阅'}
                  </Button>
                </CardContent>
              </Card>
            ))}
          </div>
        )}
      </div>

      <Dialog open={!!pendingPlan} onOpenChange={(next) => !next && setPendingPlan(null)}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>确认购买订阅</DialogTitle>
            <DialogDescription>
              将创建支付订单并跳转支付。套餐价格{' '}
              <strong>{pendingPlan ? formatUsd(pendingPlan.price_quota) : ''}</strong>
              ，开通「{pendingPlan?.display_name || pendingPlan?.name}」订阅，有效期{' '}
              {pendingPlan?.duration_days} 天。
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setPendingPlan(null)} disabled={purchase.isPending}>
              取消
            </Button>
            <Button
              onClick={() => {
                if (!pendingPlan) return;
                const paymentWindow = window.open('about:blank', '_blank');
                if (paymentWindow) {
                  paymentWindow.opener = null;
                  paymentWindow.document.title = '正在前往支付';
                  paymentWindow.document.body.innerHTML = '<p style="font-family: sans-serif; padding: 24px;">正在创建支付订单，请稍候...</p>';
                }
                purchase.mutate({ groupId: pendingPlan.id, paymentWindow });
              }}
              disabled={purchase.isPending}
            >
              {purchase.isPending ? <Loader2 className="size-4 animate-spin" /> : <Wallet className="size-4" />}
              确认购买
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
