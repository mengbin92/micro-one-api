import { safePaymentURL, safeWechatPaymentURL } from '@/lib/payment-url';
import { useEffect, useMemo, useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
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
import { WechatPaymentCode } from '@/components/WechatPaymentCode';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';

const DEFAULT_RECHARGE_AMOUNT_MULTIPLIER = 10;
const RATE = rechargeAmountMultiplier();
const PRESET_AMOUNTS = [2, 10, 20, 50, 100];

interface PaymentOrder {
  trade_no?: string;
  pay_url?: string;
  channel?: string;
  status?: string;
}

interface PaymentChannel {
  channel: string;
  display_name?: string;
  currency: string;
  asset_types?: string[];
}

interface CreatePaymentResponse {
  trade_no?: string;
  pay_url?: string;
  order?: PaymentOrder;
}

interface PaymentMutationVariables {
  paymentWindow: Window | null;
  channel: string;
  amount: number;
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
  const queryClient = useQueryClient();
  const [amountInput, setAmountInput] = useState('20');
  const amount = normalizeAmount(amountInput);
  const receiveAmount = useMemo(() => amount * RATE, [amount]);

  const { data: dashboard } = useQuery(accountDashboardQueryOptions);
  const [selectedChannel, setSelectedChannel] = useState('');
  const [wechatOrder, setWechatOrder] = useState<PaymentOrder | null>(null);
  const attempt = useRef<{ scope: string; key: string } | null>(null);
  const channelsQuery = useQuery({
    queryKey: ['payment-channels'],
    queryFn: async () => {
      const res = await apiClient.get('/user/payment/channels');
      return unwrapApiData<{ channels?: PaymentChannel[] }>(res.data).channels ?? [];
    },
  });
  const channels = (channelsQuery.data ?? []).filter(value => value.currency === 'CNY'
    && value.asset_types?.includes('balance') && ['alipay', 'wechat'].includes(value.channel));
  const channel = channels.find(value => value.channel === selectedChannel)?.channel ?? channels[0]?.channel ?? '';
  const orderQuery = useQuery({
    queryKey: ['wechat-payment-order', wechatOrder?.trade_no],
    enabled: Boolean(wechatOrder?.trade_no),
    queryFn: async () => {
      const res = await apiClient.get(`/user/payment/orders/${encodeURIComponent(wechatOrder!.trade_no!)}`);
      return unwrapApiData<{ order: PaymentOrder }>(res.data).order;
    },
    refetchInterval: query => ['paid', 'closed', 'refunded'].includes(query.state.data?.status ?? '') ? false : 3000,
  });
  const paymentStatus = orderQuery.data?.status ?? wechatOrder?.status;
  const wechatPayURL = orderQuery.data?.pay_url || wechatOrder?.pay_url;
  useEffect(() => {
    if (paymentStatus === 'paid') void queryClient.invalidateQueries({ queryKey: accountDashboardQueryOptions.queryKey });
  }, [paymentStatus, queryClient]);

  const createPayment = useMutation({
    mutationFn: async (variables: PaymentMutationVariables) => {
      const scope = `${variables.channel}:${variables.amount}`;
      if (attempt.current?.scope !== scope) attempt.current = { scope, key: crypto.randomUUID() };
      const res = await apiClient.post('/user/pay', {
        amount: variables.amount,
        payment_method: variables.channel,
      }, { headers: { 'Idempotency-Key': attempt.current.key } });
      return unwrapApiData<CreatePaymentResponse>(res.data, t("创建支付订单失败"));
    },
    onSuccess: (data, variables) => {
      const payURL = data.pay_url || data.order?.pay_url;
      if (variables.channel === 'wechat') {
        variables.paymentWindow?.close();
        const tradeNo = data.trade_no || data.order?.trade_no;
        if (!tradeNo) { toast.error(t('创建支付订单失败')); return; }
        setWechatOrder({ ...data.order, trade_no: tradeNo, pay_url: (payURL && safeWechatPaymentURL(payURL)) || undefined });
        return;
      }
      attempt.current = null;
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
      const url = safePaymentURL(payURL);
      if (!url) { variables.paymentWindow?.close(); toast.error(t('支付地址无效')); return; }
      if (variables.paymentWindow) {
        variables.paymentWindow.location.href = url;
        return;
      }
      window.open(url, '_blank', 'noopener,noreferrer');
    },
    onError: (_error, variables) => {
      variables.paymentWindow?.close();
    },
  });

  const canSubmit = amount > 0 && amount <= 1_000_000 && Boolean(channel) && !channelsQuery.isError && !createPayment.isPending;

  const handleCreatePayment = () => {
    const paymentWindow = channel === 'wechat' ? null : window.open('about:blank', '_blank');
    if (paymentWindow) {
      paymentWindow.opener = null;
      paymentWindow.document.title = t("正在前往支付");
      paymentWindow.document.body.textContent = t("正在创建支付订单，请稍候...");
    }
    createPayment.mutate({ paymentWindow, channel, amount });
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
        {channelsQuery.isPending ? <p role="status">{t('正在加载支付方式')}</p>
          : channelsQuery.isError ? <p role="alert">{t('支付方式暂不可用，请稍后重试')}</p>
            : channels.length === 0 ? <p role="status">{t('暂无可用支付方式')}</p> : (
              <div className="grid gap-3 sm:grid-cols-2">
                {channels.map(value => (
                  <button key={value.channel} type="button" aria-pressed={channel === value.channel}
                    disabled={createPayment.isPending} onClick={() => setSelectedChannel(value.channel)}
                    className={cn('flex min-h-16 items-center gap-4 rounded-lg border px-4 text-left focus-visible:outline-2 focus-visible:outline-ring',
                      channel === value.channel ? 'border-primary bg-accent' : 'border-border bg-card')}>
                    <CreditCard className="size-6 text-primary" />
                    <span className="flex-1 font-semibold">{t(value.channel === 'wechat' ? '微信支付' : '支付宝')}</span>
                    {channel === value.channel && <Check className="size-5 text-primary" />}
                  </button>
                ))}
              </div>
            )}
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
      <Dialog open={Boolean(wechatOrder)} onOpenChange={open => {
        if (!open) {
          if (['paid', 'closed', 'refunded'].includes(paymentStatus ?? '')) attempt.current = null;
          setWechatOrder(null);
        }
      }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('微信支付')}</DialogTitle>
            <DialogDescription>{t('订单号')}：{wechatOrder?.trade_no}</DialogDescription>
          </DialogHeader>
          {paymentStatus === 'paid' ? <p role="status">{t('订单已支付')}</p>
            : paymentStatus === 'closed' || paymentStatus === 'refunded' ? <p role="status">{t('订单已关闭')}</p>
              : wechatPayURL ? <WechatPaymentCode value={wechatPayURL} />
                : <p role="status">{t('下单结果暂未确认，请先刷新订单状态。再次尝试将复用本次订单。')}</p>}
          {orderQuery.isError && <p role="alert">{t('支付结果暂未确认，请刷新状态或在我的订单中核对。')}</p>}
          <Button variant="outline" disabled={orderQuery.isFetching} onClick={() => void orderQuery.refetch()}>{t('刷新支付状态')}</Button>
          {['paid', 'closed', 'refunded'].includes(paymentStatus ?? '') && (
            <Button onClick={() => { attempt.current = null; setWechatOrder(null); }}>{t('创建新充值订单')}</Button>
          )}
        </DialogContent>
      </Dialog>
    </div>
  );
}
