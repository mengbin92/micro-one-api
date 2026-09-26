import { useMemo, useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Check, ChevronRight, CreditCard, Loader2, ShieldCheck, WalletCards } from 'lucide-react';
import { toast } from 'sonner';
import { PurchasablePlansSection } from '@/components/PurchasablePlansSection';
import { Button } from '@/components/ui/button';
import { apiClient } from '@/lib/api';
import { unwrapApiData } from '@/lib/api-response';
import { amountUnitsToCurrencyUnits } from '@/lib/amount';
import { cn } from '@/lib/utils';
import { t } from '@/lib/i18n';
import { accountDashboardQueryOptions } from '@/lib/account-queries';

const DEFAULT_RECHARGE_AMOUNT_MULTIPLIER = 10;
const RATE = rechargeAmountMultiplier();
const PRESET_AMOUNTS = [2, 10, 20, 50, 100];

interface PaymentOrder {
  trade_no?: string;
  pay_url?: string;
}

interface CreatePaymentResponse {
  trade_no?: string;
  pay_url?: string;
  order?: PaymentOrder;
}

interface PaymentMutationVariables {
  paymentWindow: Window | null;
}

function formatCny(value: number) {
  return `¥ ${value.toFixed(2)}`;
}

function formatUsd(value: number) {
  return `$ ${value.toFixed(4)}`;
}

function amountUnitsToUsd(value?: number) {
  return amountUnitsToCurrencyUnits(value);
}

function rechargeAmountMultiplier() {
  const parsed = Number(import.meta.env.VITE_RECHARGE_AMOUNT_MULTIPLIER ?? DEFAULT_RECHARGE_AMOUNT_MULTIPLIER);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : DEFAULT_RECHARGE_AMOUNT_MULTIPLIER;
}

function normalizeAmount(value: string) {
  const parsed = Number.parseFloat(value);
  if (!Number.isFinite(parsed)) return 0;
  return Math.round(parsed * 100) / 100;
}

