import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { useMemo } from 'react';
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
import { buildAdminListParams } from '@/lib/admin-table-query';
import { ensureApiSuccess, unwrapApiData } from '@/lib/api-response';
import { formatAmountUnits } from '@/lib/amount';
import { sortRows, type SortState } from '@/lib/table-utils';
import { t } from '@/lib/i18n';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';

interface User {
  id: string;
  username: string;
  displayName: string;
  email: string;
  group: string;
  status: number;
  role: number;
  balance: string;
  usedAmount: string;
  createdAt: string;
}

const ROLE_GUEST = 0;
const ROLE_COMMON = 1;
const ROLE_ADMIN = 10;
const ROLE_ROOT = 100;

function roleLabel(role: number) {
  switch (role) {
    case ROLE_ROOT:
      return t('超级管理员');
    case ROLE_ADMIN:
      return t('管理员');
    case ROLE_COMMON:
      return t('用户');
    case ROLE_GUEST:
      return t('访客');
    default:
      return String(role);
  }
}

function roleBadgeClass(role: number) {
  if (role >= ROLE_ROOT) {
    return 'bg-purple-100 text-purple-800 dark:bg-purple-900 dark:text-purple-200';
  }
  if (role >= ROLE_ADMIN) {
    return 'bg-blue-100 text-blue-800 dark:bg-blue-900 dark:text-blue-200';
  }
  if (role <= ROLE_GUEST) {
    return 'bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-300';
  }
  return 'bg-emerald-100 text-emerald-800 dark:bg-emerald-900 dark:text-emerald-200';
}

