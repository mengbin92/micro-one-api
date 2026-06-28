import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { KeyRound, Pencil, Save } from 'lucide-react';
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
import { SortableHeader } from '@/components/admin/SortableHeader';
import { useAdminTableState } from '@/hooks/useAdminTableState';
import { buildAdminListParams } from '@/lib/admin-table-query';
import { ensureApiSuccess } from '@/lib/api-response';
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

// Mirrors common.v1.SubscriptionAccountSummary JSON tags returned by
// GET /api/subscription-accounts (alias of /v1/subscription-accounts).
interface SubscriptionAccountSummary {
  id: number;
  name: string;
  platform: string;
  accountType: string;
  status: number;
  group: string;
  models: string;
  priority: number;
  accountId: string;
  expiresAt: number;
  updatedAt: number;
}

// Mirrors common.v1.SubscriptionAccountInfo JSON tags returned by
// GET /api/subscription-accounts/{id}. The protobuf-generated JSON uses
// snake_case keys, and nullable string fields come back as null, so they are
// typed as optional and coerced to "" in toDraft().
interface SubscriptionAccountInfo {
  id: number;
  name: string;
  platform: string;
  account_type: string;
  status: number;
  group: string;
  models: string;
  priority: number;
  base_url: string | null;
  access_token: string;
  refresh_token: string;
  expires_at: number;
  account_id: string | null;
  fingerprint: string | null;
  metadata: string | null;
  created_at: number;
  updated_at: number;
}

interface SubscriptionAccountEditDraft {
  id: number;
  name: string;
  accountType: string;
  group: string;
  models: string;
  priority: string;
  baseUrl: string;
  accessToken: string;
  refreshToken: string;
  expiresAt: string;
  accountId: string;
  fingerprint: string;
  metadata: string;
}

interface CreatePayload {
  name: string;
  platform: string;
  account_type: string;
  group: string;
  models: string;
  priority: number;
  base_url: string;
  access_token: string;
  refresh_token: string;
  expires_at: number;
  account_id: string;
  fingerprint: string;
  metadata: string;
}

interface UpdatePayload {
  id: number;
  name: string;
  account_type: string;
  group: string;
  models: string;
  priority: number;
  base_url: string;
  access_token: string;
  refresh_token: string;
  expires_at: number;
  account_id: string;
  fingerprint: string;
  metadata: string;
}

// Subscription account platforms supported by the hybrid relay adaptor layer
// (internal/relay/identity + internal/relay/credential). Keep in sync with
// PlatformCodex / PlatformClaude.
const PLATFORM_OPTIONS: Array<{ value: string; label: string }> = [
  { value: 'claude', label: 'Claude (Claude Code OAuth)' },
  { value: 'codex', label: 'Codex (ChatGPT OAuth)' },
];

const ACCOUNT_TYPE_OPTIONS: Array<{ value: string; label: string }> = [
  { value: 'oauth', label: 'OAuth 订阅账号' },
];

function platformLabel(platform: string) {
  return PLATFORM_OPTIONS.find((option) => option.value === platform)?.label ?? platform;
}

function statusBadgeClass(status: number) {
  return status === 1
    ? 'bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200'
    : 'bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-200';
}

function statusLabel(status: number) {
  return status === 1 ? 'Active' : 'Disabled';
}

function formatTimestamp(unix: number) {
  if (!unix) return '—';
  return new Date(unix * 1000).toLocaleString();
}

function toDraft(account: SubscriptionAccountInfo): SubscriptionAccountEditDraft {
  return {
    id: account.id,
    name: account.name,
    accountType: account.account_type,
    group: account.group,
    models: account.models,
    priority: String(account.priority ?? 0),
    baseUrl: account.base_url ?? '',
    accessToken: '',
    refreshToken: '',
    expiresAt: account.expires_at ? String(account.expires_at) : '',
    accountId: account.account_id ?? '',
    fingerprint: account.fingerprint ?? '',
    metadata: account.metadata ?? '',
  };
}

