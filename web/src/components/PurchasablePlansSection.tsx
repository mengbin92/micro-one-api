import { ContractSummary, type SubscriptionContract } from '@/components/SubscriptionContract';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { CalendarClock, Check, Loader2, Wallet } from 'lucide-react';
import { useRef, useState } from 'react';
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
import { SubscriptionProgressCard, type SubscriptionProgressData } from '@/components/SubscriptionProgress';
import { t } from '@/lib/i18n';

// Mirrors the subscription-group DTO returned by /api/v1/subscriptions/groups.
// Only enabled groups with price_quota>0 and duration_days>0 are returned there.
interface PurchasableGroup {
  plan_id?: number;
  contract?: SubscriptionContract;
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
  planId?: number;
  changeSubscriptionId?: number;
  groupId: number;
  paymentWindow?: Window | null;
}

function formatUsd(value: number) {
  return `$${value.toFixed(2)}`;
}

function limitLabel(prefix: string, limit: number | null) {
  return `${prefix} ${limit == null ? t("不限") : formatUsd(limit)}`;
}

/**
 * Self-contained "purchasable subscription plans" section: lists enabled
 * subscription groups, shows the active-subscription progress (shared cache
 * key `my-subscription-progress`), and handles the purchase → payment flow.
 * Used by the 充值/订阅 page so recharge and subscription purchase live
 * together. The 我的订阅 page no longer renders this block.
 */
