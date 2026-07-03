import { useQuery } from '@tanstack/react-query';
import { CreditCard } from 'lucide-react';
import { useMemo } from 'react';
import { EmptyState } from '@/components/EmptyState';
import { TableSkeleton } from '@/components/LoadingStates';
import { AdminPagination } from '@/components/admin/AdminPagination';
import { AdminTableToolbar } from '@/components/admin/AdminTableToolbar';
import { SortableHeader } from '@/components/admin/SortableHeader';
import { useAdminTableState } from '@/hooks/useAdminTableState';
import { buildAdminListParams } from '@/lib/admin-table-query';
import { adminApiClient } from '@/lib/api';
import { unwrapApiData } from '@/lib/api-response';
import { formatAmountUnits } from '@/lib/amount';
import { sortRows, type SortState } from '@/lib/table-utils';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';

interface PaymentOrder {
  id: number | string;
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
  paid_at?: string | { seconds?: number };
  paidAt?: string | { seconds?: number };
  created_at?: string | { seconds?: number };
  createdAt?: string | { seconds?: number };
}

interface PaymentOrdersPayload {
  orders?: PaymentOrder[];
  total?: number;
}

const STATUS_NAMES: Record<string, string> = {
  pending: '待支付',
  paid: '已支付',
  closed: '已关闭',
};

const CHANNEL_NAMES: Record<string, string> = {
  alipay: '支付宝',
  mock: 'Mock',
};

function numberValue(value: unknown): number {
  const parsed = Number(value ?? 0);
  return Number.isFinite(parsed) ? parsed : 0;
}

