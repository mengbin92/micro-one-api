import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { useMemo, useState } from 'react';
import { toast } from 'sonner';
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
      return res.data.data as RedeemCode[];
    },
  });

  const createMutation = useMutation({
    mutationFn: async () => {
      const amount = Math.floor(parseFloat(newCodeAmount) * 500000);
      const count = parseInt(newCodeCount);
      const res = await adminApiClient.post('/redemption', {
        name: newCodeName,
        amount,
        count,
        batch_size: count,
      });
      return (res.data?.data?.codes ?? res.data?.codes ?? []) as string[];
    },
    onSuccess: (codes) => {
      queryClient.invalidateQueries({ queryKey: ['admin-redemptions'] });
      setIsCreateOpen(false);
      setGeneratedCodes(codes);
      setNewCodeName('');
      setNewCodeAmount('');
      setNewCodeCount('1');
      toast.success('Redemption code created');
    },
  });

  const deleteMutation = useMutation({
    mutationFn: async (code: string) => {
      await adminApiClient.delete(`/redemption/${code}`);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin-redemptions'] });
      toast.success('Redemption code deleted');
    },
  });

  function formatQuota(q: string) {
    return (parseInt(q || '0') / 500000).toFixed(2);
  }

  const handleCreate = () => {
    if (newCodeName.trim() && newCodeAmount && parseFloat(newCodeAmount) > 0) {
      setGeneratedCodes([]);
      createMutation.mutate();
      return;
    }
    toast.error('Name and a positive quota amount are required');
  };

  const visibleCodes = useMemo(() => {
    return sortRows(codes ?? [], sort);
  }, [codes, sort]);

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-2xl font-semibold">Redemption Codes</h2>
        <Dialog open={isCreateOpen} onOpenChange={setIsCreateOpen}>
          <DialogTrigger render={<Button />}>
            Create Code
          </DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Create Redemption Code</DialogTitle>
              <DialogDescription>Generate new redemption codes for users.</DialogDescription>
            </DialogHeader>
            <div className="space-y-4 pt-4">
              <div className="space-y-2">
                <Label htmlFor="code-name">Name</Label>
                <Input
                  id="code-name"
                  value={newCodeName}
                  onChange={(e) => setNewCodeName(e.target.value)}
                  placeholder="e.g., Welcome Bonus"
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="code-amount">Amount (quota)</Label>
                <Input
                  id="code-amount"
                  type="number"
                  step="0.01"
                  value={newCodeAmount}
                  onChange={(e) => setNewCodeAmount(e.target.value)}
                  placeholder="e.g., 10.00"
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="code-count">Count</Label>
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
                {createMutation.isPending ? 'Creating...' : 'Create'}
              </Button>
            </div>
          </DialogContent>
        </Dialog>
      </div>

      <div className="flex items-center gap-4">
        <Input
          placeholder="Search by code or name..."
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="max-w-sm"
        />
        <select
          value={statusFilter}
          onChange={(event) => setFilter('status', event.target.value)}
          className="h-8 rounded-md border bg-background px-2 text-sm"
          aria-label="Filter redemption codes by status"
        >
          <option value="">All statuses</option>
          <option value="1">Active</option>
          <option value="2">Used</option>
        </select>
        <Button variant="outline" onClick={clearSearch}>
          Clear
        </Button>
        <div className="ml-auto">
          <ExportButton
            filename="admin-redemptions.csv"
            href={exportHref}
            rows={visibleCodes}
            columns={[
              { key: 'code', label: 'Code' },
              { key: 'name', label: 'Name' },
              { key: 'amount', label: 'Amount' },
              { key: 'count', label: 'Count' },
              { key: 'status', label: 'Status' },
              { key: 'createdBy', label: 'Created By' },
              { key: 'createdAt', label: 'Created At' },
            ]}
          />
        </div>
      </div>

      {generatedCodes.length > 0 && (
        <div className="rounded-lg border bg-muted/30 p-3">
          <div className="mb-2 text-sm font-medium">Generated Codes</div>
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
        <TableSkeleton columns={['Code', 'Name', 'Amount', 'Count', 'Status', 'Created By', 'Created At', 'Actions']} />
      ) : !codes || codes.length === 0 ? (
        <EmptyState title="No redemption codes found" description="Create codes for quota grants or clear the search term." />
      ) : visibleCodes.length === 0 ? (
        <EmptyState title="No redemption codes match the filters" description="Clear the table filters to show the loaded rows." />
      ) : (
        <>
          <div className="border rounded-lg overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <SortableHeader<RedeemCode> columnKey="code" sort={sort} onSortChange={setSort}>
                    Code
                  </SortableHeader>
                  <SortableHeader<RedeemCode> columnKey="name" sort={sort} onSortChange={setSort}>
                    Name
                  </SortableHeader>
                  <SortableHeader<RedeemCode> columnKey="amount" sort={sort} onSortChange={setSort}>
                    Amount
                  </SortableHeader>
                  <TableHead>Count</TableHead>
                  <SortableHeader<RedeemCode> columnKey="status" sort={sort} onSortChange={setSort}>
                    Status
                  </SortableHeader>
                  <TableHead className="hidden md:table-cell">Created By</TableHead>
                  <SortableHeader<RedeemCode> columnKey="createdAt" sort={sort} onSortChange={setSort}>
                    Created At
                  </SortableHeader>
                  <TableHead className="text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {visibleCodes.map((code) => (
                  <TableRow key={code.code}>
                    <TableCell className="font-mono text-sm">{code.code}</TableCell>
                    <TableCell className="font-medium">{code.name}</TableCell>
                    <TableCell>{formatQuota(code.amount)}</TableCell>
                    <TableCell>{code.count}</TableCell>
                    <TableCell>
                      <span
                        className={`inline-flex items-center px-2 py-1 rounded-full text-xs font-medium ${
                          code.status === 1
                            ? 'bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200'
                            : 'bg-gray-100 text-gray-800 dark:bg-gray-900 dark:text-gray-200'
                        }`}
                      >
                        {code.status === 1 ? 'Active' : 'Used'}
                      </span>
                    </TableCell>
                    <TableCell className="hidden md:table-cell">{code.createdBy || '—'}</TableCell>
                    <TableCell>
                      {new Date(parseInt(code.createdAt) * 1000).toLocaleDateString()}
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        variant="destructive"
                        size="sm"
                        onClick={() => deleteMutation.mutate(code.code)}
                        disabled={deleteMutation.isPending}
                      >
                        Delete
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
