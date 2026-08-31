import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { useMemo, useState } from 'react';
import { toast } from 'sonner';
import { locale, t } from '@/lib/i18n';
import { adminApiClient } from '@/lib/api';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { EmptyState } from '@/components/EmptyState';
import { TableSkeleton } from '@/components/LoadingStates';
import { AdminPagination } from '@/components/admin/AdminPagination';
import { ExportButton } from '@/components/admin/ExportButton';
import { SortableHeader } from '@/components/admin/SortableHeader';
import { useAdminTableState } from '@/hooks/useAdminTableState';
import { buildAdminListParams } from '@/lib/admin-table-query';
import { ensureApiSuccess, unwrapApiData } from '@/lib/api-response';
import { currencyUnitsToAmountUnits, formatAmountUnits } from '@/lib/amount';
import { sortRows, type SortState } from '@/lib/table-utils';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog';

interface RedeemCode {
  code: string;
  name: string;
  amount: string;
  count: number;
  status: number;
  createdBy: string;
  createdAt: string;
}

interface CreateRedemptionPayload {
  codes?: string[];
}

export function AdminRedemptionsPage() {
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
    storageKey: 'redemptions',
    filters: ['status'],
  });
  const [isCreateOpen, setIsCreateOpen] = useState(false);
  const [newCodeName, setNewCodeName] = useState('');
  const [newCodeAmount, setNewCodeAmount] = useState('');
  const [newCodeCount, setNewCodeCount] = useState('1');
  const [generatedCodes, setGeneratedCodes] = useState<string[]>([]);
  const queryClient = useQueryClient();
  const statusFilter = filters.status ?? '';
  const sort = { key: sortKey as keyof RedeemCode | null, direction: sortDirection } satisfies SortState<RedeemCode>;
  const exportParams = buildAdminListParams({
    page,
    pageSize,
    search,
    sortKey,
    sortDirection,
    filters: { status: statusFilter },
  });
  exportParams.set('format', 'csv');
  const exportHref = `/redemption/export?${exportParams}`;

  const { data: codes, isLoading } = useQuery({
    queryKey: ['admin-redemptions', page, pageSize, search, statusFilter, sortKey, sortDirection],
    queryFn: async () => {
      const params = buildAdminListParams({
        page,
        pageSize,
        search,
        sortKey,
        sortDirection,
        filters: { status: statusFilter },
      });
      const res = await adminApiClient.get(`/redemption?${params}`);
      return unwrapApiData<RedeemCode[]>(res.data);
    },
  });

  const createMutation = useMutation({
    mutationFn: async () => {
      const amount = currencyUnitsToAmountUnits(newCodeAmount);
      const count = parseInt(newCodeCount);
      const res = await adminApiClient.post('/redemption', {
        name: newCodeName,
        amount,
        count,
        batch_size: count,
      });
      const payload = unwrapApiData<CreateRedemptionPayload>(res.data, t('创建兑换码失败'));
      return payload.codes ?? [];
    },
    onSuccess: (codes) => {
      queryClient.invalidateQueries({ queryKey: ['admin-redemptions'] });
      setIsCreateOpen(false);
      setGeneratedCodes(codes);
      setNewCodeName('');
      setNewCodeAmount('');
      setNewCodeCount('1');
      toast.success(t('兑换码已创建'));
    },
  });

  const deleteMutation = useMutation({
    mutationFn: async (code: string) => {
      const res = await adminApiClient.delete(`/redemption/${code}`);
      ensureApiSuccess(res.data, t('删除兑换码失败'));
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin-redemptions'] });
      toast.success(t('兑换码已删除'));
    },
  });

  function formatAmount(q: string) {
    return formatAmountUnits(q);
  }

  const handleCreate = () => {
    if (newCodeName.trim() && newCodeAmount && parseFloat(newCodeAmount) > 0) {
      setGeneratedCodes([]);
      createMutation.mutate();
      return;
    }
    toast.error(t('名称和大于零的金额为必填项'));
  };

  const visibleCodes = useMemo(() => {
    return sortRows(codes ?? [], sort);
  }, [codes, sort]);

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-2xl font-semibold">{t('兑换码')}</h2>
        <Dialog open={isCreateOpen} onOpenChange={setIsCreateOpen}>
          <DialogTrigger render={<Button />}>
            {t('创建兑换码')}
          </DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>{t('创建兑换码')}</DialogTitle>
              <DialogDescription>{t('为用户生成新的兑换码。')}</DialogDescription>
            </DialogHeader>
            <div className="space-y-4 pt-4">
              <div className="space-y-2">
                <Label htmlFor="code-name">{t('名称')}</Label>
                <Input
                  id="code-name"
                  value={newCodeName}
                  onChange={(e) => setNewCodeName(e.target.value)}
                  placeholder={t('例如：新用户奖励')}
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="code-amount">{t('金额')}</Label>
                <Input
                  id="code-amount"
                  type="number"
                  step="0.01"
                  value={newCodeAmount}
                  onChange={(e) => setNewCodeAmount(e.target.value)}
                  placeholder={t('例如：10.00')}
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="code-count">{t('数量')}</Label>
                <Input
                  id="code-count"
                  type="number"
                  min="1"
                  value={newCodeCount}
                  onChange={(e) => setNewCodeCount(e.target.value)}
                />
              </div>
              <Button
                onClick={handleCreate}
                disabled={createMutation.isPending || !newCodeName.trim() || !newCodeAmount}
                className="w-full"
              >
                {createMutation.isPending ? t('创建中...') : t('创建')}
              </Button>
            </div>
          </DialogContent>
        </Dialog>
      </div>

      <div className="flex items-center gap-4">
        <Input
          placeholder={t('按兑换码或名称搜索...')}
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="max-w-sm"
        />
        <select
          value={statusFilter}
          onChange={(event) => setFilter('status', event.target.value)}
          className="h-8 rounded-md border bg-background px-2 text-sm"
          aria-label={t('按状态筛选兑换码')}
        >
          <option value="">{t('全部状态')}</option>
          <option value="1">{t('可用')}</option>
          <option value="2">{t('已使用')}</option>
        </select>
        <Button variant="outline" onClick={clearSearch}>
          {t('清除')}
        </Button>
        <div className="ml-auto">
          <ExportButton
            filename="admin-redemptions.csv"
            href={exportHref}
            rows={visibleCodes}
            columns={[
              { key: 'code', label: t('兑换码') },
              { key: 'name', label: t('名称') },
              { key: 'amount', label: t('金额') },
              { key: 'count', label: t('数量') },
              { key: 'status', label: t('状态') },
              { key: 'createdBy', label: t('创建人') },
              { key: 'createdAt', label: t('创建时间') },
            ]}
          />
        </div>
      </div>

      {generatedCodes.length > 0 && (
        <div className="rounded-lg border bg-muted/30 p-3">
          <div className="mb-2 text-sm font-medium">{t('已生成的兑换码')}</div>
          <div className="flex flex-wrap gap-2">
            {generatedCodes.map((code) => (
              <span key={code} className="rounded-md border bg-background px-2 py-1 font-mono text-sm">
                {code}
              </span>
            ))}
          </div>
        </div>
      )}

      {isLoading ? (
        <TableSkeleton columns={[t('兑换码'), t('名称'), t('金额'), t('数量'), t('状态'), t('创建人'), t('创建时间'), t('操作')]} />
      ) : !codes || codes.length === 0 ? (
        <EmptyState title={t('未找到兑换码')} description={t('请创建用于发放余额的兑换码，或清除搜索词。')} />
      ) : visibleCodes.length === 0 ? (
        <EmptyState title={t('没有兑换码符合筛选条件')} description={t('清除表格筛选条件以显示已加载的数据。')} />
      ) : (
        <>
          <div className="border rounded-lg overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <SortableHeader<RedeemCode> columnKey="code" sort={sort} onSortChange={setSort}>
                    {t('兑换码')}
                  </SortableHeader>
                  <SortableHeader<RedeemCode> columnKey="name" sort={sort} onSortChange={setSort}>
                    {t('名称')}
                  </SortableHeader>
                  <SortableHeader<RedeemCode> columnKey="amount" sort={sort} onSortChange={setSort}>
                    {t('金额')}
                  </SortableHeader>
                  <TableHead>{t('数量')}</TableHead>
                  <SortableHeader<RedeemCode> columnKey="status" sort={sort} onSortChange={setSort}>
                    {t('状态')}
                  </SortableHeader>
                  <TableHead className="hidden md:table-cell">{t('创建人')}</TableHead>
                  <SortableHeader<RedeemCode> columnKey="createdAt" sort={sort} onSortChange={setSort}>
                    {t('创建时间')}
                  </SortableHeader>
                  <TableHead className="text-right">{t('操作')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {visibleCodes.map((code) => (
                  <TableRow key={code.code}>
                    <TableCell className="font-mono text-sm">{code.code}</TableCell>
                    <TableCell className="font-medium">{code.name}</TableCell>
                    <TableCell>{formatAmount(code.amount)}</TableCell>
                    <TableCell>{code.count}</TableCell>
                    <TableCell>
                      <span
                        className={`inline-flex items-center px-2 py-1 rounded-full text-xs font-medium ${
                          code.status === 1
                            ? 'bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200'
                            : 'bg-gray-100 text-gray-800 dark:bg-gray-900 dark:text-gray-200'
                        }`}
                      >
                        {code.status === 1 ? t('可用') : t('已使用')}
                      </span>
                    </TableCell>
                    <TableCell className="hidden md:table-cell">{code.createdBy || '—'}</TableCell>
                    <TableCell>
                      {new Date(parseInt(code.createdAt) * 1000).toLocaleDateString(locale())}
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        variant="destructive"
                        size="sm"
                        onClick={() => deleteMutation.mutate(code.code)}
                        disabled={deleteMutation.isPending}
                      >
                        {t('删除')}
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>

          <AdminPagination
            page={page}
            pageSize={pageSize}
            hasNextPage={!!codes && codes.length >= pageSize}
            onPageChange={setPage}
            onPageSizeChange={setPageSize}
          />
        </>
      )}
    </div>
  );
}