function formatQuota(value: unknown) {
  return formatAmountUnits(numberValue(value));
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

function formatDate(value: unknown) {
  const seconds = timestampSeconds(value);
  if (!seconds) return '-';
  return new Date(seconds * 1000).toLocaleString();
}

function getTradeNo(order: PaymentOrder) {
  return order.trade_no || order.tradeNo || '-';
}

function getUserID(order: PaymentOrder) {
  return order.user_id || order.userId || '-';
}

function getAssetAmount(order: PaymentOrder) {
  return order.asset_amount ?? order.assetAmount ?? 0;
}

function getAssetType(order: PaymentOrder) {
  return order.asset_type || order.assetType || 'balance';
}

function getGroupID(order: PaymentOrder) {
  return numberValue(order.group_id ?? order.groupId);
}

function formatAsset(order: PaymentOrder) {
  if (getAssetType(order) === 'subscription') {
    return `订阅分组 #${getGroupID(order) || '-'}`;
  }
  return formatQuota(getAssetAmount(order));
}

function getMoneyCents(order: PaymentOrder) {
  return order.money_cents ?? order.moneyCents ?? 0;
}

function getProviderTradeNo(order: PaymentOrder) {
  return order.provider_trade_no || order.providerTradeNo || '-';
}

function getCreatedAt(order: PaymentOrder) {
  return order.created_at ?? order.createdAt;
}

export function AdminPaymentOrdersPage() {
  const {
    page,
    pageSize,
    search,
    sortKey,
    sortDirection,
    filters,
    setPage,
    setPageSize,
    setSearch,
    clearSearch,
    setSort,
    setFilter,
  } = useAdminTableState({
    storageKey: 'payment-orders',
    filters: ['status', 'channel', 'user_id'],
  });
  const statusFilter = filters.status ?? '';
  const channelFilter = filters.channel ?? '';
  const userIDFilter = filters.user_id ?? '';
  const sort = { key: sortKey as keyof PaymentOrder | null, direction: sortDirection } satisfies SortState<PaymentOrder>;

  const { data, isLoading } = useQuery({
    queryKey: ['admin-payment-orders', page, pageSize, search, sortKey, sortDirection, filters],
    queryFn: async () => {
      const params = buildAdminListParams({
        page,
        pageSize,
        search,
        sortKey,
        sortDirection,
        filters,
      });
      if (search) {
        params.set('trade_no', search);
        params.delete('keyword');
      }
      const res = await adminApiClient.get(`/payment/orders?${params}`);
      return unwrapApiData<PaymentOrdersPayload>(res.data);
    },
  });

  const orders = useMemo(() => data?.orders ?? [], [data?.orders]);
  const visibleOrders = useMemo(() => sortRows(orders, sort), [orders, sort]);

  return (
    <div className="space-y-5">
      <div>
        <h2 className="flex items-center gap-2 text-2xl font-black tracking-normal text-slate-950 dark:text-white">
          <CreditCard className="size-6" />
          支付订单
        </h2>
        <p className="mt-1 text-sm font-medium text-slate-500 dark:text-slate-400">
          查看所有用户的支付订单、支付状态、订单金额和资产发放状态。
        </p>
      </div>

      <AdminTableToolbar
        search={search}
        searchPlaceholder="搜索商户订单号或渠道订单号..."
        onSearchChange={setSearch}
        onClear={clearSearch}
      />

      <div className="flex flex-wrap items-center gap-3">
        <select
          value={statusFilter}
          onChange={(event) => setFilter('status', event.target.value)}
          className="h-8 rounded-md border bg-background px-2 text-sm"
          aria-label="Filter payment orders by status"
        >
          <option value="">全部状态</option>
          <option value="pending">待支付</option>
          <option value="paid">已支付</option>
          <option value="closed">已关闭</option>
        </select>
        <select
          value={channelFilter}
          onChange={(event) => setFilter('channel', event.target.value)}
          className="h-8 rounded-md border bg-background px-2 text-sm"
          aria-label="Filter payment orders by channel"
        >
          <option value="">全部渠道</option>
          <option value="alipay">支付宝</option>
          <option value="mock">Mock</option>
        </select>
        <input
          value={userIDFilter}
          onChange={(event) => setFilter('user_id', event.target.value.trim())}
          placeholder="用户 ID"
          className="h-8 w-36 rounded-md border bg-background px-2 text-sm"
          aria-label="Filter payment orders by user id"
        />
      </div>

      {isLoading ? (
        <TableSkeleton columns={['订单号', '用户', '渠道', '状态', '金额', '资产', '渠道单号', '创建时间']} rows={8} />
      ) : visibleOrders.length === 0 ? (
        <EmptyState title="暂无支付订单" description="用户发起充值或订阅支付后会显示在这里。" />
      ) : (
        <>
          <div className="overflow-x-auto rounded-lg bg-white shadow-sm ring-1 ring-slate-200 dark:bg-card dark:ring-white/10">
            <Table className="min-w-[1120px]">
              <TableHeader>
                <TableRow className="bg-slate-50 hover:bg-slate-50 dark:bg-white/5">
                  <SortableHeader<PaymentOrder> columnKey="trade_no" sort={sort} onSortChange={setSort}>
                    订单号
                  </SortableHeader>
                  <SortableHeader<PaymentOrder> columnKey="user_id" sort={sort} onSortChange={setSort}>
                    用户
                  </SortableHeader>
                  <TableHead>渠道</TableHead>
                  <SortableHeader<PaymentOrder> columnKey="status" sort={sort} onSortChange={setSort}>
                    状态
                  </SortableHeader>
                  <TableHead>金额</TableHead>
                  <TableHead>资产</TableHead>
                  <TableHead>渠道单号</TableHead>
                  <SortableHeader<PaymentOrder> columnKey="created_at" sort={sort} onSortChange={setSort}>
                    创建时间
                  </SortableHeader>
                </TableRow>
              </TableHeader>
              <TableBody>
                {visibleOrders.map((order) => (
                  <TableRow key={order.id || getTradeNo(order)}>
                    <TableCell className="font-mono text-xs">{getTradeNo(order)}</TableCell>
                    <TableCell className="font-mono text-xs">{getUserID(order)}</TableCell>
                    <TableCell>{CHANNEL_NAMES[order.channel || ''] || order.channel || '-'}</TableCell>
                    <TableCell>
                      <span className="inline-flex rounded-md bg-blue-100 px-2 py-1 text-xs font-bold text-blue-700 dark:bg-blue-500/15 dark:text-blue-300">
                        {STATUS_NAMES[order.status || ''] || order.status || '-'}
                      </span>
                    </TableCell>
                    <TableCell className="font-semibold">{formatMoney(getMoneyCents(order), order.currency)}</TableCell>
                    <TableCell>
                      <div className="font-semibold">{formatAsset(order)}</div>
                      <div className="text-xs text-slate-400">
                        {getAssetType(order) === 'subscription' ? 'subscription' : order.asset_issue_status || order.assetIssueStatus || '-'}
                      </div>
                    </TableCell>
                    <TableCell className="font-mono text-xs">{getProviderTradeNo(order)}</TableCell>
                    <TableCell>{formatDate(getCreatedAt(order))}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>

          <AdminPagination
            page={page}
            pageSize={pageSize}
            hasNextPage={orders.length >= pageSize}
            onPageChange={setPage}
            onPageSizeChange={setPageSize}
          />
        </>
      )}
    </div>
  );
}