export function AdminUsersPage() {
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
    storageKey: 'users',
    filters: ['status', 'group'],
  });
  const queryClient = useQueryClient();
  const sort = { key: sortKey as keyof User | null, direction: sortDirection } satisfies SortState<User>;
  const statusFilter = filters.status ?? '';
  const groupFilter = filters.group ?? '';
  const exportParams = buildAdminListParams({ page, pageSize, search, sortKey, sortDirection, filters });
  exportParams.set('format', 'csv');
  const exportHref = `/user/export?${exportParams}`;

  const { data: users, isLoading } = useQuery({
    queryKey: ['admin-users', page, pageSize, search, sortKey, sortDirection, filters],
    queryFn: async () => {
      const params = buildAdminListParams({
        page,
        pageSize,
        search,
        sortKey,
        sortDirection,
        filters,
      });
      const res = await adminApiClient.get(`/user?${params}`);
      return unwrapApiData<User[]>(res.data);
    },
  });

  const toggleStatusMutation = useMutation({
    mutationFn: async ({ id, currentStatus }: { id: string; currentStatus: number }) => {
      let res;
      if (currentStatus === 1) {
        res = await adminApiClient.post(`/user/disable/${id}`);
      } else {
        res = await adminApiClient.post(`/user/enable/${id}`);
      }
      ensureApiSuccess(res.data, t('更新用户状态失败'));
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin-users'] });
      toast.success(t('用户状态已更新'));
    },
  });

  const setRoleMutation = useMutation({
    mutationFn: async ({ username, action }: { username: string; action: 'promote' | 'demote' }) => {
      const res = await adminApiClient.post('/user/manage', { username, action });
      ensureApiSuccess(res.data, t('更新角色失败'));
    },
    onSuccess: (_data, variables) => {
      queryClient.invalidateQueries({ queryKey: ['admin-users'] });
      toast.success(variables.action === 'promote' ? t('用户已提升为管理员') : t('管理员已降级为用户'));
    },
    onError: (error: unknown) => {
      const message = error instanceof Error ? error.message : t('更新角色失败');
      toast.error(message);
    },
  });

  function formatAmount(q: string) {
    return formatAmountUnits(q);
  }

  const visibleUsers = useMemo(() => {
    return sortRows(users ?? [], sort);
  }, [users, sort]);

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-2xl font-semibold">{t('用户管理')}</h2>
      </div>

      <AdminTableToolbar
        search={search}
        searchPlaceholder={t('按用户名或邮箱搜索...')}
        onSearchChange={setSearch}
        onClear={clearSearch}
        actions={
          <ExportButton
            filename="admin-users.csv"
            href={exportHref}
            rows={visibleUsers}
            columns={[
              { key: 'id', label: 'ID' },
              { key: 'username', label: t('用户名') },
              { key: 'displayName', label: t('显示名称') },
              { key: 'email', label: t('邮箱') },
              { key: 'group', label: t('分组') },
              { key: 'role', label: t('角色') },
              { key: 'balance', label: t('余额') },
              { key: 'usedAmount', label: t('已用金额') },
              { key: 'status', label: t('状态') },
              { key: 'createdAt', label: t('创建时间') },
            ]}
          />
        }
      />

      <div className="flex flex-wrap items-center gap-3">
        <select
          value={statusFilter}
          onChange={(event) => setFilter('status', event.target.value)}
          className="h-8 rounded-md border bg-background px-2 text-sm"
          aria-label={t('按状态筛选用户')}
        >
          <option value="">{t('全部状态')}</option>
          <option value="1">{t('启用')}</option>
          <option value="2">{t('禁用')}</option>
        </select>
        <input
          value={groupFilter}
          onChange={(event) => setFilter('group', event.target.value)}
          placeholder={t('筛选分组')}
          className="h-8 w-40 rounded-md border bg-background px-2 text-sm"
          aria-label={t('按分组筛选用户')}
        />
      </div>

      {isLoading ? (
        <TableSkeleton columns={['ID', t('用户名'), t('显示名称'), t('邮箱'), t('分组'), t('角色'), t('余额'), t('已用'), t('状态'), t('操作')]} />
      ) : !users || users.length === 0 ? (
        <EmptyState title={t('未找到用户')} description={t('请尝试清除搜索词或查看其他页面。')} />
      ) : visibleUsers.length === 0 ? (
        <EmptyState title={t('没有用户符合筛选条件')} description={t('清除表格筛选条件以显示已加载的数据。')} />
      ) : (
        <>
          <div className="border rounded-lg">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>ID</TableHead>
                  <SortableHeader<User> columnKey="username" sort={sort} onSortChange={setSort}>
                    {t('用户名')}
                  </SortableHeader>
                  <TableHead className="hidden lg:table-cell">{t('显示名称')}</TableHead>
                  <SortableHeader<User> columnKey="email" sort={sort} onSortChange={setSort}>
                    {t('邮箱')}
                  </SortableHeader>
                  <SortableHeader<User> columnKey="group" sort={sort} onSortChange={setSort}>
                    {t('分组')}
                  </SortableHeader>
                  <SortableHeader<User> columnKey="role" sort={sort} onSortChange={setSort}>
                    {t('角色')}
                  </SortableHeader>
                  <SortableHeader<User> columnKey="balance" sort={sort} onSortChange={setSort}>
                    {t('余额')}
                  </SortableHeader>
                  <TableHead>{t('已用')}</TableHead>
                  <SortableHeader<User> columnKey="status" sort={sort} onSortChange={setSort}>
                    {t('状态')}
                  </SortableHeader>
                  <TableHead className="text-right">{t('操作')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {visibleUsers.map((user) => {
                  const isRoot = user.role >= ROLE_ROOT;
                  const isAdmin = user.role >= ROLE_ADMIN && user.role < ROLE_ROOT;
                  const roleAction: 'promote' | 'demote' = isAdmin ? 'demote' : 'promote';
                  const roleActionLabel = isAdmin ? t('降级') : t('提升');
                  return (
                    <TableRow key={user.id}>
                      <TableCell className="font-mono text-sm">{user.id}</TableCell>
                      <TableCell className="font-medium">{user.username}</TableCell>
                      <TableCell className="hidden lg:table-cell">{user.displayName || '—'}</TableCell>
                      <TableCell className="max-w-56 truncate">{user.email || '—'}</TableCell>
                      <TableCell>{user.group}</TableCell>
                      <TableCell>
                        <span
                          className={`inline-flex items-center px-2 py-1 rounded-full text-xs font-medium ${roleBadgeClass(user.role)}`}
                        >
                          {roleLabel(user.role)}
                        </span>
                      </TableCell>
                      <TableCell>{formatAmount(user.balance)}</TableCell>
                      <TableCell>{formatAmount(user.usedAmount)}</TableCell>
                      <TableCell>
                        <span
                          className={`inline-flex items-center px-2 py-1 rounded-full text-xs font-medium ${
                            user.status === 1
                              ? 'bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200'
                              : 'bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-200'
                          }`}
                        >
                          {user.status === 1 ? t('启用') : t('禁用')}
                        </span>
                      </TableCell>
                      <TableCell className="text-right">
                        <div className="flex justify-end gap-2">
                          <Button
                            variant="outline"
                            size="sm"
                            onClick={() =>
                              setRoleMutation.mutate({ username: user.username, action: roleAction })
                            }
                            disabled={isRoot || setRoleMutation.isPending}
                            title={isRoot ? t('无法在此修改超级管理员角色') : undefined}
                          >
                            {roleActionLabel}
                          </Button>
                          <Button
                            variant="outline"
                            size="sm"
                            onClick={() =>
                              toggleStatusMutation.mutate({ id: user.id, currentStatus: user.status })
                            }
                            disabled={toggleStatusMutation.isPending}
                          >
                            {user.status === 1 ? t('禁用') : t('启用')}
                          </Button>
                        </div>
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          </div>

          <AdminPagination
            page={page}
            pageSize={pageSize}
            hasNextPage={!!users && users.length >= pageSize}
            onPageChange={setPage}
            onPageSizeChange={setPageSize}
          />
        </>
      )}
    </div>
  );
}
