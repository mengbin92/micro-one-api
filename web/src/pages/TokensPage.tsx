import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { Copy } from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
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
import { apiClient } from '@/lib/api';
import { EmptyState } from '@/components/EmptyState';
import { TableSkeleton } from '@/components/LoadingStates';
import { ensureApiSuccess, unwrapApiData } from '@/lib/api-response';

interface Token {
  id: number;
  name?: string;
  key?: string;
  masked_key?: string;
  status: number;
  remain_quota: number;
  created_time: number;
}

interface TokenListData {
  items?: Token[];
  total?: number;
}

function normalizeTokens(data: Token[] | TokenListData): Token[] {
  const onlyNamedTokens = (items: Token[]) => items.filter((token) => token.name?.trim());
  if (Array.isArray(data)) {
    return onlyNamedTokens(data);
  }
  if (Array.isArray(data?.items)) {
    return onlyNamedTokens(data.items);
  }
  return [];
}

export function TokensPage() {
  const [isCreateOpen, setIsCreateOpen] = useState(false);
  const [newTokenName, setNewTokenName] = useState('');
  const [createdToken, setCreatedToken] = useState<Token | null>(null);
  const [revealedKeys, setRevealedKeys] = useState<Record<number, string>>({});
  const queryClient = useQueryClient();

  const { data: tokens, isLoading } = useQuery({
    queryKey: ['tokens'],
    queryFn: async () => {
      const res = await apiClient.get('/token');
      return normalizeTokens(unwrapApiData<Token[] | TokenListData>(res.data));
    },
  });

  const createMutation = useMutation({
    mutationFn: async (name: string) => {
      const res = await apiClient.post('/token', { name });
      return unwrapApiData<Token>(res.data);
    },
    onSuccess: (token) => {
      if (token.key) {
        setRevealedKeys((current) => ({ ...current, [token.id]: token.key as string }));
      }
      setCreatedToken(token);
      queryClient.setQueryData<Token[]>(['tokens'], (current = []) => {
        const withoutCreated = current.filter((item) => item.id !== token.id);
        return [token, ...withoutCreated];
      });
      setNewTokenName('');
      toast.success('Token created');
    },
  });

  const deleteMutation = useMutation({
    mutationFn: async (id: number) => {
      const res = await apiClient.delete(`/token/${id}`);
      ensureApiSuccess(res.data, 'Token delete failed');
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['tokens'] });
      toast.success('Token deleted');
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : 'Token delete failed');
    },
  });

  const handleCreate = () => {
    if (newTokenName.trim()) {
      createMutation.mutate(newTokenName);
      return;
    }
    toast.error('Token name is required');
  };

  const handleCreateOpenChange = (open: boolean) => {
    setIsCreateOpen(open);
    if (!open) {
      setCreatedToken(null);
      setNewTokenName('');
    }
  };

  const copyKey = async (key: string) => {
    try {
      await navigator.clipboard.writeText(key);
      toast.success('Token copied');
    } catch {
      toast.error('Unable to copy token');
    }
  };

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h2 className="text-2xl font-semibold">Tokens</h2>
        <Dialog open={isCreateOpen} onOpenChange={handleCreateOpenChange}>
          <DialogTrigger render={<Button />}>
            Create Token
          </DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Create New Token</DialogTitle>
              <DialogDescription>Enter a name for your new API token.</DialogDescription>
            </DialogHeader>
            <div className="space-y-4 pt-4">
              <div className="space-y-2">
                <Label htmlFor="token-name">Token Name</Label>
                <Input
                  id="token-name"
                  value={newTokenName}
                  onChange={(e) => setNewTokenName(e.target.value)}
                  placeholder="My Token"
                />
              </div>
              {createdToken?.key && (
                <div className="space-y-2">
                  <Label htmlFor="created-token-key">API Key</Label>
                  <div className="flex gap-2">
                    <Input id="created-token-key" readOnly value={createdToken.key} className="font-mono text-xs" />
                    <Button type="button" variant="outline" size="icon" onClick={() => copyKey(createdToken.key as string)} aria-label="Copy token">
                      <Copy />
                    </Button>
                  </div>
                </div>
              )}
              <Button
                onClick={handleCreate}
                disabled={createMutation.isPending || !newTokenName.trim()}
                className="w-full"
              >
                {createMutation.isPending ? 'Creating...' : 'Create'}
              </Button>
            </div>
          </DialogContent>
        </Dialog>
      </div>

      {isLoading ? (
        <TableSkeleton columns={['Name', 'Key', 'Status', 'Quota', 'Created', 'Actions']} />
      ) : !tokens || tokens.length === 0 ? (
        <EmptyState title="No tokens yet" description="Create a token to start calling the API." />
      ) : (
        <div className="overflow-x-auto rounded-lg border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Key</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Quota</TableHead>
                <TableHead>Created</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {tokens.map((token) => (
                <TableRow key={token.id}>
                  <TableCell className="font-medium">{token.name}</TableCell>
                  <TableCell className="font-mono text-sm">{revealedKeys[token.id] || token.key || token.masked_key || 'Hidden'}</TableCell>
                  <TableCell>
                    <span
                      className={`inline-flex items-center px-2 py-1 rounded-full text-xs font-medium ${
                        token.status === 1
                          ? 'bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200'
                          : 'bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-200'
                      }`}
                    >
                      {token.status === 1 ? 'Active' : 'Disabled'}
                    </span>
                  </TableCell>
                  <TableCell>{(token.remain_quota ?? 0).toLocaleString()}</TableCell>
                  <TableCell>
                    {token.created_time ? new Date(token.created_time * 1000).toLocaleDateString() : '—'}
                  </TableCell>
                  <TableCell className="text-right">
                    <Button
                      variant="destructive"
                      size="sm"
                      onClick={() => deleteMutation.mutate(token.id)}
                      disabled={deleteMutation.isPending}
                    >
                      Delete
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </div>
  );
}
