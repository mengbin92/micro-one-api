import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Eye, Trash2 } from 'lucide-react';
import { useMemo, useState } from 'react';
import { toast } from 'sonner';
import { adminApiClient } from '@/lib/api';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
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

interface LogEntry {
  id: string;
  level?: string;
  userId: string;
  user_id?: string | number;
  type: string;
  message?: string;
  source?: string;
  request_id?: string;
  amount: string;
  balanceAfter: string;
  referenceId: string;
  remark: string;
  createdAt: string;
  created_at?: string | number;
  username?: string;
  token_name?: string;
  model_name?: string;
  quota?: string | number;
  prompt_tokens?: string | number;
  completion_tokens?: string | number;
  cache_read_tokens?: string | number;
  channel?: string | number;
  channelId?: number;
  channelName?: string;
  channelType?: number;
  channelTypeStr?: string;
  endpoint?: string;
  subscription_account_id?: number;
  subscriptionAccountId?: number;
  elapsed_time?: string | number;
  is_stream?: boolean;
}

interface LogListData {
  logs?: LogEntry[];
  total?: number;
}

type DetailRow = [string, string | number | boolean | undefined | null];

const EMPTY_LOGS: LogEntry[] = [];

const LOG_TYPE_NAMES: Record<string, string> = {
  redeem: 'Redeem',
  recharge: 'Recharge',
  consume: 'Consume',
  refund: 'Refund',
};

function datetimeLocalToUnixSeconds(value: string) {
  const millis = new Date(value).getTime();
  if (!Number.isFinite(millis) || millis <= 0) return '';
  return String(Math.floor(millis / 1000));
}

function unixSecondsToDatetimeLocal(value: string | undefined) {
  const seconds = Number(value ?? 0);
  if (!Number.isFinite(seconds) || seconds <= 0) return '';
  const date = new Date(seconds * 1000);
  const offsetMillis = date.getTimezoneOffset() * 60 * 1000;
  return new Date(date.getTime() - offsetMillis).toISOString().slice(0, 16);
}

