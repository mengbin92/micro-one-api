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

interface User {
  id: string;
  username: string;
  displayName: string;
  email: string;
  group: string;
  status: number;
  quota: string;
  usedQuota: string;
  createdAt: string;
}

export function AdminUsersPage() {
  const { page, pageSize, search, setPage, setPageSize, setSearch, clearSearch } = useAdminTableState({
    storageKey: 'users',
  });
  const [sort, setSort] = useState<SortState<User>>({ key: null, direction: null });
  const [statusFilter, setStatusFilter] = useState('');
  const [groupFilter, setGroupFilter] = useState('');
  const queryClient = useQueryClient();

  const { data: users, isLoading } = useQuery({
    queryKey: ['admin-users', page, pageSize, search],
    queryFn: async () => {
      const params = new URLSearchParams();
      params.set('page', page.toString());
      params.set('page_size', pageSize.toString());
      if (search) params.set('keyword', search);
      const res = await adminApiClient.get(`/user?${params}`);
      return res.data.data as User[];
    },
  });

  const toggleStatusMutation = useMutation({
    mutationFn: async ({ id, currentStatus }: { id: string; currentStatus: number }) => {
      const newStatus = currentStatus === 1 ? 2 : 1;
      await adminApiClient.put(`/user/${id}`, { status: newStatus });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin-users'] });
      toast.success('User status updated');
    },
  });

  function formatQuota(q: string) {
    return (parseInt(q || '0') / 500000).toFixed(2);
  }

  const visibleUsers = useMemo(() => {
    const filtered = (users ?? []).filter((user) => {
      const statusMatches = !statusFilter || String(user.status) === statusFilter;
      const groupMatches = !groupFilter || user.group.toLowerCase().includes(groupFilter.toLowerCase());
      return statusMatches && groupMatches;
    });
    return sortRows(filtered, sort);
  }, [users, statusFilter, groupFilter, sort]);

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-2xl font-semibold">Users Management</h2>
      </div>

      <AdminTableToolbar
        search={search}
        searchPlaceholder="Search by username or email..."
        onSearchChange={setSearch}
        onClear={clearSearch}
        actions={
          <ExportButton
            filename="admin-users.csv"
            rows={visibleUsers}
            columns={[
              { key: 'id', label: 'ID' },
              { key: 'username', label: 'Username' },
              { key: 'displayName', label: 'Display Name' },
              { key: 'email', label: 'Email' },
              { key: 'group', label: 'Group' },
              { key: 'quota', label: 'Quota' },
              { key: 'usedQuota', label: 'Used Quota' },
              { key: 'status', label: 'Status' },
              { key: 'createdAt', label: 'Created At' },
            ]}
          />
        }
      />

      <div className="flex flex-wrap items-center gap-3">
        <select
          value={statusFilter}
          onChange={(event) => setStatusFilter(event.target.value)}
          className="h-8 rounded-md border bg-background px-2 text-sm"
          aria-label="Filter users by status"
        >
          <option value="">All statuses</option>
          <option value="1">Active</option>
          <option value="2">Disabled</option>
        </select>
        <input
          value={groupFilter}
          onChange={(event) => setGroupFilter(event.target.value)}
          placeholder="Filter group"
          className="h-8 w-40 rounded-md border bg-background px-2 text-sm"
          aria-label="Filter users by group"
        />
      </div>

      {isLoading ? (
        <TableSkeleton columns={['ID', 'Username', 'Display Name', 'Email', 'Group', 'Quota', 'Used', 'Status', 'Actions']} />
      ) : !users || users.length === 0 ? (
        <EmptyState title="No users found" description="Try clearing the search term or checking another page." />
      ) : visibleUsers.length === 0 ? (
        <EmptyState title="No users match the filters" description="Clear the table filters to show the loaded rows." />
      ) : (
        <>
          <div className="border rounded-lg">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>ID</TableHead>
                  <SortableHeader<User> columnKey="username" sort={sort} onSortChange={setSort}>
                    Username
                  </SortableHeader>
                  <TableHead>Display Name</TableHead>
                  <SortableHeader<User> columnKey="email" sort={sort} onSortChange={setSort}>
                    Email
                  </SortableHeader>
                  <SortableHeader<User> columnKey="group" sort={sort} onSortChange={setSort}>
                    Group
                  </SortableHeader>
                  <SortableHeader<User> columnKey="quota" sort={sort} onSortChange={setSort}>
                    Quota
                  </SortableHeader>
                  <TableHead>Used</TableHead>
                  <SortableHeader<User> columnKey="status" sort={sort} onSortChange={setSort}>
                    Status
                  </SortableHeader>
                  <TableHead className="text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {visibleUsers.map((user) => (
                  <TableRow key={user.id}>
                    <TableCell className="font-mono text-sm">{user.id}</TableCell>
                    <TableCell className="font-medium">{user.username}</TableCell>
                    <TableCell>{user.displayName || '—'}</TableCell>
                    <TableCell>{user.email || '—'}</TableCell>
                    <TableCell>{user.group}</TableCell>
                    <TableCell>{formatQuota(user.quota)}</TableCell>
                    <TableCell>{formatQuota(user.usedQuota)}</TableCell>
                    <TableCell>
                      <span
                        className={`inline-flex items-center px-2 py-1 rounded-full text-xs font-medium ${
                          user.status === 1
                            ? 'bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200'
                            : 'bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-200'
                        }`}
                      >
                        {user.status === 1 ? 'Active' : 'Disabled'}
                      </span>
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() =>
                          toggleStatusMutation.mutate({ id: user.id, currentStatus: user.status })
                        }
                        disabled={toggleStatusMutation.isPending}
                      >
                        {user.status === 1 ? 'Disable' : 'Enable'}
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
            hasNextPage={!!users && users.length >= pageSize}
            onPageChange={setPage}
            onPageSizeChange={setPageSize}
          />
        </>
      )}
    </div>
  );
}
