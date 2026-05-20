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
  const [page, setPage] = useState(1);
  const [search, setSearch] = useState('');
  const queryClient = useQueryClient();

  const { data: channels, isLoading } = useQuery({
    queryKey: ['admin-channels', page, search],
    queryFn: async () => {
      const params = new URLSearchParams();
      params.set('page', page.toString());
      params.set('page_size', '20');
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
    },
  });

  const refreshBalanceMutation = useMutation({
    mutationFn: async (id: string) => {
      await adminApiClient.get(`/channel/update_balance/${id}`);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin-channels'] });
    },
  });

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-2xl font-semibold">Channels Management</h2>
      </div>

      <div className="flex items-center gap-4">
        <Input
          placeholder="Search by name..."
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="max-w-sm"
        />
        <Button variant="outline" onClick={() => setSearch('')}>
          Clear
        </Button>
      </div>

      {isLoading ? (
        <p className="text-muted-foreground">Loading channels...</p>
      ) : !channels || channels.length === 0 ? (
        <p className="text-muted-foreground">No channels found.</p>
      ) : (
        <>
          <div className="border rounded-lg overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>ID</TableHead>
                  <TableHead>Name</TableHead>
                  <TableHead>Type</TableHead>
                  <TableHead>Group</TableHead>
                  <TableHead>Priority</TableHead>
                  <TableHead>Balance</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead className="text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {channels.map((ch) => (
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

          <div className="flex items-center justify-between">
            <Button variant="outline" onClick={() => setPage((p) => Math.max(1, p - 1))} disabled={page === 1}>
              Previous
            </Button>
            <span className="text-sm text-muted-foreground">Page {page}</span>
            <Button variant="outline" onClick={() => setPage((p) => p + 1)} disabled={!channels || channels.length < 20}>
              Next
            </Button>
          </div>
        </>
      )}
    </div>
  );
}
