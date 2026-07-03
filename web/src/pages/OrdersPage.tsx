import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useMemo, useState } from 'react';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { EmptyState } from '@/components/EmptyState';
import { TableSkeleton } from '@/components/LoadingStates';
import { adminApiClient, apiClient } from '@/lib/api';
import { unwrapApiData } from '@/lib/api-response';
import { formatAmountUnits } from '@/lib/amount';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';

interface LedgerLog {
  id?: string;
  type: string;
  amount: number;
  balance_after: number;
  reference_id?: string;
  remark?: string;
  created_at: number;
}

interface LedgerLogData {
  items?: LedgerLog[];
  logs?: LedgerLog[];
  total?: number;
}

interface PaymentOrder {
  id?: number | string;
  user_id?: string;
  userId?: string;
  trade_no?: string;
  tradeNo?: string;
  channel?: string;
  asset_type?: string;
  assetType?: string;
  asset_amount?: number;
  assetAmount?: number;
  money_cents?: number;
  moneyCents?: number;
  currency?: string;
  status?: string;
  provider_trade_no?: string;
  providerTradeNo?: string;
  asset_issue_status?: string;
  assetIssueStatus?: string;
  group_id?: number;
  groupId?: number;
  pay_url?: string;
  payUrl?: string;
  created_at?: number | string | { seconds?: number };
  createdAt?: number | string | { seconds?: number };
}

interface PaymentOrdersPayload {
  orders?: PaymentOrder[];
  total?: number;
}

interface PaymentOrderPayload {
  order?: PaymentOrder;
}

interface OrderRow {
  id: string;
  type: string;
  amount: string;
  balance: string;
  reference: string;
  remark: string;
  createdAt: number;
  userID?: string;
  status?: string;
  paymentOrder?: PaymentOrder;
}

const ORDER_TYPES = new Set(['recharge', 'redeem', 'refund']);

const TYPE_NAMES: Record<string, string> = {
  payment: '支付订单',
  recharge: '充值',
  redeem: '兑换',
  refund: '退款',
};

const STATUS_NAMES: Record<string, string> = {
  pending: '待支付',
  paid: '已支付',
  closed: '已关闭',
};

function normalizeLogs(data: LedgerLog[] | LedgerLogData): LedgerLog[] {
  if (Array.isArray(data)) return data;
  if (Array.isArray(data?.items)) return data.items;
  if (Array.isArray(data?.logs)) return data.logs;
  return [];
}

function numberValue(value: unknown): number {
  const parsed = Number(value ?? 0);
  return Number.isFinite(parsed) ? parsed : 0;
}

function formatQuota(value: number) {
  return formatAmountUnits(value);
}

function formatMoney(cents: unknown, currency?: string) {
  return `${currency || 'CNY'} ${(numberValue(cents) / 100).toFixed(2)}`;
}

function timestampSeconds(value: unknown): number {
  if (!value) return 0;
  if (typeof value === 'object' && 'seconds' in value) {
    return numberValue((value as { seconds?: number }).seconds);
  }
  return numberValue(value);
}

function formatDate(value: number) {
  if (!value) return '-';
  return new Date(value * 1000).toLocaleString();
}

function getTradeNo(order: PaymentOrder) {
  return order.trade_no || order.tradeNo || '-';
}

function getUserID(order: PaymentOrder) {
  return order.user_id || order.userId || '';
}

function getAssetAmount(order: PaymentOrder) {
  return numberValue(order.asset_amount ?? order.assetAmount);
}

function getAssetType(order: PaymentOrder) {
  return order.asset_type || order.assetType || 'balance';
}

function getGroupID(order: PaymentOrder) {
  return numberValue(order.group_id ?? order.groupId);
}

function getMoneyCents(order: PaymentOrder) {
  return numberValue(order.money_cents ?? order.moneyCents);
}

function getCreatedAt(order: PaymentOrder) {
  return timestampSeconds(order.created_at ?? order.createdAt);
}

function getPayURL(order: PaymentOrder) {
  return order.pay_url || order.payUrl || '';
}

function paymentOrderToRow(order: PaymentOrder): OrderRow {
  const tradeNo = getTradeNo(order);
  const issueStatus = order.asset_issue_status || order.assetIssueStatus || '-';
  const assetType = getAssetType(order);
  const assetLabel = assetType === 'subscription' ? `订阅分组 #${getGroupID(order) || '-'}` : formatQuota(getAssetAmount(order));
  return {
    id: `payment-${order.id || tradeNo}`,
    type: 'payment',
    amount: formatMoney(getMoneyCents(order), order.currency),
    balance: assetLabel,
    reference: tradeNo,
    remark: `状态：${STATUS_NAMES[order.status || ''] || order.status || '-'}；资产：${assetType === 'subscription' ? '订阅' : issueStatus}`,
    createdAt: getCreatedAt(order),
    userID: getUserID(order),
    status: order.status,
    paymentOrder: order,
  };
}

