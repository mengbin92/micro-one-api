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
import { AdminTableToolbar } from '@/components/admin/AdminTableToolbar';
import { ExportButton } from '@/components/admin/ExportButton';
import { SortableHeader } from '@/components/admin/SortableHeader';
import { useAdminTableState } from '@/hooks/useAdminTableState';
import { buildAdminListParams } from '@/lib/admin-table-query';
import { ensureApiSuccess, unwrapApiData } from '@/lib/api-response';
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
    storageKey: 'channels',
    filters: ['status', 'type'],
  });
  const [isCreateOpen, setIsCreateOpen] = useState(false);
  const [newChannelName, setNewChannelName] = useState('');
  const [newChannelType, setNewChannelType] = useState('1');
  const [newChannelBaseUrl, setNewChannelBaseUrl] = useState('');
  const [newChannelKey, setNewChannelKey] = useState('');
  const [newChannelModels, setNewChannelModels] = useState('');
  const [newChannelGroup, setNewChannelGroup] = useState('default');
  const [newChannelPriority, setNewChannelPriority] = useState('0');
  const [newChannelWeight, setNewChannelWeight] = useState('1');
  const queryClient = useQueryClient();
  const sort = { key: sortKey as keyof Channel | null, direction: sortDirection } satisfies SortState<Channel>;
  const statusFilter = filters.status ?? '';
  const typeFilter = filters.type ?? '';
  const exportParams = buildAdminListParams({ page, pageSize, search, sortKey, sortDirection, filters });
  exportParams.set('format', 'csv');
  const exportHref = `/channel/export?${exportParams}`;

  const { data: channels, isLoading } = useQuery({
    queryKey: ['admin-channels', page, pageSize, search, sortKey, sortDirection, filters],
    queryFn: async () => {
      const params = buildAdminListParams({
        page,
        pageSize,
        search,
        sortKey,
        sortDirection,
        filters,
      });
      const res = await adminApiClient.get(`/channel?${params}`);
      return unwrapApiData<Channel[]>(res.data);
    },
  });

  const createMutation = useMutation({
    mutationFn: async () => {
      const res = await adminApiClient.post('/channel', {
        name: newChannelName.trim(),
        type: parseInt(newChannelType, 10),
        base_url: newChannelBaseUrl.trim(),
        key: newChannelKey.trim(),
        models: newChannelModels.trim(),
        group: newChannelGroup.trim(),
        priority: parseInt(newChannelPriority || '0', 10),
        weight: parseInt(newChannelWeight || '1', 10),
      });
      ensureApiSuccess(res.data, 'Channel create failed');
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin-channels'] });
      setIsCreateOpen(false);
      setNewChannelName('');
      setNewChannelType('1');
      setNewChannelBaseUrl('');
      setNewChannelKey('');
      setNewChannelModels('');
      setNewChannelGroup('default');
      setNewChannelPriority('0');
      setNewChannelWeight('1');
      toast.success('Channel created');
    },
  });

  const toggleStatusMutation = useMutation({
    mutationFn: async ({ id, currentStatus }: { id: string; currentStatus: number }) => {
      let res;
      if (currentStatus === 1) {
        res = await adminApiClient.post(`/channel/disable/${id}`);
      } else {
        res = await adminApiClient.post(`/channel/enable/${id}`);
      }
      ensureApiSuccess(res.data, 'Channel status update failed');
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin-channels'] });
      toast.success('Channel status updated');
    },
  });

  const refreshBalanceMutation = useMutation({
    mutationFn: async (id: string) => {
      const res = await adminApiClient.get(`/channel/update_balance/${id}`);
      ensureApiSuccess(res.data, 'Channel balance refresh failed');
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin-channels'] });
      toast.success('Channel balance refreshed');
    },
  });

  const visibleChannels = useMemo(() => {
    return sortRows(channels ?? [], sort);
  }, [channels, sort]);

  const handleCreate = () => {
    if (!newChannelName.trim() || !newChannelBaseUrl.trim() || !newChannelKey.trim() || !newChannelModels.trim() || !newChannelGroup.trim()) {
      toast.error('Name, base URL, API key, models, and group are required');
      return;
    }
    createMutation.mutate();
  };

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-2xl font-semibold">Channels Management</h2>
        <Dialog open={isCreateOpen} onOpenChange={setIsCreateOpen}>
          <DialogTrigger render={<Button />}>
            Create Channel
          </DialogTrigger>
          <DialogContent className="sm:max-w-lg">
            <DialogHeader>
              <DialogTitle>Create Channel</DialogTitle>
              <DialogDescription>Add an upstream provider channel.</DialogDescription>
            </DialogHeader>
            <div className="grid gap-4 pt-2 sm:grid-cols-2">
              <div className="space-y-2">
                <Label htmlFor="channel-name">Name</Label>
                <Input id="channel-name" value={newChannelName} onChange={(e) => setNewChannelName(e.target.value)} placeholder="openai-main" />
              </div>
              <div className="space-y-2">
                <Label htmlFor="channel-type">Provider</Label>
                <select
                  id="channel-type"
                  value={newChannelType}
                  onChange={(event) => setNewChannelType(event.target.value)}
                  className="h-8 w-full rounded-lg border border-input bg-background px-2.5 text-sm"
                >
                  {Object.entries(PROVIDER_NAMES).map(([type, name]) => (
                    <option key={type} value={type}>
                      {name}
                    </option>
                  ))}
                </select>
              </div>
              <div className="space-y-2 sm:col-span-2">
                <Label htmlFor="channel-base-url">Base URL</Label>
                <Input id="channel-base-url" value={newChannelBaseUrl} onChange={(e) => setNewChannelBaseUrl(e.target.value)} placeholder="https://api.example.com/v1" />
              </div>
              <div className="space-y-2 sm:col-span-2">
                <Label htmlFor="channel-key">API Key</Label>
                <Input id="channel-key" type="password" value={newChannelKey} onChange={(e) => setNewChannelKey(e.target.value)} placeholder="sk-..." />
              </div>
              <div className="space-y-2 sm:col-span-2">
                <Label htmlFor="channel-models">Models</Label>
                <Input id="channel-models" value={newChannelModels} onChange={(e) => setNewChannelModels(e.target.value)} placeholder="gpt-4o-mini,gpt-4o" />
              </div>
              <div className="space-y-2">
                <Label htmlFor="channel-group">Group</Label>
                <Input id="channel-group" value={newChannelGroup} onChange={(e) => setNewChannelGroup(e.target.value)} />
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-2">
                  <Label htmlFor="channel-priority">Priority</Label>
                  <Input id="channel-priority" type="number" value={newChannelPriority} onChange={(e) => setNewChannelPriority(e.target.value)} />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="channel-weight">Weight</Label>
                  <Input id="channel-weight" type="number" min="1" value={newChannelWeight} onChange={(e) => setNewChannelWeight(e.target.value)} />
                </div>
              </div>
              <Button
                onClick={handleCreate}
                disabled={createMutation.isPending || !newChannelName.trim() || !newChannelBaseUrl.trim() || !newChannelKey.trim() || !newChannelModels.trim() || !newChannelGroup.trim()}
                className="sm:col-span-2"
              >
                {createMutation.isPending ? 'Creating...' : 'Create'}
              </Button>
            </div>
          </DialogContent>
        </Dialog>
      </div>

      <AdminTableToolbar
        search={search}
        searchPlaceholder="Search by name..."
        onSearchChange={setSearch}
        onClear={clearSearch}
        actions={
          <ExportButton
            filename="admin-channels.csv"
            href={exportHref}
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
          onChange={(event) => setFilter('status', event.target.value)}
          className="h-8 rounded-md border bg-background px-2 text-sm"
          aria-label="Filter channels by status"
        >
          <option value="">All statuses</option>
          <option value="1">Active</option>
          <option value="2">Disabled</option>
        </select>
        <select
          value={typeFilter}
          onChange={(event) => setFilter('type', event.target.value)}
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
                  <SortableHeader<Channel> columnKey="priority" sort={sort} onSortChange={setSort} className="hidden lg:table-cell">
                    Priority
                  </SortableHeader>
                  <SortableHeader<Channel> columnKey="balance" sort={sort} onSortChange={setSort} className="hidden md:table-cell">
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
                    <TableCell className="hidden lg:table-cell">{ch.priority ?? 0}</TableCell>
                    <TableCell className="hidden md:table-cell">
                      {ch.balance != null ? `$${ch.balance.toFixed(2)}` : '$0.00'}
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