export function PurchasablePlansSection() {
  const userId = localStorage.getItem('userId');
  const queryClient = useQueryClient();
  const [pendingPlan, setPendingPlan] = useState<PurchasableGroup | null>(null);
  // Session-scoped idempotency key (v0.18 P0): generated once per purchase
  // intent and reused across retries of that intent, so a double-fired or
  // retried request carries the same key and the backend (billing ledger
  // unique constraint) dedupes it instead of creating duplicate payment
  // orders / charging twice. Cleared once the intent definitively finishes.
  const purchaseKeyRef = useRef<string | null>(null);

  // Shared query key with SubscriptionsPage so both views stay in sync without
  // a duplicate network request (react-query dedupes identical queryKeys).
  const { data: progress, isLoading } = useQuery({
    queryKey: ['my-subscription-progress', userId],
    enabled: !!userId && userId !== '0',
    queryFn: async () => {
      const res = await apiClient.get(`/v1/subscriptions/progress?user_id=${userId}`);
      if (res.data?.success === false) return null;
      return (res.data?.data as SubscriptionProgressData | null) ?? null;
    },
  });

  const { data: legacyGroups, isLoading: groupsLoading } = useQuery({
    queryKey: ['purchasable-subscription-groups'],
    queryFn: async () => {
      const res = await apiClient.get('/v1/subscriptions/groups');
      if (res.data?.success === false) return [];
      return (res.data?.data as PurchasableGroup[] | null) ?? [];
    },
  });


  const { data: contractPlans, isLoading: contractPlansLoading } = useQuery({
    queryKey: ['purchasable-subscription-plans'],
    queryFn: async () => {
      const res = await apiClient.get('/v1/subscriptions/plans');
      if (res.data?.success === false) throw new Error(res.data.message || '套餐加载失败');
      const rows = (res.data?.data ?? []) as { id: number; name: string; price_quota: number; validity_days: number; contract?: SubscriptionContract; group?: PurchasableGroup }[];
      return rows.map((p): PurchasableGroup => ({ id: p.id, plan_id: p.id, name: p.name, display_name: p.name, platform: p.group?.platform ?? '', price_quota: p.price_quota, duration_days: p.validity_days, contract: p.contract, daily_limit_usd: p.contract ? p.contract.quota_policy.daily_limit_usd : p.group?.daily_limit_usd ?? null, weekly_limit_usd: p.contract ? p.contract.quota_policy.weekly_limit_usd : p.group?.weekly_limit_usd ?? null, monthly_limit_usd: p.contract ? p.contract.quota_policy.monthly_limit_usd : p.group?.monthly_limit_usd ?? null }));
    },
  });
  const plans = contractPlans?.length ? contractPlans : legacyGroups;
  const plansLoading = groupsLoading || contractPlansLoading;

  const purchase = useMutation({
    mutationFn: async ({ groupId, planId, changeSubscriptionId }: PurchaseVariables) => {
      if (!purchaseKeyRef.current) {
        purchaseKeyRef.current = crypto.randomUUID();
      }
      const res = await apiClient.post(
        changeSubscriptionId ? '/v1/subscriptions/change' : '/v1/subscriptions/purchase/payment',
        {
          ...(changeSubscriptionId ? { from_subscription_id: changeSubscriptionId, to_plan_id: planId } : {}),
          ...(planId ? { plan_id: planId } : { group_id: groupId }),
          channel: 'alipay',
        },
        { headers: { 'Idempotency-Key': purchaseKeyRef.current } }
      );
      if (res.data?.success === false) {
        throw new Error(res.data?.message || t("购买失败"));
      }
      return (res.data?.data ?? {}) as PurchaseResponse;
    },
    onSuccess: (data, variables) => {
      // The intent is definitively done (order created or subscription
      // granted): a fresh key is fine for the next purchase.
      purchaseKeyRef.current = null;
      if (data.subscription || variables.changeSubscriptionId) {
        variables.paymentWindow?.close();
        toast.success(variables.changeSubscriptionId ? '订阅更换已受理，请查看当前权益；降档在目标套餐续费时生效。' : t("订阅购买成功"));
        setPendingPlan(null);
        void queryClient.invalidateQueries({ queryKey: ['my-subscription-progress'] });
        void queryClient.invalidateQueries({ queryKey: ['user-dashboard'] });
        return;
      }

      const payURL = data.payment?.pay_url;
      if (!payURL) {
        variables.paymentWindow?.close();
        toast.success(t("支付订单已创建，请在我的订单中查看状态"));
        setPendingPlan(null);
        return;
      }
      if (payURL.startsWith('mock://')) {
        variables.paymentWindow?.close();
        toast.success(t(`测试支付订单已创建：${data.payment?.trade_no || '-'}`));
        setPendingPlan(null);
        return;
      }
      if (variables.paymentWindow) {
        variables.paymentWindow.location.href = payURL;
      } else {
        window.open(payURL, '_blank', 'noopener,noreferrer');
      }
      setPendingPlan(null);
      toast.success(t("支付订单已创建，请在新打开的页面完成支付"));
    },
    onError: (error: Error, variables) => {
      variables.paymentWindow?.close();
      // 409 = the server already processed this key (duplicate request): the
      // intent is resolved, drop the key so a fresh purchase can start.
      // Other errors (timeout / 5xx) keep the key so the user's retry is
      // deduped against the possibly-already-processed first attempt.
      const status = (error as { response?: { status?: number } }).response?.status;
      if (status === 409) {
        purchaseKeyRef.current = null;
      }
      toast.error(error.message || t("购买失败"));
    },
  });

  const hasActive = !!progress;
  const changeIntent = !!(progress && pendingPlan?.contract && progress.contract?.digest !== pendingPlan.contract.digest);

  return (
    <div className="space-y-3">
      <div className="flex items-baseline justify-between">
        <h3 className="text-lg font-semibold">{t("订阅套餐")}</h3>
        {hasActive ? (
          <span className="text-xs text-muted-foreground">{t("已有生效订阅，到期后可再次购买")}</span>
        ) : null}
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
        <SubscriptionProgressCard progress={progress} title={t("当前订阅")} />
      ) : null}

      {plansLoading ? (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <Skeleton className="h-48 w-full" />
          <Skeleton className="h-48 w-full" />
          <Skeleton className="h-48 w-full" />
        </div>
      ) : !plans || plans.length === 0 ? (
        <EmptyState title={t("暂无可购买套餐")} description={t("管理员尚未上架任何可自助购买的订阅套餐。")} />
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
                <ContractSummary contract={plan.contract} />
                <ul className="space-y-1.5 text-sm text-muted-foreground">
                  <li className="flex items-center gap-2">
                    <CalendarClock className="size-4" />{t("有效期")}{plan.duration_days}{t("天")}</li>
                  <li className="flex items-center gap-2">
                    <Check className="size-4" /> {limitLabel(t("每日额度"), plan.daily_limit_usd)}
                  </li>
                  <li className="flex items-center gap-2">
                    <Check className="size-4" /> {limitLabel(t("每月额度"), plan.monthly_limit_usd)}
                  </li>
                </ul>
                <Button
                  className="mt-auto"
                  disabled={(hasActive && !plan.contract) || purchase.isPending}
                  onClick={() => setPendingPlan(plan)}
                >
                  <Wallet className="size-4" />
                  {hasActive && plan.contract && progress?.contract?.digest === plan.contract.digest ? '续费订阅' : hasActive && plan.contract ? '更换套餐' : hasActive ? t("已有生效订阅") : t("购买订阅")}
                </Button>
              </CardContent>
            </Card>
          ))}
        </div>
      )}

      <Dialog open={!!pendingPlan} onOpenChange={(next) => !next && setPendingPlan(null)}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t("确认购买订阅")}</DialogTitle>
            <DialogDescription>{changeIntent ? '更换为所选合同；升级从钱包收取差价，降档在目标套餐续费时生效。已用额度保留。目标套餐价格' : t("将创建支付订单并跳转支付。套餐价格")}{' '}
              <strong>{pendingPlan ? formatUsd(pendingPlan.price_quota) : ''}</strong>{t("，开通「")}{pendingPlan?.display_name || pendingPlan?.name}{t("」订阅，有效期")}{' '}
              {pendingPlan?.duration_days}{t("天。")}</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setPendingPlan(null)} disabled={purchase.isPending}>{t("取消")}</Button>
            <Button
              onClick={() => {
                if (!pendingPlan) return;
                const paymentWindow = changeIntent ? null : window.open('about:blank', '_blank');
                if (paymentWindow) {
                  paymentWindow.opener = null;
                  paymentWindow.document.title = t("正在前往支付");
                  paymentWindow.document.body.innerHTML = t("<p style=\"font-family: sans-serif; padding: 24px;\">正在创建支付订单，请稍候...</p>");
                }
                purchase.mutate({ groupId: pendingPlan.id, planId: pendingPlan.plan_id, changeSubscriptionId: changeIntent ? progress?.id : undefined, paymentWindow });
              }}
              disabled={purchase.isPending}
            >
              {purchase.isPending ? <Loader2 className="size-4 animate-spin" /> : <Wallet className="size-4" />}{t("确认购买")}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
