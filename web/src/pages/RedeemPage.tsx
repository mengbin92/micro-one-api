import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Gift, Loader2, Ticket, WalletCards } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { apiClient } from '@/lib/api';
import { unwrapApiData } from '@/lib/api-response';
import { formatUSD } from '@/lib/amount';

interface AccountDashboard {
  balance?: number;
}

function formatAmount(value?: number) {
  return formatUSD(value);
}

export function RedeemPage() {
  const [code, setCode] = useState('');
  const [redeemedAmount, setRedeemedAmount] = useState<number | null>(null);
  const queryClient = useQueryClient();

  const { data: dashboard } = useQuery({
    queryKey: ['redeem-dashboard'],
    queryFn: async () => {
      const res = await apiClient.get('/user/dashboard');
      return unwrapApiData<AccountDashboard>(res.data);
    },
  });

  const redeemMutation = useMutation({
    mutationFn: async () => {
      const res = await apiClient.post('/user/topup', { key: code.trim() });
      return unwrapApiData<number>(res.data, '兑换失败');
    },
    onSuccess: (amount) => {
      setRedeemedAmount(amount);
      setCode('');
      queryClient.invalidateQueries({ queryKey: ['redeem-dashboard'] });
      queryClient.invalidateQueries({ queryKey: ['dashboard'] });
      toast.success(`兑换成功：${formatAmount(amount)}`);
    },
  });

  const handleRedeem = () => {
    if (!code.trim()) {
      toast.error('请输入兑换码');
      return;
    }
    setRedeemedAmount(null);
    redeemMutation.mutate();
  };

  return (
    <div className="mx-auto flex max-w-4xl flex-col gap-4">
      <section className="overflow-hidden rounded-lg border border-violet-100 bg-white shadow-sm dark:border-white/10 dark:bg-card">
        <div className="grid gap-0 md:grid-cols-[1fr_0.8fr]">
          <div className="p-6 sm:p-8">
            <div className="mb-5 grid size-12 place-items-center rounded-lg bg-violet-50 text-violet-600 dark:bg-violet-500/10 dark:text-violet-300">
              <Gift className="size-6" />
            </div>
            <h2 className="text-3xl font-black tracking-normal text-slate-950 dark:text-white">兑换码充值</h2>
            <p className="mt-2 max-w-xl text-sm font-medium text-slate-500 dark:text-slate-400">
              输入管理员发放的兑换码，成功后金额会立即入账。
            </p>

            <div className="mt-8 space-y-3">
              <Label htmlFor="redeem-code">兑换码</Label>
              <div className="flex flex-col gap-3 sm:flex-row">
                <Input
                  id="redeem-code"
                  value={code}
                  onChange={(event) => setCode(event.target.value)}
                  placeholder="CODE-XXXX"
                  className="h-11 font-mono text-base uppercase"
                />
                <Button
                  type="button"
                  size="lg"
                  onClick={handleRedeem}
                  disabled={redeemMutation.isPending || !code.trim()}
                  className="h-11 shrink-0 bg-violet-600 text-white hover:bg-violet-700"
                >
                  {redeemMutation.isPending ? <Loader2 className="size-5 animate-spin" /> : <Ticket className="size-5" />}
                  立即兑换
                </Button>
              </div>
            </div>

            {redeemedAmount !== null && (
              <div className="mt-5 rounded-lg border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm font-bold text-emerald-700 dark:border-emerald-500/30 dark:bg-emerald-500/10 dark:text-emerald-300">
                兑换成功，已到账 {formatAmount(redeemedAmount)}
              </div>
            )}
          </div>

          <div className="flex flex-col justify-center bg-violet-600 p-6 text-white sm:p-8">
            <div className="flex items-center gap-3 text-sm font-bold text-violet-100">
              <WalletCards className="size-5" />
              当前余额
            </div>
            <div className="mt-4 text-5xl font-black">{formatAmount(dashboard?.balance)}</div>
            <div className="mt-3 text-sm font-semibold text-violet-100">兑换后可在使用记录和订单中查看账务流水。</div>
          </div>
        </div>
      </section>
    </div>
  );
}
