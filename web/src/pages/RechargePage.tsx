import { useMemo, useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Check, ChevronRight, CreditCard, Loader2, ShieldCheck, WalletCards } from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { apiClient } from '@/lib/api';
import { unwrapApiData } from '@/lib/api-response';
import { amountUnitsToCurrencyUnits } from '@/lib/amount';
import { cn } from '@/lib/utils';

const DEFAULT_RECHARGE_AMOUNT_MULTIPLIER = 10;
const RATE = rechargeAmountMultiplier();
const PRESET_AMOUNTS = [2, 10, 20, 50, 100];

interface AccountDashboard {
  balance?: number;
}

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

  const { data: dashboard } = useQuery({
    queryKey: ['recharge-dashboard'],
    queryFn: async () => {
      const res = await apiClient.get('/user/dashboard');
      return unwrapApiData<AccountDashboard>(res.data);
    },
  });

  const createPayment = useMutation({
    mutationFn: async (variables: PaymentMutationVariables) => {
      void variables;
      const res = await apiClient.post('/user/pay', {
        amount,
        payment_method: 'alipay',
      });
      return unwrapApiData<CreatePaymentResponse>(res.data, '创建支付订单失败');
    },
    onSuccess: (data, variables) => {
      const payURL = data.pay_url || data.order?.pay_url;
      if (!payURL) {
        variables.paymentWindow?.close();
        toast.success('支付订单已创建，请在我的订单中查看状态');
        return;
      }
      if (payURL.startsWith('mock://')) {
        variables.paymentWindow?.close();
        toast.success(`测试订单已创建：${data.trade_no || data.order?.trade_no || '-'}`);
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
      paymentWindow.document.title = '正在前往支付';
      paymentWindow.document.body.innerHTML = '<p style="font-family: sans-serif; padding: 24px;">正在创建支付订单，请稍候...</p>';
    }
    createPayment.mutate({ paymentWindow });
  };

  return (
    <div className="mx-auto flex max-w-7xl flex-col gap-4 pb-28">
      <section className="overflow-hidden rounded-lg border border-blue-100 bg-white shadow-sm dark:border-white/10 dark:bg-card">
        <div className="grid min-h-28 grid-cols-1 md:grid-cols-[1fr_auto_1fr_auto_1.1fr]">
          <div className="flex flex-col justify-center px-6 py-5 text-center md:text-left">
            <div className="text-3xl font-black text-blue-600 sm:text-4xl">1 CNY = {RATE} USD</div>
            <div className="mt-2 text-sm font-bold text-slate-500 dark:text-slate-400">实时汇率 · 固定倍率</div>
          </div>
          <div className="hidden items-center px-4 text-blue-500 md:flex">
            <ChevronRight className="size-8" />
          </div>
          <div className="flex flex-col justify-center border-t border-blue-50 px-6 py-5 text-center md:border-l md:border-t-0 dark:border-white/10">
            <div className="text-4xl font-black text-orange-500">{formatCny(amount)}</div>
            <div className="mt-2 text-sm font-bold text-slate-500 dark:text-slate-400">您将支付</div>
          </div>
          <div className="hidden items-center px-4 text-blue-500 md:flex">
            <ChevronRight className="size-8" />
          </div>
          <div className="relative flex flex-col justify-center overflow-hidden bg-blue-600 px-6 py-5 text-center text-white md:text-left">
            <div className="absolute inset-y-0 -left-10 hidden w-20 -skew-x-12 bg-white md:block dark:bg-card" />
            <div className="relative md:pl-8">
              <div className="text-4xl font-black">{formatUsd(receiveAmount)}</div>
              <div className="mt-2 text-sm font-bold text-blue-100">充值成功后到账</div>
            </div>
          </div>
        </div>
      </section>

      <section className="rounded-lg border border-slate-200 bg-white p-5 shadow-sm dark:border-white/10 dark:bg-card">
        <div className="mb-5 flex items-center justify-between gap-3">
          <h2 className="text-lg font-black text-slate-950 dark:text-white">快捷金额</h2>
          <div className="hidden items-center gap-2 rounded-full bg-emerald-50 px-3 py-1.5 text-sm font-black text-emerald-600 sm:flex dark:bg-emerald-500/10 dark:text-emerald-300">
            <WalletCards className="size-4" />
            当前余额 {formatUsd(amountUnitsToUsd(dashboard?.balance))}
          </div>
        </div>

        <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
          {PRESET_AMOUNTS.map((preset) => {
            const selected = amount === preset;
            return (
              <button
                key={preset}
                type="button"
                onClick={() => setAmountInput(String(preset))}
                className={cn(
                  'relative flex min-h-24 flex-col items-center justify-center rounded-lg border bg-white px-4 text-center transition-colors dark:bg-background',
                  selected
                    ? 'border-blue-500 bg-blue-50/60 text-blue-600 ring-1 ring-blue-500 dark:bg-blue-500/10'
                    : 'border-slate-200 text-slate-950 hover:border-blue-300 hover:bg-blue-50/40 dark:border-white/10 dark:text-white dark:hover:bg-white/5',
                )}
              >
                <span className="text-2xl font-black">¥{preset}</span>
                <span className="mt-2 text-sm font-bold text-slate-500 dark:text-slate-400">
                  得 {formatUsd(preset * RATE)}
                </span>
                {selected && (
                  <span className="absolute right-4 top-1/2 grid size-8 -translate-y-1/2 place-items-center rounded-full bg-blue-600 text-white shadow-sm">
                    <Check className="size-5" />
                  </span>
                )}
              </button>
            );
          })}
        </div>

        <div className="mt-6">
          <label htmlFor="custom-amount" className="text-base font-black text-slate-700 dark:text-slate-200">
            自定义金额
          </label>
          <div className="mt-3 flex h-14 items-center rounded-lg border border-slate-200 bg-white px-4 shadow-sm focus-within:border-blue-500 focus-within:ring-1 focus-within:ring-blue-500 dark:border-white/10 dark:bg-background">
            <span className="mr-4 text-lg font-black text-slate-500">¥</span>
            <input
              id="custom-amount"
              type="number"
              min="0.01"
              step="0.01"
              value={amountInput}
              onChange={(event) => setAmountInput(event.target.value)}
              className="h-full min-w-0 flex-1 bg-transparent text-lg font-black text-slate-950 outline-none dark:text-white"
            />
          </div>
        </div>
      </section>

      <section className="rounded-lg border border-slate-200 bg-white p-5 shadow-sm dark:border-white/10 dark:bg-card">
        <h2 className="mb-4 text-lg font-black text-slate-950 dark:text-white">支付方式</h2>
        <button
          type="button"
          className="flex min-h-16 w-full items-center gap-4 rounded-lg border border-blue-500 bg-blue-50/50 px-4 text-left ring-1 ring-blue-500 dark:bg-blue-500/10"
        >
          <span className="grid size-10 shrink-0 place-items-center rounded-lg bg-[#1677ff] text-xl font-black text-white">
            支
          </span>
          <span className="min-w-0 flex-1">
            <span className="block text-base font-black text-slate-950 dark:text-white">支付宝</span>
            <span className="block text-sm font-bold text-slate-500 dark:text-slate-400">推荐使用支付宝扫码支付</span>
          </span>
          <span className="grid size-8 place-items-center rounded-full bg-blue-600 text-white">
            <Check className="size-5" />
          </span>
        </button>
      </section>

      <section className="rounded-lg border border-slate-200 bg-white p-5 shadow-sm dark:border-white/10 dark:bg-card">
        <div className="grid gap-4 md:grid-cols-[1fr_auto_1fr_auto] md:items-center">
          <div>
            <div className="text-sm font-bold text-slate-500 dark:text-slate-400">充值金额（您将支付）</div>
            <div className="mt-3 text-3xl font-black text-slate-950 dark:text-white">{formatCny(amount)}</div>
          </div>
          <ChevronRight className="hidden size-8 text-slate-400 md:block" />
          <div>
            <div className="text-sm font-bold text-slate-500 dark:text-slate-400">到账金额（充值成功后得到到账）</div>
            <div className="mt-3 text-3xl font-black text-blue-600">{formatUsd(receiveAmount)}</div>
          </div>
          <div className="text-left md:text-right">
            <div className="text-sm font-bold text-slate-500 dark:text-slate-400">充值倍率</div>
            <div className="mt-3 text-lg font-black text-slate-950 dark:text-white">1 CNY = {RATE} USD</div>
          </div>
        </div>
      </section>

      <div className="fixed inset-x-4 bottom-4 z-10 md:left-[19rem] md:right-8 xl:right-10">
        <Button
          type="button"
          size="lg"
          disabled={!canSubmit}
          onClick={handleCreatePayment}
          className="h-14 w-full rounded-lg bg-blue-600 text-lg font-black text-white shadow-lg shadow-blue-600/20 hover:bg-blue-700"
        >
          {createPayment.isPending ? <Loader2 className="size-5 animate-spin" /> : <ShieldCheck className="size-5" />}
          确认支付 {formatCny(amount)}
        </Button>
      </div>

      <div className="flex items-center gap-2 px-1 text-sm font-semibold text-slate-500 dark:text-slate-400">
        <CreditCard className="size-4" />
        支付完成后系统会自动入账，可在我的订单查看订单状态。
      </div>
    </div>
  );
}
