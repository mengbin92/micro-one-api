import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { AlertTriangle, Pencil, RefreshCw, Save, Trash2 } from 'lucide-react';
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
import { ModelMultiSelect } from '@/components/admin/ModelMultiSelect';
import { locale, t } from '@/lib/i18n';

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
  32: 'CodexOAuth',
  33: 'ClaudeOAuth',
  34: 'ZhipuPlan',
  35: 'MinimaxPlan',
  36: 'KimiOAuth',
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

function healthStatusLabel(status: string) {
  if (status === 'unavailable') return t('不可用');
  if (status === 'degraded') return t('性能下降');
  return t('正常');
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
      ensureApiSuccess(res.data, t('创建渠道失败'));
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
      toast.success(t('渠道已创建'));
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
      ensureApiSuccess(res.data, t('更新渠道状态失败'));
    },
    onSuccess: () => {
      invalidateChannelQueries();
      toast.success(t('渠道状态已更新'));
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
      ensureApiSuccess(res.data, t('更新渠道失败'));
    },
    onSuccess: () => {
      invalidateChannelQueries();
      setEditingChannel(null);
      toast.success(t('渠道配置已保存'));
    },
  });

  const refreshBalanceMutation = useMutation({
    mutationFn: async (id: string) => {
      const res = await adminApiClient.get(`/channel/update_balance/${id}`);
      ensureApiSuccess(res.data, t('刷新渠道余额失败'));
    },
    onSuccess: () => {
      invalidateChannelQueries();
      toast.success(t('渠道余额已刷新'));
    },
  });

  const testChannelMutation = useMutation({
    mutationFn: async (id: string) => {
      const res = await adminApiClient.get(`/channel/test/${id}`);
      ensureApiSuccess(res.data, t('渠道健康检测失败'));
    },
    onSuccess: () => {
      invalidateChannelQueries();
      toast.success(t('渠道健康检测已完成'));
    },
  });

  const deleteChannelMutation = useMutation({
    mutationFn: async (id: string) => {
      const res = await adminApiClient.delete(`/channel/${id}`);
      ensureApiSuccess(res.data, t('删除渠道失败'));
    },
    onSuccess: () => {
      invalidateChannelQueries();
      toast.success(t('渠道已删除'));
    },
  });

  const visibleChannels = useMemo(() => {
    return sortRows(channels ?? [], sort);
  }, [channels, sort]);
  const healthSummary = useMemo(() => summarizeChannelHealth(healthChannels ?? []), [healthChannels]);

  const handleCreate = () => {
    if (!newChannelName.trim() || !newChannelBaseUrl.trim() || !newChannelKey.trim() || !newChannelGroup.trim()) {
      toast.error(t('名称、基础 URL、API 密钥和分组为必填项'));
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
      toast.error(t('名称、模型和分组为必填项'));
      return;
    }
    updateMutation.mutate(editingChannel);
  };

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-2xl font-semibold">{t('渠道管理')}</h2>
        <Dialog open={isCreateOpen} onOpenChange={setIsCreateOpen}>
          <DialogTrigger render={<Button />}>
            {t('创建渠道')}
          </DialogTrigger>
          <DialogContent className="sm:max-w-lg">
            <DialogHeader>
              <DialogTitle>{t('创建渠道')}</DialogTitle>
              <DialogDescription>{t('添加上游供应商渠道。')}</DialogDescription>
            </DialogHeader>
            <div className="grid gap-4 pt-2 sm:grid-cols-2">
              <div className="space-y-2">
                <Label htmlFor="channel-name">{t('名称')}</Label>
                <Input id="channel-name" value={newChannelName} onChange={(e) => setNewChannelName(e.target.value)} placeholder="openai-main" />
              </div>
              <div className="space-y-2">
                <Label htmlFor="channel-type">{t('供应商')}</Label>
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
                <Label htmlFor="channel-base-url">{t('基础 URL')}</Label>
                <Input id="channel-base-url" value={newChannelBaseUrl} onChange={(e) => setNewChannelBaseUrl(e.target.value)} placeholder="https://api.example.com/v1" />
              </div>
              <div className="space-y-2 sm:col-span-2">
                <Label htmlFor="channel-key">{t('API 密钥')}</Label>
                <Input id="channel-key" type="password" value={newChannelKey} onChange={(e) => setNewChannelKey(e.target.value)} placeholder="sk-..." />
              </div>
              <div className="space-y-2 sm:col-span-2">
                <Label htmlFor="channel-models">{t('模型（可选）')}</Label>
                <ModelMultiSelect value={newChannelModels} onChange={setNewChannelModels} />
              </div>
              <div className="space-y-2">
                <Label htmlFor="channel-group">{t('分组')}</Label>
                <Input id="channel-group" value={newChannelGroup} onChange={(e) => setNewChannelGroup(e.target.value)} />
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-2">
                  <Label htmlFor="channel-priority">{t('优先级')}</Label>
                  <Input id="channel-priority" type="number" value={newChannelPriority} onChange={(e) => setNewChannelPriority(e.target.value)} />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="channel-weight">{t('权重')}</Label>
                  <Input id="channel-weight" type="number" min="1" value={newChannelWeight} onChange={(e) => setNewChannelWeight(e.target.value)} />
                </div>
              </div>
              <Button
                onClick={handleCreate}
                disabled={createMutation.isPending || !newChannelName.trim() || !newChannelBaseUrl.trim() || !newChannelKey.trim() || !newChannelGroup.trim()}
                className="sm:col-span-2"
              >
                {createMutation.isPending ? t('创建中...') : t('创建')}
              </Button>
            </div>
          </DialogContent>
        </Dialog>
      </div>

      <Dialog open={!!editingChannel} onOpenChange={(open) => !open && setEditingChannel(null)}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{t('模型配置')}</DialogTitle>
            <DialogDescription>{t('编辑此渠道的模型和路由设置。')}</DialogDescription>
          </DialogHeader>
          {editingChannel && (
            <div className="grid gap-4 pt-2 sm:grid-cols-2">
              <div className="space-y-2 sm:col-span-2">
                <Label htmlFor="edit-channel-name">{t('名称')}</Label>
                <Input
                  id="edit-channel-name"
                  value={editingChannel.name}
                  onChange={(event) => setEditingChannel({ ...editingChannel, name: event.target.value })}
                />
              </div>
              <div className="space-y-2 sm:col-span-2">
                <Label htmlFor="edit-channel-models">{t('模型')}</Label>
                <ModelMultiSelect
                  value={editingChannel.models}
                  onChange={(csv) => setEditingChannel({ ...editingChannel, models: csv })}
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="edit-channel-group">{t('分组')}</Label>
                <Input
                  id="edit-channel-group"
                  value={editingChannel.group}
                  onChange={(event) => setEditingChannel({ ...editingChannel, group: event.target.value })}
                />
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-2">
                  <Label htmlFor="edit-channel-priority">{t('优先级')}</Label>
                  <Input
                    id="edit-channel-priority"
                    type="number"
                    value={editingChannel.priority}
                    onChange={(event) => setEditingChannel({ ...editingChannel, priority: event.target.value })}
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="edit-channel-weight">{t('权重')}</Label>
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
                {updateMutation.isPending ? t('保存中...') : t('保存配置')}
              </Button>
            </div>
          )}
        </DialogContent>
      </Dialog>

      <AdminTableToolbar
        search={search}
        searchPlaceholder={t('按名称搜索...')}
        onSearchChange={setSearch}
        onClear={clearSearch}
        actions={
          <ExportButton
            filename="admin-channels.csv"
            href={exportHref}
            rows={visibleChannels}
            columns={[
              { key: 'id', label: 'ID' },
              { key: 'name', label: t('名称') },
              { key: 'type', label: t('类型') },
              { key: 'group', label: t('分组') },
              { key: 'priority', label: t('优先级') },
              { key: 'balance', label: t('余额') },
              { key: 'healthStatus', label: t('健康状态') },
              { key: 'status', label: t('状态') },
              { key: 'usedQuota', label: t('已用额度') },
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
                  ? `${t('不可用渠道：')}${healthSummary.unavailable.length}`
                  : `${t('性能下降渠道：')}${healthSummary.degraded.length}`}
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
              {t('查看渠道')}
            </Button>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => void refetchHealthChannels()}
              disabled={isFetchingHealthChannels}
            >
              <RefreshCw className="size-3.5" />
              {isFetchingHealthChannels ? t('刷新中') : t('刷新')}
            </Button>
          </div>
        </div>
      )}

      <div className="flex flex-wrap items-center gap-3">
        <select
          value={statusFilter}
          onChange={(event) => setFilter('status', event.target.value)}
          className="h-8 rounded-md border bg-background px-2 text-sm"
          aria-label={t('按状态筛选渠道')}
        >
          <option value="">{t('全部状态')}</option>
          <option value="1">{t('启用')}</option>
          <option value="2">{t('禁用')}</option>
        </select>
        <select
          value={typeFilter}
          onChange={(event) => setFilter('type', event.target.value)}
          className="h-8 rounded-md border bg-background px-2 text-sm"
          aria-label={t('按供应商筛选渠道')}
        >
          <option value="">{t('全部供应商')}</option>
          {Object.entries(PROVIDER_NAMES).map(([type, name]) => (
            <option key={type} value={type}>
              {name}
            </option>
          ))}
        </select>
      </div>

      {isLoading ? (
        <TableSkeleton columns={['ID', t('名称'), t('类型'), t('分组'), t('优先级'), t('余额'), t('健康状态'), t('状态'), t('操作')]} />
      ) : !channels || channels.length === 0 ? (
        <EmptyState title={t('未找到渠道')} description={t('请尝试清除搜索词或查看其他页面。')} />
      ) : visibleChannels.length === 0 ? (
        <EmptyState title={t('没有渠道符合筛选条件')} description={t('清除表格筛选条件以显示已加载的数据。')} />
      ) : (
        <>
          <div className="border rounded-lg overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>ID</TableHead>
                  <SortableHeader<Channel> columnKey="name" sort={sort} onSortChange={setSort}>
                    {t('名称')}
                  </SortableHeader>
                  <SortableHeader<Channel> columnKey="type" sort={sort} onSortChange={setSort}>
                    {t('类型')}
                  </SortableHeader>
                  <SortableHeader<Channel> columnKey="group" sort={sort} onSortChange={setSort}>
                    {t('分组')}
                  </SortableHeader>
                  <SortableHeader<Channel> columnKey="priority" sort={sort} onSortChange={setSort} className="hidden lg:table-cell">
                    {t('优先级')}
                  </SortableHeader>
                  <SortableHeader<Channel> columnKey="balance" sort={sort} onSortChange={setSort} className="hidden md:table-cell">
                    {t('余额')}
                  </SortableHeader>
                  <TableHead className="hidden xl:table-cell">{t('健康状态')}</TableHead>
                  <SortableHeader<Channel> columnKey="status" sort={sort} onSortChange={setSort}>
                    {t('状态')}
                  </SortableHeader>
                  <TableHead className="text-right">{t('操作')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {visibleChannels.map((ch) => (
                  <TableRow key={ch.id}>
                    <TableCell className="font-mono text-sm">{ch.id}</TableCell>
                    <TableCell className="font-medium">{ch.name}</TableCell>
                    <TableCell>{PROVIDER_NAMES[ch.type] || `${t('类型')} ${ch.type}`}</TableCell>
                    <TableCell>{ch.group}</TableCell>
                    <TableCell className="hidden lg:table-cell">{ch.priority ?? 0}</TableCell>
                    <TableCell className="hidden md:table-cell">
                      {ch.balance != null ? `$${ch.balance.toFixed(2)}` : '$0.00'}
                    </TableCell>
                    <TableCell className="hidden xl:table-cell">
                      <div className="flex flex-col gap-1">
                        <span className={`inline-flex w-fit items-center rounded-full px-2 py-1 text-xs font-medium ${healthBadgeClass(channelHealthStatus(ch))}`}>
                          {healthStatusLabel(channelHealthStatus(ch))}
                        </span>
                        {channelHealthFailures(ch) > 0 && (
                          <span className="text-xs text-muted-foreground">
                            {channelHealthFailures(ch)} {t('次失败')}
                            {channelCircuitUntil(ch) > 0 ? ` · ${t('持续至')} ${new Date(channelCircuitUntil(ch) * 1000).toLocaleString(locale())}` : ''}
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
                        {ch.status === 1 ? t('启用') : t('禁用')}
                      </span>
                    </TableCell>
                    <TableCell className="text-right space-x-2">
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => openEdit(ch)}
                      >
                        <Pencil className="size-3.5" />
                        {t('编辑')}
                      </Button>
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => refreshBalanceMutation.mutate(ch.id)}
                        disabled={refreshBalanceMutation.isPending}
                      >
                        <RefreshCw className="size-3.5" />
                        {t('刷新')}
                      </Button>
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => testChannelMutation.mutate(ch.id)}
                        disabled={testChannelMutation.isPending}
                      >
                        {t('测试')}
                      </Button>
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() =>
                          toggleStatusMutation.mutate({ id: ch.id, currentStatus: ch.status })
                        }
                        disabled={toggleStatusMutation.isPending}
                      >
                        {ch.status === 1 ? t('禁用') : t('启用')}
                      </Button>
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => {
                          if (confirm(t(`确认删除渠道「${ch.name}」？此操作不可撤销。`))) {
                            deleteChannelMutation.mutate(ch.id);
                          }
                        }}
                        disabled={deleteChannelMutation.isPending}
                      >
                        <Trash2 className="size-3.5" />
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
            hasNextPage={!!channels && channels.length >= pageSize}
            onPageChange={setPage}
            onPageSizeChange={setPageSize}
          />
        </>
      )}
    </div>
  );
}
