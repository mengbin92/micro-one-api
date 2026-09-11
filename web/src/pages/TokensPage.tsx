import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { useNavigate } from 'react-router';
import { Copy, FlaskConical, Zap } from 'lucide-react';
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
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog';
import { apiClient } from '@/lib/api';
import { EmptyState } from '@/components/EmptyState';
import { TableSkeleton } from '@/components/LoadingStates';
import { ensureApiSuccess, unwrapApiData } from '@/lib/api-response';
import { CCSwitchDialog } from '@/components/CCSwitchDialog';
import { setPlaygroundCredential } from '@/lib/playground-credential';
import { loadAvailableGroups } from '@/lib/routing-groups';
import { locale, t } from '@/lib/i18n';

interface Token {
  routing_mode?: string;
  routing_group_id?: number;
  routing_revision?: number;
  id: number;
  name?: string;
  key?: string;
  masked_key?: string;
  status: number;
  created_time: number;
}

interface TokenListData {
  items?: Token[];
  total?: number;
}

interface PricingRow {
  model: string;
}

interface PricingPayload {
  prices?: PricingRow[];
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

function maskApiKey(key?: string): string | undefined {
  if (!key) {
    return undefined;
  }
  if (key.length <= 8) {
    return '*'.repeat(key.length);
  }
  return `${key.slice(0, 4)}${'*'.repeat(key.length - 8)}${key.slice(-4)}`;
}

function tokenForList(token: Token): Token {
  const { key, ...safeToken } = token;
  return {
    ...safeToken,
    masked_key: token.masked_key || maskApiKey(key) || safeToken.masked_key,
  };
}

export function TokensPage() {
  const [isCreateOpen, setIsCreateOpen] = useState(false);
  const [newTokenName, setNewTokenName] = useState('');
  const [selectedGroup, setSelectedGroup] = useState('inherit');
  const [editingToken, setEditingToken] = useState<Token | null>(null);
  const available = useQuery({ queryKey: ['available-routing-groups'], queryFn: loadAvailableGroups, retry: false });
  const selected = available.data?.groups.find((g) => String(g.id) === selectedGroup);
  const [createdToken, setCreatedToken] = useState<Token | null>(null);
  const [ccSwitchOpen, setCCSwitchOpen] = useState(false);
  const [ccSwitchKey, setCCSwitchKey] = useState('');
  // Bumped on each open so CCSwitchDialog remounts and re-reads tokenKey
  // into initial state — Base UI's Dialog does not echo the parent's
  // controlled `open=true` through onOpenChange.
  const [ccSwitchSessionId, setCCSwitchSessionId] = useState(0);
  const queryClient = useQueryClient();
  const navigate = useNavigate();

  const { data: tokens, isLoading } = useQuery({
    queryKey: ['tokens'],
    queryFn: async () => {
      const res = await apiClient.get('/token');
      return normalizeTokens(unwrapApiData<Token[] | TokenListData>(res.data));
    },
  });

  const { data: pricing } = useQuery({
    queryKey: ['readonly-pricing'],
    queryFn: async () => {
      const res = await apiClient.get('/pricing');
      return unwrapApiData<PricingPayload>(res.data);
    },
  });

  const modelOptions = (pricing?.prices ?? [])
    .map((row) => String(row.model || '').trim())
    .filter(Boolean)
    .sort((a, b) => a.localeCompare(b));

  const [isCreating, setIsCreating] = useState(false);

  const deleteMutation = useMutation({
    mutationFn: async (id: number) => {
      const res = await apiClient.delete(`/token/${id}`);
      ensureApiSuccess(res.data, t('删除 Token 失败'));
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['tokens'] });
      toast.success(t('Token 已删除'));
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t('删除 Token 失败'));
    },
  });

  const handleCreate = async () => {
    const name = newTokenName.trim();
    if (!name) {
      toast.error(t('Token 名称为必填项'));
      return;
    }

    setIsCreating(true);
    try {
      const res = available.data
        ? await apiClient.post('/v1/routing-tokens', { name, routing_mode: selectedGroup === 'inherit' ? 'inherit' : 'fixed', routing_group_id: selectedGroup === 'inherit' ? 0 : Number(selectedGroup) })
        : await apiClient.post('/token', { name });
      const token = unwrapApiData<Token>(res.data);
      setCreatedToken(token);
      queryClient.setQueryData<Token[]>(['tokens'], (current = []) => {
        const safeToken = tokenForList(token);
        const withoutCreated = current.filter((item) => item.id !== token.id);
        return [safeToken, ...withoutCreated];
      });
      setNewTokenName('');
      toast.success(t('Token 已创建'));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('创建 Token 失败'));
    } finally {
      setIsCreating(false);
    }
  };

  const handleCreateOpenChange = (open: boolean) => {
    setIsCreateOpen(open);
    if (!open) {
      setCreatedToken(null);
      setNewTokenName('');
      setSelectedGroup('inherit');
    }
  };

  const saveRouting = async () => {
    if (!editingToken) return;
    setIsCreating(true);
    try {
      const res = await apiClient.patch(`/v1/routing-tokens/${editingToken.id}`, { routing_mode: selectedGroup === 'inherit' ? 'inherit' : 'fixed', routing_group_id: selectedGroup === 'inherit' ? 0 : Number(selectedGroup), expected_revision: editingToken.routing_revision });
      ensureApiSuccess(res.data, '保存分组失败');
      setEditingToken(null);
      await queryClient.invalidateQueries({ queryKey: ['tokens'] });
      toast.success('Key 分组已更新');
    } catch (error) { toast.error(error instanceof Error ? error.message : '保存分组失败'); }
    finally { setIsCreating(false); }
  };
  const routingSelector = <div className="space-y-2">
    <Label htmlFor="token-routing-group">路由分组</Label>
    <select id="token-routing-group" className="h-10 w-full rounded-md border bg-background px-3" value={selectedGroup} onChange={(e) => setSelectedGroup(e.target.value)}>
      <option value="inherit">跟随默认分组{available.data && !available.data.default_available ? '（需要重新选择分组）' : ''}</option>
      {selectedGroup !== 'inherit' && !selected && <option value={selectedGroup}>固定分组 #{selectedGroup}（当前不可用）</option>}
      {available.data?.groups.filter(() => available.data.creation_enabled).map((g) => <option key={g.id} value={g.id}>{g.display_name || g.key} · ×{g.price_ratio}</option>)}
    </select>
    {selected && <p className="text-sm text-muted-foreground">订阅优先，余额由钱包支付 · {selected.subscription_covered ? '当前订阅覆盖' : '当前无订阅覆盖'} · 价格来源：{selected.price_source}</p>}
  </div>;

  const copyKey = async (key: string) => {
    try {
      await navigator.clipboard.writeText(key);
      toast.success(t('Token 已复制'));
    } catch {
      toast.error(t('无法复制 Token'));
    }
  };

  const openCCSwitch = (key: string) => {
    setCCSwitchSessionId((id) => id + 1);
    setCCSwitchKey(key);
    setCCSwitchOpen(true);
  };

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h2 className="text-2xl font-semibold">{t('API 密钥')}</h2>
        <Dialog open={isCreateOpen} onOpenChange={handleCreateOpenChange}>
          <DialogTrigger render={<Button />}>
            {t('创建 Token')}
          </DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>{createdToken?.key ? t('Token 已创建') : t('创建新 Token')}</DialogTitle>
              <DialogDescription>
                {createdToken?.key ? t('请立即复制此 API 密钥，之后将不再完整显示。') : t('请为新的 API Token 输入名称。')}
              </DialogDescription>
            </DialogHeader>
            <div className="space-y-4 pt-4">
              <div className="space-y-2">
                <Label htmlFor="token-name">{t('Token 名称')}</Label>
                {createdToken?.key ? (
                  <Input id="token-name" readOnly value={createdToken.name || newTokenName} />
                ) : (
                  <Input
                    id="token-name"
                    value={newTokenName}
                    onChange={(e) => setNewTokenName(e.target.value)}
                    placeholder={t('我的 Token')}
                  />
                )}
              </div>
              {!createdToken?.key && available.data && routingSelector}
              {createdToken?.key && (
                <div className="space-y-2">
                  <Label htmlFor="created-token-key">{t('API 密钥')}</Label>
                  <div className="flex gap-2">
                    <Input id="created-token-key" readOnly value={createdToken.key} className="font-mono text-xs" />
                    <Button type="button" variant="outline" size="icon" onClick={() => copyKey(createdToken.key as string)} aria-label={t('复制 Token')}>
                      <Copy />
                    </Button>
                  </div>
                </div>
              )}
              {createdToken?.key ? (
                <div className="space-y-3">
                  <Button
                    variant="outline"
                    className="w-full gap-2 border-blue-200 bg-blue-50 text-blue-700 hover:bg-blue-100 dark:border-blue-500/30 dark:bg-blue-500/10 dark:text-blue-300"
                    onClick={() => {
                      setPlaygroundCredential(createdToken.key as string);
                      handleCreateOpenChange(false);
                      navigate('/playground');
                    }}
                  >
                    <FlaskConical className="size-4" />{t("在在线调试中使用")}</Button>
                  <Button
                    variant="outline"
                    className="w-full gap-2 border-orange-200 bg-orange-50 text-orange-700 hover:bg-orange-100 dark:border-orange-500/30 dark:bg-orange-500/10 dark:text-orange-300"
                    onClick={() => openCCSwitch(createdToken.key as string)}
                  >
                    <Zap className="size-4" />{t("导入到 CC Switch")}</Button>
                  <DialogFooter>
                    <DialogClose render={<Button className="w-full" />}>{t('完成')}</DialogClose>
                  </DialogFooter>
                </div>
              ) : (
                <Button
                  onClick={handleCreate}
                  disabled={isCreating || !newTokenName.trim() || !!(available.data && selectedGroup === 'inherit' && !available.data.default_available)}
                  className="w-full"
                >
                  {isCreating ? t('创建中...') : t('创建')}
                </Button>
              )}
            </div>
          </DialogContent>
        </Dialog>
      </div>

      <Dialog open={!!editingToken} onOpenChange={(open) => { if (!open) setEditingToken(null); }}>
        <DialogContent><DialogHeader><DialogTitle>修改 Key 路由分组</DialogTitle><DialogDescription>保存后新请求使用新分组，已有会话可能需要重新建立。</DialogDescription></DialogHeader>
          {routingSelector}<Button disabled={isCreating || (selectedGroup === 'inherit' ? !available.data?.default_available : !selected)} onClick={saveRouting}>保存</Button>
        </DialogContent>
      </Dialog>
      {available.data && <section className="mb-6 rounded-lg border p-4 space-y-3">
        <h3 className="font-semibold">可用分组</h3>
        {!available.data.default_available && <p role="alert" className="text-amber-700">默认分组失效：跟随默认分组的 Key 需要重新选择分组。</p>}
        {available.data.groups.length === 0 && <p>当前没有可用分组</p>}
        {available.data.groups.map((g) => <div key={g.id} className="border-t pt-2 text-sm">
          <div className="flex items-center justify-between"><strong>{g.display_name || g.key} · ×{g.price_ratio}</strong>
            <Button variant="outline" size="sm" disabled={g.id === available.data.facts.default_routing_group_id} onClick={async () => {
              try { const res = await apiClient.patch('/v1/routing-access', { operation: 'default', routing_group_id: g.id, expected_revision: available.data.facts.revision }); ensureApiSuccess(res.data, '设置失败'); await available.refetch(); toast.success('默认分组已更新'); }
              catch (error) { toast.error(error instanceof Error ? error.message : '设置失败'); }
            }}>{g.id === available.data.facts.default_routing_group_id ? '默认分组' : '设为默认'}</Button></div>
          <p className="text-muted-foreground">{g.sources.map((source) => `${source.source_type}${source.expires_at ? `（有效至 ${new Date(source.expires_at * 1000).toLocaleString(locale())}）` : ''}`).join('、')} · {g.subscription_covered ? '订阅覆盖，共享额度' : '钱包支付'} · {g.price_source}</p>
          <details className="text-muted-foreground"><summary>{t('价格版本')}</summary><code className="break-all">{g.price_version}</code></details>
          <p className="break-words text-muted-foreground">模型：{g.models?.join('、') || '暂无已授权模型'}</p>
        </div>)}
      </section>}
      {available.isError && <p className="mb-4 text-sm text-muted-foreground">{t('可用分组暂不可用，无法修改分组设置。')}</p>}
      {isLoading ? (
        <TableSkeleton columns={[t('名称'), t('密钥'), t('状态'), t('创建时间'), t('操作')]} />
      ) : !tokens || tokens.length === 0 ? (
        <EmptyState title={t('暂无 API 密钥')} description={t('创建一个 Token 后即可开始调用 API。')} />
      ) : (
        <div className="overflow-x-auto rounded-lg border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('名称')}</TableHead>
                <TableHead>{t('密钥')}</TableHead>
                <TableHead>{t('路由分组')}</TableHead>
                <TableHead>{t('状态')}</TableHead>
                <TableHead>{t('创建时间')}</TableHead>
                <TableHead className="text-right">{t('操作')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {tokens.map((token) => (
                <TableRow key={token.id}>
                  <TableCell className="font-medium">{token.name}</TableCell>
                  <TableCell className="font-mono text-sm">{token.masked_key || t('已隐藏')}</TableCell>
                  <TableCell>{token.routing_mode === 'fixed' ? (available.data?.groups.find((g) => g.id === token.routing_group_id)?.display_name || `固定分组 #${token.routing_group_id}（当前不可用）`) : t('跟随默认分组')}</TableCell>
                  <TableCell>
                    <span
                      className={`inline-flex items-center px-2 py-1 rounded-full text-xs font-medium ${
                        token.status === 1
                          ? 'bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200'
                          : 'bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-200'
                      }`}
                    >
                      {token.status === 1 ? t('启用') : t('禁用')}
                    </span>
                  </TableCell>
                  <TableCell>
                    {token.created_time ? new Date(token.created_time * 1000).toLocaleDateString(locale()) : '—'}
                  </TableCell>
                  <TableCell className="text-right">
                    <div className="flex items-center justify-end gap-2">
                      {available.data && token.routing_revision && <Button variant="outline" size="sm" onClick={() => { setSelectedGroup(token.routing_mode === 'fixed' ? String(token.routing_group_id) : 'inherit'); setEditingToken(token); }}>分组设置</Button>}
                      <Button
                        variant="destructive"
                        size="sm"
                        onClick={() => deleteMutation.mutate(token.id)}
                        disabled={deleteMutation.isPending}
                      >
                        {t('删除')}
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      <CCSwitchDialog
        key={ccSwitchSessionId}
        open={ccSwitchOpen}
        onOpenChange={setCCSwitchOpen}
        tokenKey={ccSwitchKey}
        modelOptions={modelOptions}
      />
    </div>
  );
}
