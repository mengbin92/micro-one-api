import { useQuery } from '@tanstack/react-query';
import { useMemo } from 'react';
import { adminApiClient } from '@/lib/api';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { EmptyState } from '@/components/EmptyState';
import { TableSkeleton } from '@/components/LoadingStates';
import { AdminPagination } from '@/components/admin/AdminPagination';
import { ExportButton } from '@/components/admin/ExportButton';
import { SortableHeader } from '@/components/admin/SortableHeader';
import { useAdminTableState } from '@/hooks/useAdminTableState';
import { buildAdminListParams } from '@/lib/admin-table-query';
import { unwrapApiData } from '@/lib/api-response';
import { sortRows, type SortState } from '@/lib/table-utils';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';

interface LogEntry {
  id: string;
  userId: string;
  type: string;
  amount: string;
  balanceAfter: string;
  referenceId: string;
  remark: string;
  createdAt: string;
}

interface LogListData {
  logs?: LogEntry[];
  total?: number;
}

const EMPTY_LOGS: LogEntry[] = [];

const LOG_TYPE_NAMES: Record<string, string> = {
  redeem: 'Redeem',
  recharge: 'Recharge',
  consume: 'Consume',
  refund: 'Refund',
};

export function AdminLogsPage() {
  const {
    page,
    pageSize,
    sortKey,
    sortDirection,
    filters,
    setPage,
    setPageSize,
    setSort,
    setFilter,
  } = useAdminTableState({
    storageKey: 'logs',
    defaultPageSize: 50,
    filters: ['user_id', 'type'],
  });
  const userId = filters.user_id ?? '';
  const type = filters.type ?? '';
  const sort = useMemo(
    () => ({ key: sortKey as keyof LogEntry | null, direction: sortDirection }) satisfies SortState<LogEntry>,
    [sortKey, sortDirection],
  );
  const exportParams = buildAdminListParams({
    page,
    pageSize,
    sortKey,
    sortDirection,
    filters: { user_id: userId, type },
  });
  exportParams.set('format', 'csv');
  const exportHref = `/log/export?${exportParams}`;

  const { data, isLoading } = useQuery({
    queryKey: ['admin-logs', page, pageSize, userId, type, sortKey, sortDirection],
    queryFn: async () => {
      const params = buildAdminListParams({
        page,
        pageSize,
        sortKey,
        sortDirection,
        filters: { user_id: userId, type },
      });
      const res = await adminApiClient.get(`/log?${params}`);
      const payload = unwrapApiData<LogEntry[] | LogListData>(res.data);
      return Array.isArray(payload)
        ? { logs: payload, total: payload.length }
        : { logs: payload.logs ?? [], total: payload.total ?? payload.logs?.length ?? 0 };
    },
  });

  function formatQuota(q: string) {
    return (parseInt(q || '0') / 500000).toFixed(2);
  }

  const logs = data?.logs ?? EMPTY_LOGS;
  const total = data?.total ?? logs.length;
  const visibleLogs = useMemo(() => sortRows(logs, sort), [logs, sort]);

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-2xl font-semibold">Billing Logs</h2>
      </div>

      <div className="flex items-center gap-4">
        <Input
          placeholder="User ID"
          value={userId}
          onChange={(e) => setFilter('user_id', e.target.value.trim())}
          className="max-w-xs"
        />
        <select
          value={type}
          onChange={(e) => setFilter('type', e.target.value)}
          className="border rounded px-3 py-2 text-sm"
        >
          <option value="">All Types</option>
          <option value="redeem">Redeem</option>
          <option value="recharge">Recharge</option>
          <option value="consume">Consume</option>
          <option value="refund">Refund</option>
        </select>
        <Button
          variant="outline"
          onClick={() => {
            setFilter('user_id', '');
            setFilter('type', '');
          }}
        >
          Clear
        </Button>
        <div className="ml-auto">
          <ExportButton
            filename="admin-billing-logs.csv"
            href={exportHref}
            rows={visibleLogs}
            columns={[
              { key: 'id', label: 'ID' },
              { key: 'userId', label: 'User ID' },
              { key: 'type', label: 'Type' },
              { key: 'amount', label: 'Amount' },
              { key: 'balanceAfter', label: 'Balance After' },
              { key: 'referenceId', label: 'Reference' },
              { key: 'remark', label: 'Remark' },
              { key: 'createdAt', label: 'Created At' },
            ]}
          />
        </div>
      </div>

      {isLoading ? (
        <TableSkeleton columns={['ID', 'User ID', 'Type', 'Amount', 'Balance After', 'Reference', 'Remark', 'Created At']} rows={8} />
      ) : !logs || logs.length === 0 ? (
        <EmptyState title="No logs found" description="Adjust the filters or check back after billing events are recorded." />
      ) : (
        <>
          <div className="border rounded-lg overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>ID</TableHead>
                  <SortableHeader<LogEntry> columnKey="userId" sort={sort} onSortChange={setSort}>
                    User ID
                  </SortableHeader>
                  <SortableHeader<LogEntry> columnKey="type" sort={sort} onSortChange={setSort}>
                    Type
                  </SortableHeader>
                  <SortableHeader<LogEntry> columnKey="amount" sort={sort} onSortChange={setSort}>
                    Amount
                  </SortableHeader>
                  <TableHead className="hidden md:table-cell">Balance After</TableHead>
                  <TableHead className="hidden lg:table-cell">Reference</TableHead>
                  <TableHead className="hidden lg:table-cell">Remark</TableHead>
                  <SortableHeader<LogEntry> columnKey="createdAt" sort={sort} onSortChange={setSort}>
                    Created At
                  </SortableHeader>
                </TableRow>
              </TableHeader>
              <TableBody>
                {visibleLogs.map((log) => (
                  <TableRow key={log.id}>
                    <TableCell className="font-mono text-sm">{log.id}</TableCell>
                    <TableCell className="font-mono text-sm">{log.userId}</TableCell>
                    <TableCell>
                      <span className="inline-flex items-center px-2 py-1 rounded-full text-xs font-medium bg-blue-100 text-blue-800 dark:bg-blue-900 dark:text-blue-200">
                        {LOG_TYPE_NAMES[log.type] || log.type}
                      </span>
                    </TableCell>
                    <TableCell>{formatQuota(log.amount)}</TableCell>
                    <TableCell className="hidden md:table-cell">{formatQuota(log.balanceAfter)}</TableCell>
                    <TableCell className="hidden font-mono text-xs lg:table-cell">{log.referenceId || '—'}</TableCell>
                    <TableCell className="hidden max-w-xs truncate lg:table-cell">{log.remark || '—'}</TableCell>
                    <TableCell>
                      {new Date(parseInt(log.createdAt) * 1000).toLocaleString()}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>

          <AdminPagination
            page={page}
            pageSize={pageSize}
            hasNextPage={page * pageSize < total}
            onPageChange={setPage}
            onPageSizeChange={setPageSize}
          />
        </>
      )}
    </div>
  );
}