export function AdminSubscriptionAccountsPage() {
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
    storageKey: 'subscription-accounts',
    filters: ['status', 'platform'],
  });
  const [isCreateOpen, setIsCreateOpen] = useState(false);
  const [editingAccount, setEditingAccount] = useState<SubscriptionAccountEditDraft | null>(null);
  const queryClient = useQueryClient();

  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: ['admin-subscription-accounts'] });
  };

  const sort = {
    key: sortKey as keyof SubscriptionAccountSummary | null,
    direction: sortDirection,
  } satisfies SortState<SubscriptionAccountSummary>;
  const statusFilter = filters.status ?? '';
  const platformFilter = filters.platform ?? '';

  const { data: accounts, isLoading } = useQuery({
    queryKey: ['admin-subscription-accounts', page, pageSize, search, statusFilter, platformFilter, sortKey, sortDirection],
    queryFn: async () => {
      const params = buildAdminListParams({
        page,
        pageSize,
        search,
        sortKey,
        sortDirection,
        filters: { status: statusFilter, platform: platformFilter },
      });
      const res = await adminApiClient.get(`/subscription-accounts?${params}`);
      const payload = res.data as { accounts?: SubscriptionAccountSummary[]; total?: number };
      return payload.accounts ?? [];
    },
  });

  const createMutation = useMutation({
    mutationFn: async (payload: CreatePayload) => {
      const res = await adminApiClient.post('/subscription-accounts', payload);
      ensureApiSuccess(res.data, 'Subscription account create failed');
    },
    onSuccess: () => {
      invalidate();
      setIsCreateOpen(false);
      toast.success('订阅账号已创建');
    },
  });

  const updateMutation = useMutation({
    mutationFn: async (draft: SubscriptionAccountEditDraft) => {
      // Only send token fields when the admin entered a new value; otherwise
      // leave them empty so the backend keeps the stored (encrypted) value.
      const payload: UpdatePayload = {
        id: draft.id,
        name: draft.name.trim(),
        account_type: draft.accountType,
        group: draft.group.trim(),
        models: draft.models.trim(),
        priority: parseInt(draft.priority || '0', 10),
        base_url: draft.baseUrl.trim(),
        access_token: draft.accessToken.trim(),
        refresh_token: draft.refreshToken.trim(),
        expires_at: draft.expiresAt ? parseInt(draft.expiresAt, 10) : 0,
        account_id: draft.accountId.trim(),
        fingerprint: draft.fingerprint,
        metadata: draft.metadata,
      };
      const res = await adminApiClient.put(`/subscription-accounts/${draft.id}`, payload);
      ensureApiSuccess(res.data, 'Subscription account update failed');
    },
    onSuccess: () => {
      invalidate();
      setEditingAccount(null);
      toast.success('订阅账号配置已保存');
    },
  });

  const toggleStatusMutation = useMutation({
    mutationFn: async ({ id, currentStatus }: { id: number; currentStatus: number }) => {
      const nextStatus = currentStatus === 1 ? 2 : 1;
      const res = await adminApiClient.put(`/subscription-accounts/${id}/status`, {
        account_id: id,
        status: nextStatus,
      });
      ensureApiSuccess(res.data, 'Subscription account status update failed');
    },
    onSuccess: () => {
      invalidate();
      toast.success('订阅账号状态已更新');
    },
  });

  const deleteMutation = useMutation({
    mutationFn: async (id: number) => {
      const res = await adminApiClient.delete(`/subscription-accounts/${id}`);
      ensureApiSuccess(res.data, 'Subscription account delete failed');
    },
    onSuccess: () => {
      invalidate();
      toast.success('订阅账号已删除');
    },
  });

  const openEdit = async (account: SubscriptionAccountSummary) => {
    try {
      const res = await adminApiClient.get(`/subscription-accounts/${account.id}`);
      const info = res.data as SubscriptionAccountInfo;
      setEditingAccount(toDraft(info));
    } catch {
      toast.error('加载订阅账号详情失败');
    }
  };

  const visibleAccounts = useMemo(() => {
    return sortRows(accounts ?? [], sort);
  }, [accounts, sort]);

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-2xl font-semibold">订阅账号管理</h2>
        <CreateAccountDialog
          open={isCreateOpen}
          onOpenChange={setIsCreateOpen}
          onSubmit={(payload) => createMutation.mutate(payload)}
          pending={createMutation.isPending}
        />
      </div>

      <AdminTableToolbar
        search={search}
        searchPlaceholder="按名称搜索..."
        onSearchChange={setSearch}
        onClear={clearSearch}
        actions={
          <div className="flex items-center gap-2">
            <select
              aria-label="按平台筛选"
              value={platformFilter}
              onChange={(event) => setFilter('platform', event.target.value)}
              className="h-8 rounded-md border bg-background px-2 text-sm"
            >
              <option value="">全部平台</option>
              {PLATFORM_OPTIONS.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </select>
            <select
              aria-label="按状态筛选"
              value={statusFilter}
              onChange={(event) => setFilter('status', event.target.value)}
              className="h-8 rounded-md border bg-background px-2 text-sm"
            >
              <option value="">全部状态</option>
              <option value="1">Active</option>
              <option value="2">Disabled</option>
            </select>
          </div>
        }
      />

      {isLoading ? (
        <TableSkeleton columns={['ID', '名称', '平台', '分组', '优先级', '过期时间', '状态', '操作']} />
      ) : !accounts || accounts.length === 0 ? (
        <EmptyState title="暂无订阅账号" description="新建一个 Claude / Codex 订阅账号以启用混合中继。" />
      ) : visibleAccounts.length === 0 ? (
        <EmptyState title="没有匹配的订阅账号" description="清除筛选条件以查看已加载的账号。" />
      ) : (
        <>
          <div className="border rounded-lg">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>ID</TableHead>
                  <SortableHeader<SubscriptionAccountSummary> columnKey="name" sort={sort} onSortChange={setSort}>
                    名称
                  </SortableHeader>
                  <SortableHeader<SubscriptionAccountSummary> columnKey="platform" sort={sort} onSortChange={setSort}>
                    平台
                  </SortableHeader>
                  <SortableHeader<SubscriptionAccountSummary> columnKey="group" sort={sort} onSortChange={setSort}>
                    分组
                  </SortableHeader>
                  <SortableHeader<SubscriptionAccountSummary> columnKey="priority" sort={sort} onSortChange={setSort} className="hidden lg:table-cell">
                    优先级
                  </SortableHeader>
                  <SortableHeader<SubscriptionAccountSummary> columnKey="expiresAt" sort={sort} onSortChange={setSort} className="hidden xl:table-cell">
                    过期时间
                  </SortableHeader>
                  <SortableHeader<SubscriptionAccountSummary> columnKey="status" sort={sort} onSortChange={setSort}>
                    状态
                  </SortableHeader>
                  <TableHead className="text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {visibleAccounts.map((account) => (
                  <TableRow key={account.id}>
                    <TableCell className="font-mono text-sm">{account.id}</TableCell>
                    <TableCell className="font-medium">{account.name}</TableCell>
                    <TableCell>{platformLabel(account.platform)}</TableCell>
                    <TableCell>{account.group}</TableCell>
                    <TableCell className="hidden lg:table-cell">{account.priority ?? 0}</TableCell>
                    <TableCell className="hidden xl:table-cell">{formatTimestamp(account.expiresAt)}</TableCell>
                    <TableCell>
                      <span
                        className={`inline-flex items-center px-2 py-1 rounded-full text-xs font-medium ${statusBadgeClass(account.status)}`}
                      >
                        {statusLabel(account.status)}
                      </span>
                    </TableCell>
                    <TableCell className="text-right space-x-2">
                      <Button variant="outline" size="sm" onClick={() => openEdit(account)}>
                        <Pencil className="size-3.5" />
                        编辑
                      </Button>
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => toggleStatusMutation.mutate({ id: account.id, currentStatus: account.status })}
                        disabled={toggleStatusMutation.isPending}
                      >
                        {account.status === 1 ? '停用' : '启用'}
                      </Button>
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => {
                          if (confirm(`确认删除订阅账号「${account.name}」？`)) {
                            deleteMutation.mutate(account.id);
                          }
                        }}
                        disabled={deleteMutation.isPending}
                      >
                        删除
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
            hasNextPage={!!accounts && accounts.length >= pageSize}
            onPageChange={setPage}
            onPageSizeChange={setPageSize}
          />
        </>
      )}

      <EditAccountDialog
        draft={editingAccount}
        onDraftChange={setEditingAccount}
        onSubmit={() => editingAccount && updateMutation.mutate(editingAccount)}
        pending={updateMutation.isPending}
        platform={editingAccount?.accountType ? editingAccount.accountType : 'oauth'}
      />
    </div>
  );
}