export function RechargePage() {
  const [amountInput, setAmountInput] = useState('20');
  const amount = normalizeAmount(amountInput);
  const receiveAmount = useMemo(() => amount * RATE, [amount]);

  const { data: dashboard } = useQuery(accountDashboardQueryOptions);

  const createPayment = useMutation({
    mutationFn: async (variables: PaymentMutationVariables) => {
      void variables;
      const res = await apiClient.post('/user/pay', {
        amount,
        payment_method: 'alipay',
      });
      return unwrapApiData<CreatePaymentResponse>(res.data, t("创建支付订单失败"));
    },
    onSuccess: (data, variables) => {
      const payURL = data.pay_url || data.order?.pay_url;
      if (!payURL) {
        variables.paymentWindow?.close();
        toast.success(t("支付订单已创建，请在我的订单中查看状态"));
        return;
      }
      if (payURL.startsWith('mock://')) {
        variables.paymentWindow?.close();
        toast.success(t(`测试订单已创建：${data.trade_no || data.order?.trade_no || '-'}`));
        return;
      }
      if (variables.paymentWindow) {
        variables.paymentWindow.location.href = payURL;
        return;
      }
      window.open(payURL, '_blank', 'noopener,noreferrer');
    },
    onError: (_error, variables) => {
      variables.paymentWindow?.close();
    },
  });

  const canSubmit = amount > 0 && !createPayment.isPending;

  const handleCreatePayment = () => {
    const paymentWindow = window.open('about:blank', '_blank');
    if (paymentWindow) {
      paymentWindow.opener = null;
      paymentWindow.document.title = t("正在前往支付");
      paymentWindow.document.body.innerHTML = t("<p style=\"font-family: sans-serif; padding: 24px;\">正在创建支付订单，请稍候...</p>");
    }
    createPayment.mutate({ paymentWindow });
  };

  return (
    <div className="mx-auto flex max-w-7xl flex-col gap-4 pb-28">
      <section className="overflow-hidden border-y border-border bg-card md:rounded-lg md:border">
        <div className="grid min-h-28 grid-cols-1 md:grid-cols-[1fr_auto_1fr_auto_1.1fr]">
          <div className="flex flex-col justify-center px-6 py-5 text-center md:text-left">
            <div className="text-2xl font-semibold text-primary sm:text-3xl">1 CNY = {RATE} USD</div>
            <div className="mt-2 text-sm text-muted-foreground">{t("固定充值倍率")}</div>
          </div>
          <div className="hidden items-center px-4 text-muted-foreground md:flex">
            <ChevronRight className="size-8" />
          </div>
          <div className="flex flex-col justify-center border-t border-border px-6 py-5 text-center md:border-l md:border-t-0">
            <div className="text-3xl font-semibold text-foreground">{formatCny(amount)}</div>
            <div className="mt-2 text-sm text-muted-foreground">{t("您将支付")}</div>
          </div>
          <div className="hidden items-center px-4 text-muted-foreground md:flex">
            <ChevronRight className="size-8" />
          </div>
          <div className="flex flex-col justify-center bg-accent px-6 py-5 text-center text-accent-foreground md:text-left">
            <div>
              <div className="text-3xl font-semibold">{formatUsd(receiveAmount)}</div>
              <div className="mt-2 text-sm">{t("充值成功后到账")}</div>
            </div>
          </div>
        </div>
      </section>

      <section className="border-b border-border p-5">
        <div className="mb-5 flex items-center justify-between gap-3">
          <h2 className="text-lg font-semibold text-foreground">{t("快捷金额")}</h2>
          <div className="hidden items-center gap-2 text-sm font-medium text-muted-foreground sm:flex">
            <WalletCards className="size-4" />{t("当前余额")}{formatUsd(amountUnitsToUsd(dashboard?.balance))}
          </div>
        </div>

        <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
          {PRESET_AMOUNTS.map((preset) => {
            const selected = amount === preset;
            return (
              <button
                key={preset}
                type="button"
                aria-pressed={selected}
                onClick={() => setAmountInput(String(preset))}
                className={cn(
                  'relative flex min-h-24 flex-col items-center justify-center rounded-lg border bg-card px-4 text-center transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring',
                  selected
                    ? 'border-primary bg-accent text-accent-foreground ring-1 ring-primary'
                    : 'border-border text-foreground hover:border-primary hover:bg-accent/50',
                )}
              >
                <span className="text-2xl font-semibold">¥{preset}</span>
                <span className="mt-2 text-sm text-muted-foreground">{t("得")}{formatUsd(preset * RATE)}
                </span>
                {selected && (
                  <span className="absolute right-4 top-1/2 grid size-8 -translate-y-1/2 place-items-center rounded-full bg-primary text-primary-foreground">
                    <Check className="size-5" />
                  </span>
                )}
              </button>
            );
          })}
        </div>

        <div className="mt-6">
          <label htmlFor="custom-amount" className="text-sm font-medium text-foreground">{t("自定义金额")}</label>
          <div className="mt-3 flex h-14 items-center rounded-lg border border-input bg-background px-4 focus-within:border-ring focus-within:ring-1 focus-within:ring-ring">
            <span className="mr-4 text-lg font-semibold text-muted-foreground">¥</span>
            <input
              id="custom-amount"
              type="number"
              min="0.01"
              step="0.01"
              value={amountInput}
              onChange={(event) => setAmountInput(event.target.value)}
              className="h-full min-w-0 flex-1 bg-transparent text-lg font-semibold text-foreground outline-none"
            />
          </div>
        </div>
      </section>

      <section className="border-b border-border p-5">
        <h2 className="mb-4 text-lg font-semibold text-foreground">{t("支付方式")}</h2>
        <div className="flex min-h-16 w-full items-center gap-4 rounded-lg border border-primary bg-accent px-4 text-left">
          <span className="grid size-10 shrink-0 place-items-center rounded-lg bg-primary text-xl font-semibold text-primary-foreground">{t("支")}</span>
          <span className="min-w-0 flex-1">
            <span className="block text-base font-semibold text-foreground">{t("支付宝")}</span>
            <span className="block text-sm text-muted-foreground">{t("推荐使用支付宝扫码支付")}</span>
          </span>
          <span className="grid size-8 place-items-center rounded-full bg-primary text-primary-foreground">
            <Check className="size-5" />
          </span>
        </div>
      </section>

      <section className="border-b border-border p-5">
        <div className="grid gap-4 md:grid-cols-[1fr_auto_1fr_auto] md:items-center">
          <div>
            <div className="text-sm text-muted-foreground">{t("充值金额（您将支付）")}</div>
            <div className="mt-3 text-2xl font-semibold text-foreground">{formatCny(amount)}</div>
          </div>
          <ChevronRight className="hidden size-8 text-muted-foreground md:block" />
          <div>
            <div className="text-sm text-muted-foreground">{t("到账金额（充值成功后得到到账）")}</div>
            <div className="mt-3 text-2xl font-semibold text-primary">{formatUsd(receiveAmount)}</div>
          </div>
          <div className="text-left md:text-right">
            <div className="text-sm text-muted-foreground">{t("充值倍率")}</div>
            <div className="mt-3 text-lg font-semibold text-foreground">1 CNY = {RATE} USD</div>
          </div>
        </div>
      </section>

      <section className="p-5">
        <PurchasablePlansSection />
      </section>

      <div className="fixed inset-x-4 bottom-4 z-10 md:left-[19rem] md:right-8 xl:right-10">
        <Button
          type="button"
          size="lg"
          disabled={!canSubmit}
          onClick={handleCreatePayment}
          className="h-14 w-full rounded-lg text-base font-semibold shadow-surface-md"
        >
          {createPayment.isPending ? <Loader2 className="size-5 animate-spin" /> : <ShieldCheck className="size-5" />}{t("确认支付")}{formatCny(amount)}
        </Button>
      </div>

      <div className="flex items-center gap-2 px-1 text-sm text-muted-foreground">
        <CreditCard className="size-4" />{t("支付完成后系统会自动入账，可在我的订单查看订单状态。")}</div>
    </div>
  );
}