export function AdminLogsPage() {
  const [selectedLogId, setSelectedLogId] = useState<string | null>(null);
  const [isCleanOpen, setIsCleanOpen] = useState(false);
  const [cleanEndTime, setCleanEndTime] = useState('');
  const queryClient = useQueryClient();
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
    filters: ['user_id', 'type', 'start_time', 'end_time', 'subscription_account_id'],
  });
  const userId = filters.user_id ?? '';
  const type = filters.type ?? '';
  const startTime = filters.start_time ?? '';
  const endTime = filters.end_time ?? '';
  const subscriptionAccountId = filters.subscription_account_id ?? '';
  const sort = useMemo(
    () => ({ key: sortKey as keyof LogEntry | null, direction: sortDirection }) satisfies SortState<LogEntry>,
    [sortKey, sortDirection],
  );
  const exportParams = buildAdminListParams({
    page,
    pageSize,
    sortKey,
    sortDirection,
    filters: { user_id: userId, type, start_time: startTime, end_time: endTime, subscription_account_id: subscriptionAccountId },
  });
  exportParams.set('format', 'csv');
  const exportHref = `/log/export?${exportParams}`;

  const { data, isLoading } = useQuery({
    queryKey: ['admin-logs', page, pageSize, userId, type, startTime, endTime, sortKey, sortDirection],
    queryFn: async () => {
      const params = buildAdminListParams({
        page,
        pageSize,
        sortKey,
        sortDirection,
        filters: { user_id: userId, type, start_time: startTime, end_time: endTime },
      });
      const res = await adminApiClient.get(`/log?${params}`);
      const payload = unwrapApiData<LogEntry[] | LogListData>(res.data);
      return Array.isArray(payload)
        ? { logs: payload, total: payload.length }
        : { logs: payload.logs ?? [], total: payload.total ?? payload.logs?.length ?? 0 };
    },
  });

  const { data: selectedLog, isLoading: isDetailLoading } = useQuery({
    queryKey: ['admin-log-detail', selectedLogId],
    enabled: selectedLogId !== null,
    queryFn: async () => {
      const res = await adminApiClient.get(`/log/${selectedLogId}`);
      return unwrapApiData<LogEntry>(res.data);
    },
  });

  const cleanMutation = useMutation({
    mutationFn: async () => {
      const endTime = Math.floor(new Date(cleanEndTime).getTime() / 1000);
      if (!Number.isFinite(endTime) || endTime <= 0) {
        throw new Error('End time is required');
      }
      const params = new URLSearchParams({ end_time: String(endTime) });
      if (startTime) params.set('start_time', startTime);
      if (userId) params.set('user_id', userId);
      if (type) params.set('type', type);
      const res = await adminApiClient.delete(`/log?${params}`);
      ensureApiSuccess(res.data, 'Log cleanup failed');
      return unwrapApiData<{ deleted?: number }>(res.data);
    },
    onSuccess: (data) => {
      queryClient.invalidateQueries({ queryKey: ['admin-logs'] });
      setIsCleanOpen(false);
      setCleanEndTime('');
      toast.success(`Deleted ${Number(data?.deleted ?? 0)} log entries`);
    },
    onError: (error: unknown) => {
      const message = error instanceof Error ? error.message : 'Log cleanup failed';
      toast.error(message);
    },
  });

  function formatQuota(q: string) {
    return formatAmountUnits(q);
  }

  function formatTimestamp(value: string | number | undefined) {
    const seconds = Number(value ?? 0);
    return seconds > 0 ? new Date(seconds * 1000).toLocaleString() : '-';
  }

  function displayValue(value: string | number | boolean | undefined | null) {
    if (value === undefined || value === null || value === '') return '-';
    if (typeof value === 'boolean') return value ? 'Yes' : 'No';
    return String(value);
  }

  const logs = data?.logs ?? EMPTY_LOGS;
  const total = data?.total ?? logs.length;
  const visibleLogs = useMemo(() => sortRows(logs, sort), [logs, sort]);
  const detailRows: DetailRow[] = selectedLog
    ? [
        ['ID', selectedLog.id],
        ['Type', selectedLog.type || selectedLog.level],
        ['User ID', selectedLog.userId || selectedLog.user_id],
        ['Username', selectedLog.username],
        ['Request ID', selectedLog.request_id],
        ['Model', selectedLog.model_name],
        ['Token', selectedLog.token_name],
        ['Channel', selectedLog.channelName
          ? `${selectedLog.channelName}${selectedLog.channelTypeStr && selectedLog.channelTypeStr !== 'Unknown' ? ` (${selectedLog.channelTypeStr})` : ''}`
          : selectedLog.channel],
        ['Subscription Account', selectedLog.subscription_account_id ?? selectedLog.subscriptionAccountId],
        ['Amount', formatQuota(selectedLog.amount)],
        ['Balance After', formatQuota(selectedLog.balanceAfter)],
        ['Quota', selectedLog.quota],
        ['Prompt Tokens', selectedLog.prompt_tokens],
        ['Completion Tokens', selectedLog.completion_tokens],
        ['Cache Read Tokens', selectedLog.cache_read_tokens],
        ['Elapsed Time', selectedLog.elapsed_time ? `${selectedLog.elapsed_time} ms` : undefined],
        ['Stream', selectedLog.is_stream],
        ['Created At', formatTimestamp(selectedLog.created_at ?? selectedLog.createdAt)],
      ]
    : [];

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
        <Input
          placeholder="订阅账号 ID"
          value={subscriptionAccountId}
          onChange={(e) => setFilter('subscription_account_id', e.target.value.trim())}
          className="max-w-xs"
        />
        <Button
          variant="outline"
          onClick={() => {
            setFilter('user_id', '');
            setFilter('type', '');
            setFilter('start_time', '');
            setFilter('end_time', '');
            setFilter('subscription_account_id', '');
          }}
        >
          Clear
        </Button>
        <Button type="button" variant="destructive" onClick={() => setIsCleanOpen(true)}>
          <Trash2 className="size-4" />
          Clean
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
              { key: 'subscriptionAccountId', label: 'Subscription Account' },
              { key: 'remark', label: 'Remark' },
              { key: 'createdAt', label: 'Created At' },
            ]}
          />
        </div>
      </div>

      <div className="flex flex-wrap items-center gap-4">
        <div className="flex items-center gap-2">
          <Label htmlFor="log-start-time" className="text-xs text-muted-foreground">
            From
          </Label>
          <Input
            id="log-start-time"
            type="datetime-local"
            value={unixSecondsToDatetimeLocal(startTime)}
            onChange={(event) => setFilter('start_time', datetimeLocalToUnixSeconds(event.target.value))}
            className="w-auto"
          />
        </div>
        <div className="flex items-center gap-2">
          <Label htmlFor="log-end-time" className="text-xs text-muted-foreground">
            To
          </Label>
          <Input
            id="log-end-time"
            type="datetime-local"
            value={unixSecondsToDatetimeLocal(endTime)}
            onChange={(event) => setFilter('end_time', datetimeLocalToUnixSeconds(event.target.value))}
            className="w-auto"
          />
        </div>
      </div>

      {isLoading ? (
        <TableSkeleton columns={['ID', 'User ID', 'Type', 'Amount', 'Balance After', 'Subscription Account', 'Reference', 'Remark', 'Created At', 'Actions']} rows={8} />
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
                  <TableHead className="hidden lg:table-cell">Channel</TableHead>
                  <TableHead className="hidden md:table-cell">Balance After</TableHead>
                  <TableHead className="hidden lg:table-cell">Subscription Account</TableHead>
                  <TableHead className="hidden lg:table-cell">Reference</TableHead>
                  <TableHead className="hidden lg:table-cell">Remark</TableHead>
                  <SortableHeader<LogEntry> columnKey="createdAt" sort={sort} onSortChange={setSort}>
                    Created At
                  </SortableHeader>
                  <TableHead className="text-right">Actions</TableHead>
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
                    <TableCell className="hidden lg:table-cell">
                      {log.channelName
                        ? `${log.channelName}${log.channelTypeStr && log.channelTypeStr !== 'Unknown' ? ` (${log.channelTypeStr})` : ''}`
                        : log.channelId
                          ? <span className="text-xs text-muted-foreground">#{log.channelId}</span>
                          : log.channel
                            ? String(log.channel)
                            : '—'}
                    </TableCell>
                    <TableCell className="hidden md:table-cell">{formatQuota(log.balanceAfter)}</TableCell>
                    <TableCell className="hidden font-mono text-xs lg:table-cell">{log.referenceId || '—'}</TableCell>
                    <TableCell className="hidden max-w-xs truncate lg:table-cell">{log.remark || '—'}</TableCell>
                    <TableCell>
                      {new Date(parseInt(log.createdAt) * 1000).toLocaleString()}
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        type="button"
                        variant="outline"
                        size="icon-sm"
                        aria-label={`View log ${log.id}`}
                        onClick={() => setSelectedLogId(String(log.id))}
                      >
                        <Eye className="size-4" />
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
            hasNextPage={page * pageSize < total}
            onPageChange={setPage}
            onPageSizeChange={setPageSize}
          />
        </>
      )}

      <Dialog open={selectedLogId !== null} onOpenChange={(open) => !open && setSelectedLogId(null)}>
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>Log Details</DialogTitle>
            <DialogDescription>
              {selectedLogId ? `Inspect billing and relay metadata for log ${selectedLogId}.` : 'Inspect billing and relay metadata.'}
            </DialogDescription>
          </DialogHeader>
          {isDetailLoading ? (
            <TableSkeleton columns={['Field', 'Value']} rows={8} />
          ) : selectedLog ? (
            <div className="space-y-4">
              <div className="overflow-x-auto rounded-lg border">
                <Table>
                  <TableBody>
                    {detailRows.map(([label, value]) => (
                      <TableRow key={label}>
                        <TableCell className="w-40 bg-muted/40 text-xs font-medium text-muted-foreground">{label}</TableCell>
                        <TableCell className="font-mono text-xs">{displayValue(value)}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
              <div className="rounded-lg border bg-muted/30 p-3">
                <div className="mb-2 text-xs font-medium text-muted-foreground">Message</div>
                <pre className="max-h-56 overflow-auto whitespace-pre-wrap break-words text-xs leading-5">
                  {displayValue(selectedLog.message || selectedLog.remark)}
                </pre>
              </div>
            </div>
          ) : (
            <EmptyState title="Log details unavailable" description="The log service did not return details for this entry." />
          )}
        </DialogContent>
      </Dialog>

      <Dialog open={isCleanOpen} onOpenChange={setIsCleanOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Clean Logs</DialogTitle>
            <DialogDescription>
              Delete matching log entries up to the selected time.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="log-clean-end-time">End Time</Label>
              <Input
                id="log-clean-end-time"
                type="datetime-local"
                value={cleanEndTime}
                onChange={(event) => setCleanEndTime(event.target.value)}
              />
            </div>
            <div className="rounded-lg border bg-muted/30 p-3 text-xs text-muted-foreground">
              Scope: user {userId || 'all'} · type {type || 'all'}
            </div>
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setIsCleanOpen(false)}>
              Cancel
            </Button>
            <Button
              type="button"
              variant="destructive"
              disabled={!cleanEndTime || cleanMutation.isPending}
              onClick={() => cleanMutation.mutate()}
            >
              <Trash2 className="size-4" />
              {cleanMutation.isPending ? 'Cleaning...' : 'Clean Logs'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
