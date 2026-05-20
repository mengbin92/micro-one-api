import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { adminApiClient } from '@/lib/api';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
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
  const [page, setPage] = useState(1);
  const [search, setSearch] = useState('');
  const queryClient = useQueryClient();

  const { data: users, isLoading } = useQuery({
    queryKey: ['admin-users', page, search],
    queryFn: async () => {
      const params = new URLSearchParams();
      params.set('page', page.toString());
      params.set('page_size', '20');
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
    },
  });

  function formatQuota(q: string) {
    return (parseInt(q || '0') / 500000).toFixed(2);
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-2xl font-semibold">Users Management</h2>
      </div>

      <div className="flex items-center gap-4">
        <Input
          placeholder="Search by username or email..."
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="max-w-sm"
        />
        <Button variant="outline" onClick={() => setSearch('')}>
          Clear
        </Button>
      </div>

      {isLoading ? (
        <p className="text-muted-foreground">Loading users...</p>
      ) : !users || users.length === 0 ? (
        <p className="text-muted-foreground">No users found.</p>
      ) : (
        <>
          <div className="border rounded-lg">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>ID</TableHead>
                  <TableHead>Username</TableHead>
                  <TableHead>Display Name</TableHead>
                  <TableHead>Email</TableHead>
                  <TableHead>Group</TableHead>
                  <TableHead>Quota</TableHead>
                  <TableHead>Used</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead className="text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {users.map((user) => (
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

          <div className="flex items-center justify-between">
            <Button variant="outline" onClick={() => setPage((p) => Math.max(1, p - 1))} disabled={page === 1}>
              Previous
            </Button>
            <span className="text-sm text-muted-foreground">Page {page}</span>
            <Button variant="outline" onClick={() => setPage((p) => p + 1)} disabled={!users || users.length < 20}>
              Next
            </Button>
          </div>
        </>
      )}
    </div>
  );
}
