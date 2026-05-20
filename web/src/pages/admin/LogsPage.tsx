import { useQuery } from '@tanstack/react-query';
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

interface LogEntry {
  id: string;
  userId: string;
  type: string;
  amount: string;
  balanceAfter: string;
  referenceId: string;
  remark: string;
  createdAt: string;
}

const LOG_TYPE_NAMES: Record<string, string> = {
  redeem: 'Redeem',
  recharge: 'Recharge',
  consume: 'Consume',
  refund: 'Refund',
};

export function AdminLogsPage() {
  const [page, setPage] = useState(1);
  const [userId, setUserId] = useState('');
  const [type, setType] = useState('');

  const { data: logs, isLoading } = useQuery({
    queryKey: ['admin-logs', page, userId, type],
    queryFn: async () => {
      const params = new URLSearchParams();
      params.set('page', page.toString());
      params.set('page_size', '50');
      if (userId) params.set('user_id', userId);
      if (type) params.set('type', type);
      const res = await adminApiClient.get(`/log?${params}`);
      return res.data.data as LogEntry[];
    },
  });

  function formatQuota(q: string) {
    return (parseInt(q || '0') / 500000).toFixed(2);
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-2xl font-semibold">Billing Logs</h2>
      </div>

      <div className="flex items-center gap-4">
        <Input
          placeholder="User ID"
          value={userId}
          onChange={(e) => setUserId(e.target.value)}
          className="max-w-xs"
        />
        <select
          value={type}
          onChange={(e) => setType(e.target.value)}
          className="border rounded px-3 py-2 text-sm"
        >
          <option value="">All Types</option>
          <option value="redeem">Redeem</option>
          <option value="recharge">Recharge</option>
          <option value="consume">Consume</option>
          <option value="refund">Refund</option>
        </select>
        <Button
          variant="outline"
          onClick={() => {
            setUserId('');
            setType('');
          }}
        >
          Clear
        </Button>
      </div>

      {isLoading ? (
        <p className="text-muted-foreground">Loading logs...</p>
      ) : !logs || logs.length === 0 ? (
        <p className="text-muted-foreground">No logs found.</p>
      ) : (
        <>
          <div className="border rounded-lg overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>ID</TableHead>
                  <TableHead>User ID</TableHead>
                  <TableHead>Type</TableHead>
                  <TableHead>Amount</TableHead>
                  <TableHead>Balance After</TableHead>
                  <TableHead>Reference</TableHead>
                  <TableHead>Remark</TableHead>
                  <TableHead>Created At</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {logs.map((log) => (
                  <TableRow key={log.id}>
                    <TableCell className="font-mono text-sm">{log.id}</TableCell>
                    <TableCell className="font-mono text-sm">{log.userId}</TableCell>
                    <TableCell>
                      <span className="inline-flex items-center px-2 py-1 rounded-full text-xs font-medium bg-blue-100 text-blue-800 dark:bg-blue-900 dark:text-blue-200">
                        {LOG_TYPE_NAMES[log.type] || log.type}
                      </span>
                    </TableCell>
                    <TableCell>{formatQuota(log.amount)}</TableCell>
                    <TableCell>{formatQuota(log.balanceAfter)}</TableCell>
                    <TableCell className="font-mono text-xs">{log.referenceId || '—'}</TableCell>
                    <TableCell className="max-w-xs truncate">{log.remark || '—'}</TableCell>
                    <TableCell>
                      {new Date(parseInt(log.createdAt) * 1000).toLocaleString()}
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
            <Button variant="outline" onClick={() => setPage((p) => p + 1)} disabled={!logs || logs.length < 50}>
              Next
            </Button>
          </div>
        </>
      )}
    </div>
  );
}
