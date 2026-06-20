import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { AlertTriangle, Pencil, RefreshCw, Save } from 'lucide-react';
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
import { summarizeChannelHealth } from './channel-health-summary';
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
  healthStatus?: string;
  health_status?: string;
  healthLastError?: string;
  health_last_error?: string;
  healthConsecutiveFailures?: number;
  health_consecutive_failures?: number;
  circuitOpenedUntil?: string | number;
  circuit_opened_until?: string | number;
}

interface ChannelEditDraft {
  id: string;
  name: string;
  models: string;
  group: string;
  priority: string;
  weight: string;
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

function channelHealthStatus(channel: Channel) {
  return channel.healthStatus || channel.health_status || 'healthy';
}

function channelHealthError(channel: Channel) {
  return channel.healthLastError || channel.health_last_error || '';
}

function channelHealthFailures(channel: Channel) {
  return Number(channel.healthConsecutiveFailures ?? channel.health_consecutive_failures ?? 0);
}

function channelCircuitUntil(channel: Channel) {
  return Number(channel.circuitOpenedUntil ?? channel.circuit_opened_until ?? 0);
}

function healthBadgeClass(status: string) {
  if (status === 'unavailable') return 'bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-200';
  if (status === 'degraded') return 'bg-amber-100 text-amber-800 dark:bg-amber-900 dark:text-amber-200';
  return 'bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200';
}

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
  const [editingChannel, setEditingChannel] = useState<ChannelEditDraft | null>(null);
  const queryClient = useQueryClient();
  const invalidateChannelQueries = () => {
    queryClient.invalidateQueries({ queryKey: ['admin-channels'] });
    queryClient.invalidateQueries({ queryKey: ['admin-channels-health'] });
  };
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

  const { data: healthChannels, refetch: refetchHealthChannels, isFetching: isFetchingHealthChannels } = useQuery({
    queryKey: ['admin-channels-health'],
    queryFn: async () => {
      const params = new URLSearchParams({ page: '1', page_size: '1000', status: '1' });
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
      invalidateChannelQueries();
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
      invalidateChannelQueries();
      toast.success('Channel status updated');
    },
  });

  const updateMutation = useMutation({
    mutationFn: async (draft: ChannelEditDraft) => {
      const res = await adminApiClient.put('/channel', {
        id: Number(draft.id),
        channel_id: Number(draft.id),
        name: draft.name.trim(),
        models: draft.models.trim(),
        group: draft.group.trim(),
        priority: parseInt(draft.priority || '0', 10),
        weight: parseInt(draft.weight || '1', 10),
      });
      ensureApiSuccess(res.data, 'Channel update failed');
    },
    onSuccess: () => {
      invalidateChannelQueries();
      setEditingChannel(null);
      toast.success('Channel configuration saved');
    },
  });

  const refreshBalanceMutation = useMutation({
    mutationFn: async (id: string) => {
      const res = await adminApiClient.get(`/channel/update_balance/${id}`);
      ensureApiSuccess(res.data, 'Channel balance refresh failed');
    },
    onSuccess: () => {
      invalidateChannelQueries();
      toast.success('Channel balance refreshed');
    },
  });

  const testChannelMutation = useMutation({
    mutationFn: async (id: string) => {
      const res = await adminApiClient.get(`/channel/test/${id}`);
      ensureApiSuccess(res.data, 'Channel health probe failed');
    },
    onSuccess: () => {
      invalidateChannelQueries();
      toast.success('Channel health probe completed');
    },
  });

  const visibleChannels = useMemo(() => {
    return sortRows(channels ?? [], sort);
  }, [channels, sort]);
  const healthSummary = useMemo(() => summarizeChannelHealth(healthChannels ?? []), [healthChannels]);

  const handleCreate = () => {
    if (!newChannelName.trim() || !newChannelBaseUrl.trim() || !newChannelKey.trim() || !newChannelModels.trim() || !newChannelGroup.trim()) {
      toast.error('Name, base URL, API key, models, and group are required');
      return;
    }
    createMutation.mutate();
  };

  const openEdit = (channel: Channel) => {
    setEditingChannel({
      id: String(channel.id),
      name: channel.name ?? '',
      models: channel.models ?? '',
      group: channel.group ?? 'default',
      priority: String(channel.priority ?? '0'),
      weight: String(channel.weight ?? 1),
    });
  };