interface CreateAccountDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSubmit: (payload: CreatePayload) => void;
  pending: boolean;
}

const emptyCreateState = {
  name: '',
  platform: 'claude',
  accountType: 'oauth',
  group: 'default',
  models: '',
  priority: '0',
  baseUrl: '',
  accessToken: '',
  refreshToken: '',
  expiresAt: '',
  accountId: '',
  fingerprint: '',
  metadata: '',
};

function CreateAccountDialog({ open, onOpenChange, onSubmit, pending }: CreateAccountDialogProps) {
  const [form, setForm] = useState({ ...emptyCreateState });

  const handleSubmit = () => {
    if (!form.name.trim() || !form.accessToken.trim() || !form.refreshToken.trim()) {
      toast.error('名称、access_token、refresh_token 为必填项');
      return;
    }
    onSubmit({
      name: form.name.trim(),
      platform: form.platform,
      account_type: form.accountType,
      group: form.group.trim(),
      models: form.models.trim(),
      priority: parseInt(form.priority || '0', 10),
      base_url: form.baseUrl.trim(),
      access_token: form.accessToken.trim(),
      refresh_token: form.refreshToken.trim(),
      expires_at: form.expiresAt ? parseInt(form.expiresAt, 10) : 0,
      account_id: form.accountId.trim(),
      fingerprint: form.fingerprint,
      metadata: form.metadata,
    });
    setForm({ ...emptyCreateState });
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger render={<Button />}>
        <KeyRound className="size-4" />
        新建订阅账号
      </DialogTrigger>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>新建订阅账号</DialogTitle>
          <DialogDescription>
            添加 Claude / Codex OAuth 订阅账号，用于混合中继的身份伪装与协议转换。
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-4 pt-2 sm:grid-cols-2">
          <div className="space-y-2">
            <Label htmlFor="sub-name">名称</Label>
            <Input
              id="sub-name"
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
              placeholder="claude-pro-1"
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="sub-platform">平台</Label>
            <select
              id="sub-platform"
              value={form.platform}
              onChange={(e) => setForm({ ...form, platform: e.target.value })}
              className="h-8 w-full rounded-lg border border-input bg-background px-2.5 text-sm"
            >
              {PLATFORM_OPTIONS.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-2">
            <Label htmlFor="sub-account-type">账号类型</Label>
            <select
              id="sub-account-type"
              value={form.accountType}
              onChange={(e) => setForm({ ...form, accountType: e.target.value })}
              className="h-8 w-full rounded-lg border border-input bg-background px-2.5 text-sm"
            >
              {ACCOUNT_TYPE_OPTIONS.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-2">
            <Label htmlFor="sub-group">分组</Label>
            <Input
              id="sub-group"
              value={form.group}
              onChange={(e) => setForm({ ...form, group: e.target.value })}
            />
          </div>
          <div className="space-y-2 sm:col-span-2">
            <Label htmlFor="sub-models">模型（逗号分隔）</Label>
            <Input
              id="sub-models"
              value={form.models}
              onChange={(e) => setForm({ ...form, models: e.target.value })}
              placeholder="claude-sonnet-4-5,claude-opus-4-1"
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="sub-priority">优先级</Label>
            <Input
              id="sub-priority"
              type="number"
              value={form.priority}
              onChange={(e) => setForm({ ...form, priority: e.target.value })}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="sub-expires">过期时间（Unix 秒）</Label>
            <Input
              id="sub-expires"
              type="number"
              value={form.expiresAt}
              onChange={(e) => setForm({ ...form, expiresAt: e.target.value })}
            />
          </div>
          <div className="space-y-2 sm:col-span-2">
            <Label htmlFor="sub-base-url">Base URL（可选，留空走默认上游）</Label>
            <Input
              id="sub-base-url"
              value={form.baseUrl}
              onChange={(e) => setForm({ ...form, baseUrl: e.target.value })}
              placeholder="https://api.anthropic.com"
            />
          </div>
          <div className="space-y-2 sm:col-span-2">
            <Label htmlFor="sub-access-token">Access Token</Label>
            <Input
              id="sub-access-token"
              type="password"
              value={form.accessToken}
              onChange={(e) => setForm({ ...form, accessToken: e.target.value })}
              placeholder="sk-ant-..."
            />
          </div>
          <div className="space-y-2 sm:col-span-2">
            <Label htmlFor="sub-refresh-token">Refresh Token</Label>
            <Input
              id="sub-refresh-token"
              type="password"
              value={form.refreshToken}
              onChange={(e) => setForm({ ...form, refreshToken: e.target.value })}
            />
          </div>
          <div className="space-y-2 sm:col-span-2">
            <Label htmlFor="sub-account-id">上游 Account ID（可选）</Label>
            <Input
              id="sub-account-id"
              value={form.accountId}
              onChange={(e) => setForm({ ...form, accountId: e.target.value })}
              placeholder="chatgpt-account-id / claude account uuid"
            />
          </div>
          <div className="space-y-2 sm:col-span-2">
            <Label htmlFor="sub-fingerprint">指纹（JSON，可选）</Label>
            <Input
              id="sub-fingerprint"
              value={form.fingerprint}
              onChange={(e) => setForm({ ...form, fingerprint: e.target.value })}
              placeholder='{"user_agent":"..."}'
            />
          </div>
          <div className="space-y-2 sm:col-span-2">
            <Label htmlFor="sub-metadata">Metadata（JSON，可选）</Label>
            <Input
              id="sub-metadata"
              value={form.metadata}
              onChange={(e) => setForm({ ...form, metadata: e.target.value })}
            />
          </div>
          <Button onClick={handleSubmit} disabled={pending} className="sm:col-span-2">
            {pending ? '创建中...' : '创建'}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

interface EditAccountDialogProps {
  draft: SubscriptionAccountEditDraft | null;
  onDraftChange: (draft: SubscriptionAccountEditDraft | null) => void;
  onSubmit: () => void;
  pending: boolean;
  platform: string;
}

function EditAccountDialog({ draft, onDraftChange, onSubmit, pending }: EditAccountDialogProps) {
  const handleUpdate = () => {
    if (!draft) return;
    if (!draft.name.trim() || !draft.group.trim() || !draft.models.trim()) {
      toast.error('名称、模型、分组为必填项');
      return;
    }
    onSubmit();
  };

  return (
    <Dialog open={!!draft} onOpenChange={(open) => !open && onDraftChange(null)}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>编辑订阅账号</DialogTitle>
          <DialogDescription>
            修改路由与凭证配置。access_token / refresh_token 留空则保留原值（服务端已脱敏，无法回显）。
          </DialogDescription>
        </DialogHeader>
        {draft && (
          <div className="grid gap-4 pt-2 sm:grid-cols-2">
            <div className="space-y-2 sm:col-span-2">
              <Label htmlFor="edit-sub-name">名称</Label>
              <Input
                id="edit-sub-name"
                value={draft.name}
                onChange={(e) => onDraftChange({ ...draft, name: e.target.value })}
              />
            </div>
            <div className="space-y-2 sm:col-span-2">
              <Label htmlFor="edit-sub-models">模型（逗号分隔）</Label>
              <Input
                id="edit-sub-models"
                value={draft.models}
                onChange={(e) => onDraftChange({ ...draft, models: e.target.value })}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="edit-sub-group">分组</Label>
              <Input
                id="edit-sub-group"
                value={draft.group}
                onChange={(e) => onDraftChange({ ...draft, group: e.target.value })}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="edit-sub-priority">优先级</Label>
              <Input
                id="edit-sub-priority"
                type="number"
                value={draft.priority}
                onChange={(e) => onDraftChange({ ...draft, priority: e.target.value })}
              />
            </div>
            <div className="space-y-2 sm:col-span-2">
              <Label htmlFor="edit-sub-base-url">Base URL</Label>
              <Input
                id="edit-sub-base-url"
                value={draft.baseUrl}
                onChange={(e) => onDraftChange({ ...draft, baseUrl: e.target.value })}
              />
            </div>
            <div className="space-y-2 sm:col-span-2">
              <Label htmlFor="edit-sub-access-token">Access Token（留空保留原值）</Label>
              <Input
                id="edit-sub-access-token"
                type="password"
                value={draft.accessToken}
                onChange={(e) => onDraftChange({ ...draft, accessToken: e.target.value })}
              />
            </div>
            <div className="space-y-2 sm:col-span-2">
              <Label htmlFor="edit-sub-refresh-token">Refresh Token（留空保留原值）</Label>
              <Input
                id="edit-sub-refresh-token"
                type="password"
                value={draft.refreshToken}
                onChange={(e) => onDraftChange({ ...draft, refreshToken: e.target.value })}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="edit-sub-expires">过期时间（Unix 秒）</Label>
              <Input
                id="edit-sub-expires"
                type="number"
                value={draft.expiresAt}
                onChange={(e) => onDraftChange({ ...draft, expiresAt: e.target.value })}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="edit-sub-account-id">上游 Account ID</Label>
              <Input
                id="edit-sub-account-id"
                value={draft.accountId}
                onChange={(e) => onDraftChange({ ...draft, accountId: e.target.value })}
              />
            </div>
            <div className="space-y-2 sm:col-span-2">
              <Label htmlFor="edit-sub-fingerprint">指纹（JSON）</Label>
              <Input
                id="edit-sub-fingerprint"
                value={draft.fingerprint}
                onChange={(e) => onDraftChange({ ...draft, fingerprint: e.target.value })}
              />
            </div>
            <div className="space-y-2 sm:col-span-2">
              <Label htmlFor="edit-sub-metadata">Metadata（JSON）</Label>
              <Input
                id="edit-sub-metadata"
                value={draft.metadata}
                onChange={(e) => onDraftChange({ ...draft, metadata: e.target.value })}
              />
            </div>
            <Button
              onClick={handleUpdate}
              disabled={pending || !draft.name.trim() || !draft.models.trim() || !draft.group.trim()}
              className="sm:col-span-2"
            >
              <Save className="size-4" />
              {pending ? '保存中...' : '保存配置'}
            </Button>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