function ledgerToRow(log: LedgerLog, index: number): OrderRow {
  return {
    id: `ledger-${log.id || log.reference_id || log.created_at}-${index}`,
    type: log.type,
    amount: formatQuota(log.amount ?? 0),
    balance: formatQuota(log.balance_after ?? 0),
    reference: log.reference_id || '-',
    remark: log.remark || '-',
    createdAt: log.created_at,
  };
}

export function OrdersPage() {
  const [page, setPage] = useState(1);
  const [type, setType] = useState('');
  const [selectedOrder, setSelectedOrder] = useState<PaymentOrder | null>(null);
  const pageSize = 20;
  const adminToken = localStorage.getItem('adminToken');
  const isAdminView = Boolean(adminToken);
  const queryClient = useQueryClient();
  const queryKey = ['user-orders', page, type, isAdminView];

  const { data, isLoading } = useQuery({
    queryKey,
    queryFn: async () => {
      const params = new URLSearchParams({
        page: String(page),
        page_size: String(pageSize),
      });
      const paymentParams = new URLSearchParams(params);
      if (type === 'payment' || type === 'recharge') {
        paymentParams.set('channel', 'alipay');
      }
      const shouldLoadPayments = !type || type === 'payment' || type === 'recharge';
      const shouldLoadLedger = !type || type !== 'payment';

      const [paymentResult, ledgerResult] = await Promise.allSettled([
        shouldLoadPayments
          ? (isAdminView ? adminApiClient : apiClient).get(
              `${isAdminView ? '/payment/orders' : '/user/payment/orders'}?${paymentParams}`,
            )
          : Promise.resolve(null),
        shouldLoadLedger
          ? apiClient.get(`/user/logs?${params}${type ? `&type=${encodeURIComponent(type)}` : ''}`)
          : Promise.resolve(null),
      ]);

      const paymentRows =
        paymentResult.status === 'fulfilled' && paymentResult.value
          ? (unwrapApiData<PaymentOrdersPayload>(paymentResult.value.data).orders ?? []).map(paymentOrderToRow)
          : [];

      const ledgerRows =
        ledgerResult.status === 'fulfilled' && ledgerResult.value
          ? normalizeLogs(unwrapApiData<LedgerLog[] | LedgerLogData>(ledgerResult.value.data))
              .filter((log) => ORDER_TYPES.has(log.type))
              .filter((log) => !type || log.type === type)
              .map(ledgerToRow)
          : [];

      return [...paymentRows, ...ledgerRows].sort((a, b) => b.createdAt - a.createdAt);
    },
  });

  const paymentOrderDetail = useMutation({
    mutationFn: async (tradeNo: string) => {
      const client = isAdminView ? adminApiClient : apiClient;
      const path = isAdminView ? `/payment/orders/${encodeURIComponent(tradeNo)}` : `/user/payment/orders/${encodeURIComponent(tradeNo)}`;
      const res = await client.get(path);
      return unwrapApiData<PaymentOrderPayload>(res.data);
    },
    onSuccess: (payload) => {
      if (!payload.order) return;
      setSelectedOrder(payload.order);
      const updatedRow = paymentOrderToRow(payload.order);
      queryClient.setQueryData<OrderRow[]>(queryKey, (current) =>
        (current ?? []).map((row) => (row.reference === updatedRow.reference ? updatedRow : row)),
      );
    },
  });

  const rows = useMemo(() => data ?? [], [data]);
  const selectedPayURL = selectedOrder ? getPayURL(selectedOrder) : '';
  const canContinuePayment = selectedOrder?.status === 'pending' && selectedPayURL !== '';

  const handleOpenPaymentDetail = (row: OrderRow) => {
    if (!row.paymentOrder) return;
    setSelectedOrder(row.paymentOrder);
    paymentOrderDetail.mutate(row.reference);
  };

  const handleContinuePayment = () => {
    if (!selectedPayURL) return;
    if (selectedPayURL.startsWith('mock://')) return;
    window.open(selectedPayURL, '_blank', 'noopener,noreferrer');
  };

  return (
    <div className="space-y-5">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h2 className="text-2xl font-black tracking-normal text-slate-950 dark:text-white">我的订单</h2>
          <p className="mt-1 text-sm font-medium text-slate-500 dark:text-slate-400">
            {isAdminView ? '展示全部支付订单，并合并当前账号的兑换和退款记录。' : '展示支付充值、兑换和退款相关记录。'}
          </p>
        </div>
        <select
          value={type}
          onChange={(event) => {
            setType(event.target.value);
            setPage(1);
          }}
          className="h-10 rounded-lg border border-slate-200 bg-white px-3 text-sm font-semibold dark:border-white/10 dark:bg-background"
        >
          <option value="">全部订单</option>
          <option value="payment">支付订单</option>
          <option value="recharge">充值记录</option>
          <option value="redeem">兑换记录</option>
          <option value="refund">退款记录</option>
        </select>
      </div>

      {isLoading ? (
        <TableSkeleton columns={['类型', '金额', '到账/余额', '关联 ID', '备注', '时间']} rows={8} />
      ) : rows.length === 0 ? (
        <EmptyState title="暂无订单记录" description="充值、兑换或退款后会显示在这里。" />
      ) : (
        <>
          <div className="overflow-x-auto rounded-lg bg-white shadow-sm ring-1 ring-slate-200 dark:bg-card dark:ring-white/10">
            <Table>
              <TableHeader>
                <TableRow className="bg-slate-50 hover:bg-slate-50 dark:bg-white/5">
                  <TableHead>类型</TableHead>
                  {isAdminView && <TableHead>用户</TableHead>}
                  <TableHead>金额</TableHead>
                  <TableHead>到账/余额</TableHead>
                  <TableHead>关联 ID</TableHead>
                  <TableHead>备注</TableHead>
                  <TableHead>时间</TableHead>
                  <TableHead className="text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((row) => (
                  <TableRow key={row.id}>
                    <TableCell>
                      <span className="inline-flex rounded-md bg-emerald-100 px-2 py-1 text-xs font-bold text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300">
                        {TYPE_NAMES[row.type] || row.type || '-'}
                      </span>
                    </TableCell>
                    {isAdminView && <TableCell className="font-mono text-xs">{row.userID || '-'}</TableCell>}
                    <TableCell className="font-semibold">{row.amount}</TableCell>
                    <TableCell>{row.balance}</TableCell>
                    <TableCell className="font-mono text-xs">{row.reference}</TableCell>
                    <TableCell className="max-w-sm truncate">{row.remark}</TableCell>
                    <TableCell>{formatDate(row.createdAt)}</TableCell>
                    <TableCell className="text-right">
                      {row.paymentOrder ? (
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          disabled={paymentOrderDetail.isPending}
                          aria-label={`查看订单 ${row.reference}`}
                          onClick={() => handleOpenPaymentDetail(row)}
                        >
                          详情
                        </Button>
                      ) : (
                        '-'
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>

          <div className="flex items-center justify-end gap-2">
            <Button variant="outline" size="sm" onClick={() => setPage((value) => Math.max(1, value - 1))} disabled={page === 1}>
              上一页
            </Button>
            <span className="min-w-14 text-center text-sm text-muted-foreground">第 {page} 页</span>
            <Button variant="outline" size="sm" onClick={() => setPage((value) => value + 1)} disabled={rows.length < pageSize}>
              下一页
            </Button>
          </div>
        </>
      )}

      <Dialog open={Boolean(selectedOrder)} onOpenChange={(open) => !open && setSelectedOrder(null)}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>订单详情</DialogTitle>
            <DialogDescription>查看支付订单状态，未支付订单可继续打开原支付链接。</DialogDescription>
          </DialogHeader>

          {selectedOrder && (
            <div className="grid gap-3 rounded-lg border border-slate-200 bg-white p-4 text-sm dark:border-white/10 dark:bg-background">
              <DetailRow label="订单号" value={getTradeNo(selectedOrder)} mono />
              {isAdminView && <DetailRow label="用户" value={getUserID(selectedOrder) || '-'} mono />}
              <DetailRow label="支付渠道" value={selectedOrder.channel || '-'} />
              <DetailRow label="状态" value={STATUS_NAMES[selectedOrder.status || ''] || selectedOrder.status || '-'} />
              <DetailRow label="支付金额" value={formatMoney(getMoneyCents(selectedOrder), selectedOrder.currency)} />
              <DetailRow
                label={getAssetType(selectedOrder) === 'subscription' ? '订阅权益' : '到账额度'}
                value={getAssetType(selectedOrder) === 'subscription' ? `订阅分组 #${getGroupID(selectedOrder) || '-'}` : formatQuota(getAssetAmount(selectedOrder))}
              />
              <DetailRow label="资产状态" value={selectedOrder.asset_issue_status || selectedOrder.assetIssueStatus || '-'} />
              <DetailRow label="创建时间" value={formatDate(getCreatedAt(selectedOrder))} />
            </div>
          )}

          <DialogFooter>
            {selectedOrder?.status === 'pending' ? (
              <Button
                type="button"
                disabled={!canContinuePayment}
                onClick={handleContinuePayment}
                className="bg-blue-600 text-white hover:bg-blue-700"
              >
                继续支付
              </Button>
            ) : selectedOrder?.status === 'paid' ? (
              <div className="rounded-lg bg-emerald-50 px-4 py-2 text-sm font-bold text-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-300">
                订单已支付
              </div>
            ) : selectedOrder?.status === 'closed' ? (
              <div className="rounded-lg bg-slate-100 px-4 py-2 text-sm font-bold text-slate-600 dark:bg-white/10 dark:text-slate-300">
                订单已关闭
              </div>
            ) : null}
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function DetailRow({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="grid gap-1 sm:grid-cols-[6rem_1fr] sm:items-center">
      <div className="text-xs font-bold text-slate-500 dark:text-slate-400">{label}</div>
      <div className={mono ? 'break-all font-mono text-xs text-slate-950 dark:text-white' : 'break-all font-semibold text-slate-950 dark:text-white'}>
        {value}
      </div>
    </div>
  );
}