  const handleUpdate = () => {
    if (!editingChannel) return;
    if (!editingChannel.name.trim() || !editingChannel.models.trim() || !editingChannel.group.trim()) {
      toast.error('Name, models, and group are required');
      return;
    }
    updateMutation.mutate(editingChannel);
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

      <Dialog open={!!editingChannel} onOpenChange={(open) => !open && setEditingChannel(null)}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>Model Configuration</DialogTitle>
            <DialogDescription>Edit the models and routing settings for this channel.</DialogDescription>
          </DialogHeader>
          {editingChannel && (
            <div className="grid gap-4 pt-2 sm:grid-cols-2">
              <div className="space-y-2 sm:col-span-2">
                <Label htmlFor="edit-channel-name">Name</Label>
                <Input
                  id="edit-channel-name"
                  value={editingChannel.name}
                  onChange={(event) => setEditingChannel({ ...editingChannel, name: event.target.value })}
                />
              </div>
              <div className="space-y-2 sm:col-span-2">
                <Label htmlFor="edit-channel-models">Models</Label>
                <Input
                  id="edit-channel-models"
                  value={editingChannel.models}
                  onChange={(event) => setEditingChannel({ ...editingChannel, models: event.target.value })}
                  placeholder="gpt-4o-mini,gpt-4o"
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="edit-channel-group">Group</Label>
                <Input
                  id="edit-channel-group"
                  value={editingChannel.group}
                  onChange={(event) => setEditingChannel({ ...editingChannel, group: event.target.value })}
                />
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-2">
                  <Label htmlFor="edit-channel-priority">Priority</Label>
                  <Input
                    id="edit-channel-priority"
                    type="number"
                    value={editingChannel.priority}
                    onChange={(event) => setEditingChannel({ ...editingChannel, priority: event.target.value })}
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="edit-channel-weight">Weight</Label>
                  <Input
                    id="edit-channel-weight"
                    type="number"
                    min="1"
                    value={editingChannel.weight}
                    onChange={(event) => setEditingChannel({ ...editingChannel, weight: event.target.value })}
                  />
                </div>
              </div>
              <Button
                onClick={handleUpdate}
                disabled={updateMutation.isPending || !editingChannel.name.trim() || !editingChannel.models.trim() || !editingChannel.group.trim()}
                className="sm:col-span-2"
              >
                <Save className="size-4" />
                {updateMutation.isPending ? 'Saving...' : 'Save Configuration'}
              </Button>
            </div>
          )}
        </DialogContent>
      </Dialog>

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
              { key: 'healthStatus', label: 'Health' },
              { key: 'status', label: 'Status' },
              { key: 'usedQuota', label: 'Used Quota' },
            ]}
          />
        }
      />

      {healthSummary.unhealthy.length > 0 && (
        <div className="flex flex-col gap-3 rounded-lg border border-amber-200 bg-amber-50 px-3 py-3 text-sm text-amber-950 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-100 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex min-w-0 items-start gap-2">
            <AlertTriangle className="mt-0.5 size-4 shrink-0 text-amber-600 dark:text-amber-300" />
            <div className="min-w-0 space-y-1">
              <div className="font-medium">
                {healthSummary.unavailable.length > 0
                  ? `${healthSummary.unavailable.length} channel${healthSummary.unavailable.length > 1 ? 's are' : ' is'} unavailable`
                  : `${healthSummary.degraded.length} channel${healthSummary.degraded.length > 1 ? 's are' : ' is'} degraded`}
              </div>
              {healthSummary.primary && (
                <div className="truncate text-xs text-amber-900/80 dark:text-amber-100/80">
                  {healthSummary.primary.name}
                  {channelHealthError(healthSummary.primary) ? ` · ${channelHealthError(healthSummary.primary)}` : ''}
                </div>
              )}
            </div>
          </div>
          <div className="flex shrink-0 flex-wrap gap-2">
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => {
                setFilter('status', '1');
                setPage(1);
                if (healthSummary.primary?.name) {
                  setSearch(healthSummary.primary.name);
                }
                void refetchHealthChannels();
                invalidateChannelQueries();
              }}
            >
              View Channel
            </Button>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => void refetchHealthChannels()}
              disabled={isFetchingHealthChannels}
            >
              <RefreshCw className="size-3.5" />
              {isFetchingHealthChannels ? 'Refreshing' : 'Refresh'}
            </Button>
          </div>
        </div>
      )}

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
        <TableSkeleton columns={['ID', 'Name', 'Type', 'Group', 'Priority', 'Balance', 'Health', 'Status', 'Actions']} />
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
                  <TableHead className="hidden xl:table-cell">Health</TableHead>
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
                    <TableCell className="hidden xl:table-cell">
                      <div className="flex flex-col gap-1">
                        <span className={`inline-flex w-fit items-center rounded-full px-2 py-1 text-xs font-medium ${healthBadgeClass(channelHealthStatus(ch))}`}>
                          {channelHealthStatus(ch)}
                        </span>
                        {channelHealthFailures(ch) > 0 && (
                          <span className="text-xs text-muted-foreground">
                            {channelHealthFailures(ch)} failures
                            {channelCircuitUntil(ch) > 0 ? ` · until ${new Date(channelCircuitUntil(ch) * 1000).toLocaleString()}` : ''}
                          </span>
                        )}
                        {channelHealthError(ch) && <span className="max-w-48 truncate text-xs text-muted-foreground">{channelHealthError(ch)}</span>}
                      </div>
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
                        onClick={() => openEdit(ch)}
                      >
                        <Pencil className="size-3.5" />
                        Edit
                      </Button>
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => refreshBalanceMutation.mutate(ch.id)}
                        disabled={refreshBalanceMutation.isPending}
                      >
                        <RefreshCw className="size-3.5" />
                        Refresh
                      </Button>
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => testChannelMutation.mutate(ch.id)}
                        disabled={testChannelMutation.isPending}
                      >
                        Test
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
