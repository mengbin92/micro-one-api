import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { useMemo, useState } from 'react';
import { toast } from 'sonner';
import { adminApiClient } from '@/lib/api';
import { Button } from '@/components/ui/button';
import { EmptyState } from '@/components/EmptyState';
import { TableSkeleton } from '@/components/LoadingStates';
import { AdminPagination } from '@/components/admin/AdminPagination';
import { AdminTableToolbar } from '@/components/admin/AdminTableToolbar';
import { ExportButton } from '@/components/admin/ExportButton';
import { SortableHeader } from '@/components/admin/SortableHeader';
import { useAdminTableState } from '@/hooks/useAdminTableState';
import { sortRows, type SortState } from '@/lib/table-utils';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';

interface Channel {
  id: string;
  type: number;
  name: string;
  status: number;
  baseUrl: string;
  group: string;
  models: string;
  priority: string;
  weight: number;
  balance: number;
  balanceUpdatedTime: string;
  usedQuota: string;
}

const PROVIDER_NAMES: Record<number, string> = {
  1: 'OpenAI',
  2: 'Anthropic',
  3: 'Azure',
  4: 'Gemini',
  14: 'DeepSeek',
  23: 'OpenRouter',
  37: 'SiliconFlow',
};

export function AdminChannelsPage() {
  const { page, pageSize, search, setPage, setPageSize, setSearch, clearSearch } = useAdminTableState({
    storageKey: 'channels',
  });
  const [sort, setSort] = useState<SortState<Channel>>({ key: null, direction: null });
  const [statusFilter, setStatusFilter] = useState('');
  const [typeFilter, setTypeFilter] = useState('');
  const queryClient = useQueryClient();

  const { data: channels, isLoading } = useQuery({
    queryKey: ['admin-channels', page, pageSize, search],
    queryFn: async () => {
      const params = new URLSearchParams();
      params.set('page', page.toString());
      params.set('page_size', pageSize.toString());
      if (search) params.set('keyword', search);
      const res = await adminApiClient.get(`/channel?${params}`);
      return res.data.data as Channel[];
    },
  });

  const toggleStatusMutation = useMutation({
    mutationFn: async ({ id, currentStatus }: { id: string; currentStatus: number }) => {
      const newStatus = currentStatus === 1 ? 2 : 1;
      await adminApiClient.put(`/channel/${id}`, { status: newStatus });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin-channels'] });
      toast.success('Channel status updated');
    },
  });

  const refreshBalanceMutation = useMutation({
    mutationFn: async (id: string) => {
      await adminApiClient.get(`/channel/update_balance/${id}`);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin-channels'] });
      toast.success('Channel balance refreshed');
    },
  });

  const visibleChannels = useMemo(() => {
    const filtered = (channels ?? []).filter((channel) => {
      const statusMatches = !statusFilter || String(channel.status) === statusFilter;
      const typeMatches = !typeFilter || String(channel.type) === typeFilter;
      return statusMatches && typeMatches;
    });
    return sortRows(filtered, sort);
  }, [channels, statusFilter, typeFilter, sort]);

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-2xl font-semibold">Channels Management</h2>
      </div>

      <AdminTableToolbar
        search={search}
        searchPlaceholder="Search by name..."
        onSearchChange={setSearch}
        onClear={clearSearch}
        actions={
          <ExportButton
            filename="admin-channels.csv"
            rows={visibleChannels}
            columns={[
              { key: 'id', label: 'ID' },
              { key: 'name', label: 'Name' },
              { key: 'type', label: 'Type' },
              { key: 'group', label: 'Group' },
              { key: 'priority', label: 'Priority' },
              { key: 'balance', label: 'Balance' },
              { key: 'status', label: 'Status' },
              { key: 'usedQuota', label: 'Used Quota' },
            ]}
          />
        }
      />

      <div className="flex flex-wrap items-center gap-3">
        <select
          value={statusFilter}
          onChange={(event) => setStatusFilter(event.target.value)}
          className="h-8 rounded-md border bg-background px-2 text-sm"
          aria-label="Filter channels by status"
        >
          <option value="">All statuses</option>
          <option value="1">Active</option>
          <option value="2">Disabled</option>
        </select>
        <select
          value={typeFilter}
          onChange={(event) => setTypeFilter(event.target.value)}
          className="h-8 rounded-md border bg-background px-2 text-sm"
          aria-label="Filter channels by provider"
        >
          <option value="">All providers</option>
          {Object.entries(PROVIDER_NAMES).map(([type, name]) => (
            <option key={type} value={type}>
              {name}
            </option>
          ))}
        </select>
      </div>

      {isLoading ? (
        <TableSkeleton columns={['ID', 'Name', 'Type', 'Group', 'Priority', 'Balance', 'Status', 'Actions']} />
      ) : !channels || channels.length === 0 ? (
        <EmptyState title="No channels found" description="Try clearing the search term or checking another page." />
      ) : visibleChannels.length === 0 ? (
        <EmptyState title="No channels match the filters" description="Clear the table filters to show the loaded rows." />
      ) : (
        <>
          <div className="border rounded-lg overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>ID</TableHead>
                  <SortableHeader<Channel> columnKey="name" sort={sort} onSortChange={setSort}>
                    Name
                  </SortableHeader>
                  <SortableHeader<Channel> columnKey="type" sort={sort} onSortChange={setSort}>
                    Type
                  </SortableHeader>
                  <SortableHeader<Channel> columnKey="group" sort={sort} onSortChange={setSort}>
                    Group
                  </SortableHeader>
                  <SortableHeader<Channel> columnKey="priority" sort={sort} onSortChange={setSort}>
                    Priority
                  </SortableHeader>
                  <SortableHeader<Channel> columnKey="balance" sort={sort} onSortChange={setSort}>
                    Balance
                  </SortableHeader>
                  <SortableHeader<Channel> columnKey="status" sort={sort} onSortChange={setSort}>
                    Status
                  </SortableHeader>
                  <TableHead className="text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {visibleChannels.map((ch) => (
                  <TableRow key={ch.id}>
                    <TableCell className="font-mono text-sm">{ch.id}</TableCell>
                    <TableCell className="font-medium">{ch.name}</TableCell>
                    <TableCell>{PROVIDER_NAMES[ch.type] || `Type ${ch.type}`}</TableCell>
                    <TableCell>{ch.group}</TableCell>
                    <TableCell>{ch.priority}</TableCell>
                    <TableCell>
                      {ch.balance !== undefined ? `$${ch.balance.toFixed(2)}` : '—'}
                    </TableCell>
                    <TableCell>
                      <span
                        className={`inline-flex items-center px-2 py-1 rounded-full text-xs font-medium ${
                          ch.status === 1
                            ? 'bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200'
                            : 'bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-200'
                        }`}
                      >
                        {ch.status === 1 ? 'Active' : 'Disabled'}
                      </span>
                    </TableCell>
                    <TableCell className="text-right space-x-2">
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => refreshBalanceMutation.mutate(ch.id)}
                        disabled={refreshBalanceMutation.isPending}
                      >
                        Refresh
                      </Button>
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() =>
                          toggleStatusMutation.mutate({ id: ch.id, currentStatus: ch.status })
                        }
                        disabled={toggleStatusMutation.isPending}
                      >
                        {ch.status === 1 ? 'Disable' : 'Enable'}
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
            hasNextPage={!!channels && channels.length >= pageSize}
            onPageChange={setPage}
            onPageSizeChange={setPageSize}
          />
        </>
      )}
    </div>
  );
}
