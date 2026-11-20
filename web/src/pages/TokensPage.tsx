import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { useNavigate } from 'react-router';
import { Copy, FlaskConical, Zap } from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Skeleton } from '@/components/ui/skeleton';
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
import { ensureApiSuccess, unwrapApiData } from '@/lib/api-response';
import { CCSwitchDialog } from '@/components/CCSwitchDialog';
import { setPlaygroundCredential } from '@/lib/playground-credential';
import { loadAvailableGroups } from '@/lib/routing-groups';
import { locale, t } from '@/lib/i18n';

interface Token {
  routing_mode?: string;
  routing_group_id?: number;
  routing_group_ids?: number[];
  routing_revision?: number;
  id: number;
  name?: string;
  key?: string;
  masked_key?: string;
  status: number;
  created_time: number;
  expired_time?: number;
  remain_quota?: number;
  used_quota?: number;
  unlimited_quota?: boolean;
  models?: string[];
}

interface ShortTermKeyForm {
  expires: string;
  quota: string;
  models: string[];
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

function effectivePrice(group: { price_ratio: number; price_source: string }) {
  return `×${group.price_ratio}${group.price_source === 'user_routing_price_override' ? t('（用户专属）') : ''}`;
}
function billingModeText(mode: string) {
  return { wallet_only: t('仅钱包'), subscription_only: t('仅订阅'), subscription_first: t('订阅优先，余额补足') }[mode] ?? mode;
}
function sourceText(source: { source_type: string; source_ref?: string; expires_at?: number }) {
  const label = { admin: t('管理员授权'), migration: t('迁移继承'), subscription: t('订阅权益'), public: t('公开分组') }[source.source_type] ?? source.source_type;
  return `${label}${source.source_ref ? ` #${source.source_ref}` : ''}${source.expires_at ? `（${t('有效至')} ${new Date(source.expires_at * 1000).toLocaleString(locale())}）` : ''}`;
}
function tokenForList(token: Token): Token {
  const { key, ...safeToken } = token;
  return {
    ...safeToken,
    masked_key: token.masked_key || maskApiKey(key) || safeToken.masked_key,
  };
}

function tokenStatus(token: Token, observedAt: number) {
  if (token.status === 3) return t('已过期');
  if (token.status === 4) return t('额度已耗尽');
  if (token.status !== 1) return t('禁用');
  if (typeof token.expired_time === 'number' && token.expired_time > 0 && token.expired_time < observedAt) return t('已过期');
  if (token.unlimited_quota === false && typeof token.remain_quota === 'number' && token.remain_quota <= 0) return t('额度已耗尽');
  return t('启用');
}

function TokenLimits({ token }: { token: Token }) {
  const expiration = token.expired_time;
  const date = typeof expiration === 'number' && expiration > 0 ? new Date(expiration * 1000) : null;
  const validDate = date && Number.isFinite(date.getTime());
  const remaining = token.unlimited_quota === true ? t('不限额') : token.remain_quota?.toLocaleString(locale()) ?? '—';

  return (
    <dl className="grid min-w-0 grid-cols-2 gap-x-4 gap-y-3 text-sm sm:grid-cols-3">
      <div className="col-span-2 sm:col-span-1">
        <dt className="text-xs text-muted-foreground">{t('到期时间（本地时间）')}</dt>
        <dd className="mt-1 font-medium">
          {validDate ? <time dateTime={date.toISOString()}>{date.toLocaleString(locale(), { dateStyle: 'medium', timeStyle: 'short' })}</time>
            : typeof expiration === 'number' && expiration <= 0 ? t('无期限') : '—'}
        </dd>
      </div>
      <div>
        <dt className="text-xs text-muted-foreground">{t('剩余额度（Token）')}</dt>
        <dd className="mt-1 font-medium tabular-nums">{remaining}</dd>
      </div>
      {token.unlimited_quota === false && (
        <div>
          <dt className="text-xs text-muted-foreground">{t('已用额度（Token）')}</dt>
          <dd className="mt-1 font-medium tabular-nums">{token.used_quota?.toLocaleString(locale()) ?? '—'}</dd>
        </div>
      )}
      <div className="col-span-full">
        <dt className="text-xs text-muted-foreground">{t('允许调用的模型')}</dt>
        <dd className="mt-1 flex flex-wrap gap-1">
          {token.models?.length ? token.models.map((model) => (
            <span key={model} className="max-w-full break-all rounded-md bg-muted px-2 py-0.5 font-mono text-xs">{model}</span>
          )) : <span className="text-muted-foreground">{t('未设置白名单')}</span>}
        </dd>
      </div>
    </dl>
  );
}

export function TokensPage() {
  const [deleteTarget, setDeleteTarget] = useState<Token | null>(null);
  const [isCreateOpen, setIsCreateOpen] = useState(false);
  const [newTokenName, setNewTokenName] = useState('');
  const [selectedGroup, setSelectedGroup] = useState('inherit');
  const [orderedGroupIDs, setOrderedGroupIDs] = useState<number[]>([]);
  const [editingToken, setEditingToken] = useState<Token | null>(null);
  const [shortTerm, setShortTerm] = useState<ShortTermKeyForm | null>(null);
  const [createOpenedAt, setCreateOpenedAt] = useState(0);
  const available = useQuery({ queryKey: ['available-routing-groups'], queryFn: loadAvailableGroups, retry: false });
  const selected = available.data?.groups.find((g) => String(g.id) === selectedGroup);
  const fixedGroupSelected = selectedGroup !== 'inherit' && selectedGroup !== 'ordered';
  const selectedModels = selected?.models ?? [];
  const restrictions = shortTerm && {
    expire_at: Math.floor(new Date(shortTerm.expires).getTime() / 1000),
    remain_quota: Number(shortTerm.quota),
    models: shortTerm.models,
  };
  const validRestrictions = restrictions && selected && available.data?.creation_enabled
    && restrictions.expire_at * 1000 > createOpenedAt
    && Number.isSafeInteger(restrictions.remain_quota) && restrictions.remain_quota > 0
    && restrictions.models.length > 0 && restrictions.models.every((model) => selectedModels.includes(model));
  const orderedSelection = orderedGroupIDs.map((id) => ({ id, group: available.data?.groups.find((g) => g.id === id) }));
  const orderedPrices = orderedSelection.flatMap(({ group }) => group ? [group.price_ratio] : []);
  const [createdToken, setCreatedToken] = useState<Token | null>(null);
  const [ccSwitchOpen, setCCSwitchOpen] = useState(false);
  const [ccSwitchKey, setCCSwitchKey] = useState('');
  // Bumped on each open so CCSwitchDialog remounts and re-reads tokenKey
  // into initial state — Base UI's Dialog does not echo the parent's
  // controlled `open=true` through onOpenChange.
  const [ccSwitchSessionId, setCCSwitchSessionId] = useState(0);
  const queryClient = useQueryClient();
  const navigate = useNavigate();

  const { data: tokens, dataUpdatedAt, isLoading } = useQuery({
    queryKey: ['tokens'],
    queryFn: async () => {
      const res = await apiClient.get('/token');
      return normalizeTokens(unwrapApiData<Token[] | TokenListData>(res.data));
    },
  });

  const { data: pricing } = useQuery({
    queryKey: ['readonly-pricing'],
    queryFn: async ({ signal }) => {
      const res = await apiClient.get('/pricing', { signal });
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
    if (restrictions && !validRestrictions) {
      toast.error(t('请设置未来的到期时间、正整数配额和至少一个模型。'));
      return;
    }

    setIsCreating(true);
    try {
      const routingPayload = routingModePayload();
      const res = restrictions
        ? await apiClient.post('/v1/routing-tokens/restricted', { name, ...routingPayload, restrictions })
        : await apiClient.post(available.data ? '/v1/routing-tokens' : '/token', available.data ? { name, ...routingPayload } : { name });
      const token = unwrapApiData<Token>(res.data);
      if (restrictions && (token.routing_mode !== 'fixed' || token.routing_group_id !== routingPayload.routing_group_id
        || token.unlimited_quota !== false || token.remain_quota !== restrictions.remain_quota
        || token.expired_time !== restrictions.expire_at || token.models?.length !== restrictions.models.length
        || !restrictions.models.every((model) => token.models?.includes(model)))) {
        await queryClient.invalidateQueries({ queryKey: ['tokens'] });
        throw new Error(t('服务端返回的密钥限制与申请不符。'));
      }
      setCreatedToken(token);
      queryClient.setQueryData<Token[]>(['tokens'], (current = []) => {
        const safeToken = tokenForList(token);
        const withoutCreated = current.filter((item) => item.id !== token.id);
        return [safeToken, ...withoutCreated];
      });
      setNewTokenName('');
      await queryClient.invalidateQueries({ queryKey: ['available-routing-groups'] });
      toast.success(t('Token 已创建'));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('创建 Token 失败'));
    } finally {
      setIsCreating(false);
    }
  };

  const handleCreateOpenChange = (open: boolean) => {
    setIsCreateOpen(open);
    if (open) {
      setCreateOpenedAt(Date.now());
    } else {
      setCreatedToken(null);
      setNewTokenName('');
      setSelectedGroup('inherit');
      setOrderedGroupIDs([]);
      setShortTerm(null);
    }
  };

  const routingModePayload = () => {
    if (selectedGroup === 'ordered') return { routing_mode: 'ordered', routing_group_id: 0, routing_group_ids: orderedGroupIDs };
    if (selectedGroup === 'inherit') return { routing_mode: 'inherit', routing_group_id: 0 };
    return { routing_mode: 'fixed', routing_group_id: Number(selectedGroup) };
  };

  const saveRouting = async () => {
    if (!editingToken) return;
    setIsCreating(true);
    try {
      const res = await apiClient.patch(`/v1/routing-tokens/${editingToken.id}`, { ...routingModePayload(), expected_revision: editingToken.routing_revision });
      ensureApiSuccess(res.data, '保存分组失败');
      setEditingToken(null);
      await queryClient.invalidateQueries({ queryKey: ['tokens'] });
      await queryClient.invalidateQueries({ queryKey: ['available-routing-groups'] });
      toast.success('Key 分组已更新');
    } catch (error) { toast.error(error instanceof Error ? error.message : '保存分组失败'); }
    finally { setIsCreating(false); }
  };
  const routingSelector = <div className="space-y-2">
    <Label htmlFor="token-routing-group">路由分组</Label>
    <select id="token-routing-group" className="h-10 w-full rounded-md border bg-background px-3" value={selectedGroup} onChange={(e) => {
      setSelectedGroup(e.target.value);
      if (shortTerm) setShortTerm({ ...shortTerm, models: [] });
    }}>
      <option value="inherit" disabled={shortTerm !== null}>跟随默认分组{available.data && !available.data.default_available ? '（需要重新选择分组）' : ''}</option>
      {selectedGroup !== 'inherit' && selectedGroup !== 'ordered' && !selected && <option value={selectedGroup}>固定分组 #{selectedGroup}（当前不可用）</option>}
      {available.data?.groups.filter(() => available.data.creation_enabled).map((g) => <option key={g.id} value={g.id}>{g.display_name || g.key} · {effectivePrice(g)}</option>)}
      {available.data?.groups.some((g) => g.ordered_eligible) && <option value="ordered" disabled={shortTerm !== null}>有序候选组（自动跨组重试）</option>}
    </select>
    {selectedGroup === 'ordered' && <div className="space-y-2 rounded-md border p-3">
      <p className="text-sm text-muted-foreground">按顺序尝试候选组：仅在前置组无可用资源时切换到下一组，各组价格可能不同。</p>
      {orderedSelection.map(({ id, group: g }, index) => <div key={id} className="flex items-center justify-between gap-2 text-sm">
        <span>{index + 1}. {g ? `${g.display_name || g.key} · ${effectivePrice(g)}` : `分组 #${id}（当前不可用）`}</span>
        <span className="flex gap-1">
          <Button type="button" variant="outline" size="sm" disabled={index === 0} onClick={() => setOrderedGroupIDs((ids) => { const next = [...ids]; [next[index - 1], next[index]] = [next[index], next[index - 1]]; return next; })}>↑</Button>
          <Button type="button" variant="outline" size="sm" disabled={index === orderedSelection.length - 1} onClick={() => setOrderedGroupIDs((ids) => { const next = [...ids]; [next[index], next[index + 1]] = [next[index + 1], next[index]]; return next; })}>↓</Button>
          <Button type="button" variant="outline" size="sm" onClick={() => setOrderedGroupIDs((ids) => ids.filter((candidate) => candidate !== id))}>移除</Button>
        </span>
      </div>)}
      <select className="h-10 w-full rounded-md border bg-background px-3" value="" onChange={(e) => { const id = Number(e.target.value); if (id > 0 && !orderedGroupIDs.includes(id)) setOrderedGroupIDs((ids) => [...ids, id]); }}>
        <option value="">添加候选组…</option>
        {available.data?.groups.filter((g) => !orderedGroupIDs.includes(g.id)).map((g) => <option key={g.id} value={g.id}>{g.display_name || g.key} · {effectivePrice(g)}{g.ordered_eligible ? '' : '（当前无资格，可预列）'}</option>)}
      </select>
      {orderedSelection.length > 0 && orderedPrices.length > 1 && new Set(orderedPrices).size > 1 && <p className="text-sm text-amber-700">候选组价格不同，最终按实际命中的组计费。</p>}
    </div>}
    {selected && <p className="text-sm text-muted-foreground">{billingModeText(selected.billing_mode)} · {selected.subscription_covered ? t('当前订阅覆盖') : t('当前无订阅覆盖')} · {t('有效价格')}：{effectivePrice(selected)} · {t('价格来源')}：{selected.price_source}</p>}
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
              {!createdToken?.key && fixedGroupSelected && (
                <label className="flex items-center gap-2 font-medium">
                  <input type="checkbox" checked={shortTerm !== null} disabled={!shortTerm && !selected}
                    onChange={(e) => setShortTerm(e.target.checked ? { expires: '', quota: '', models: [] } : null)} />
                  {t('短期 Key')}
                </label>
              )}
              {!createdToken?.key && fixedGroupSelected && shortTerm && (
                <div className="space-y-3">
                  <div className="space-y-2">
                    <Label htmlFor="token-expiry">{t('到期时间（本地时间）')}</Label>
                    <Input id="token-expiry" type="datetime-local" required value={shortTerm.expires}
                      onChange={(e) => setShortTerm({ ...shortTerm, expires: e.target.value })} />
                  </div>
                  <div className="space-y-2">
                    <Label htmlFor="token-quota">{t('配额（Token）')}</Label>
                    <Input id="token-quota" type="number" min="1" step="1" required value={shortTerm.quota}
                      onChange={(e) => setShortTerm({ ...shortTerm, quota: e.target.value })} />
                  </div>
                  <fieldset>
                    <legend className="mb-2 font-medium">{t('允许调用的模型')}</legend>
                    <div className="max-h-36 space-y-2 overflow-y-auto rounded-lg border p-2">
                      {selectedModels.map((model) => (
                        <label key={model} className="flex items-start gap-2 break-all">
                          <input type="checkbox" checked={shortTerm.models.includes(model)} onChange={(e) => {
                            const models = e.target.checked ? [...shortTerm.models, model] : shortTerm.models.filter((item) => item !== model);
                            setShortTerm({ ...shortTerm, models });
                          }} />
                          {model}
                        </label>
                      ))}
                      {selectedModels.length === 0 && <p className="text-muted-foreground">{t('暂无可用模型')}</p>}
                    </div>
                  </fieldset>
                </div>
              )}
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
                  {createdToken.unlimited_quota === false && !!createdToken.expired_time && (
                    <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 break-all text-sm">
                      <dt>{t('到期时间')}</dt><dd>{new Date(createdToken.expired_time * 1000).toLocaleString(locale())}</dd>
                      <dt>{t('配额（Token）')}</dt><dd>{createdToken.remain_quota}</dd>
                      <dt>{t('允许调用的模型')}</dt><dd>{createdToken.models?.join(', ')}</dd>
                    </dl>
                  )}
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
                  disabled={isCreating || !newTokenName.trim() || (shortTerm !== null && !validRestrictions)
                    || !!(available.data && selectedGroup === 'inherit' && !available.data.default_available) || (selectedGroup === 'ordered' && orderedGroupIDs.length === 0)}
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
          {routingSelector}<Button disabled={isCreating || (selectedGroup === 'inherit' ? !available.data?.default_available : selectedGroup === 'ordered' ? orderedGroupIDs.length === 0 : !selected)} onClick={saveRouting}>保存</Button>
        </DialogContent>
      </Dialog>
      {available.data && <section className="mb-6 rounded-lg border p-4 space-y-3">
        <h3 className="font-semibold">可用分组</h3>
        {!available.data.default_available && <p role="alert" className="text-amber-700">默认分组失效：跟随默认分组的 Key 需要重新选择分组。</p>}
        {available.data.groups.length === 0 && <p>当前没有可用分组</p>}
        {available.data.groups.map((g) => <div key={g.id} className="border-t pt-2 text-sm">
          <div className="flex items-center justify-between"><strong>{g.display_name || g.key} · {effectivePrice(g)}</strong>
            <Button variant="outline" size="sm" disabled={g.id === available.data.facts.default_routing_group_id} onClick={async () => {
              try { const res = await apiClient.patch('/v1/routing-access', { operation: 'default', routing_group_id: g.id, expected_revision: available.data.facts.revision }); ensureApiSuccess(res.data, '设置失败'); await available.refetch(); toast.success('默认分组已更新'); }
              catch (error) { toast.error(error instanceof Error ? error.message : '设置失败'); }
            }}>{g.id === available.data.facts.default_routing_group_id ? '默认分组' : '设为默认'}</Button></div>
          <p className="text-muted-foreground">{g.sources.map(sourceText).join('、')} · {g.subscription_covered ? '订阅覆盖，共享额度' : '钱包支付'} · {g.price_source}</p>
          <details className="text-muted-foreground"><summary>{t('价格版本')}</summary><code className="break-all">{g.price_version}</code></details>
          <p className="break-words text-muted-foreground">模型：{g.models?.join('、') || '暂无已授权模型'}</p>
        </div>)}
      </section>}
      {available.isError && <p className="mb-4 text-sm text-muted-foreground">{t('可用分组暂不可用，无法修改分组设置。')}</p>}
      {isLoading ? (
        <div aria-busy="true" className="space-y-3">
          <Skeleton className="h-40 w-full rounded-lg" />
        </div>
      ) : !tokens || tokens.length === 0 ? (
        <EmptyState title={t('暂无 API 密钥')} description={t('创建一个 Token 后即可开始调用 API。')} />
      ) : (
        <ul aria-label={t('API 密钥')} className="divide-y rounded-lg border">
          {tokens.map((token) => {
            const group = available.data?.groups.find((g) => g.id === token.routing_group_id);
            const facts = available.data?.facts.tokens?.find((item) => item.id === token.id);
            const groupIds = token.routing_group_ids || facts?.group_ids || [];
            const groupName = token.routing_mode === 'ordered' ? t('有序候选组（{count} 个）', { count: groupIds.length })
              : token.routing_mode === 'fixed' ? (group?.display_name || group?.key || t('固定分组 #{id}（当前不可用）', { id: token.routing_group_id ?? '—' }))
              : t('跟随默认分组');
            const status = tokenStatus(token, Math.floor(dataUpdatedAt / 1000));
            return (
              <li key={token.id} aria-labelledby={`token-name-${token.id}`}
                className="grid min-w-0 gap-4 p-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.9fr)_auto] lg:items-start">
                <div className="min-w-0 space-y-1.5">
                  <div className="flex flex-wrap items-center gap-2">
                    <h3 id={`token-name-${token.id}`} className="min-w-0 break-all font-semibold">{token.name}</h3>
                    <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${status === t('启用')
                      ? 'bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200'
                      : 'bg-muted text-muted-foreground'}`}>{status}</span>
                  </div>
                  <p className="break-all font-mono text-sm text-muted-foreground">{token.masked_key || t('已隐藏')}</p>
                  <dl className="space-y-1 text-xs">
                    <div className="flex flex-wrap gap-x-2">
                      <dt className="text-muted-foreground">{t('路由分组')}</dt><dd className="break-all">{groupName}</dd>
                    </div>
                    {token.created_time > 0 && <div className="flex flex-wrap gap-x-2">
                      <dt className="text-muted-foreground">{t('创建时间')}</dt>
                      <dd>{new Date(token.created_time * 1000).toLocaleDateString(locale())}</dd>
                    </div>}
                  </dl>
                </div>
                <TokenLimits token={token} />
                <div className="flex flex-wrap gap-2 lg:justify-end">
                  {!!(available.data && token.routing_revision) && (
                    <Button variant="outline" size="sm" onClick={() => {
                      setSelectedGroup(token.routing_mode === 'ordered' ? 'ordered' : token.routing_mode === 'fixed' ? String(token.routing_group_id) : 'inherit');
                      setOrderedGroupIDs(groupIds);
                      setEditingToken(token);
                    }}>{t('分组设置')}</Button>
                  )}
                  <Button variant="destructive" size="sm" onClick={() => setDeleteTarget(token)} disabled={deleteMutation.isPending}>
                    {t('删除')}
                  </Button>
                </div>
              </li>
            );
          })}
        </ul>
      )}

      <Dialog open={deleteTarget !== null} onOpenChange={(open) => { if (!open) setDeleteTarget(null); }}>
        <DialogContent>
          <DialogHeader><DialogTitle>{t('删除 API 密钥')}</DialogTitle><DialogDescription>{t('删除后，使用此密钥的请求将无法认证。此操作无法撤销。')}</DialogDescription></DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleteTarget(null)}>{t('取消')}</Button>
            <Button variant="destructive" disabled={deleteMutation.isPending} onClick={() => {
              if (deleteTarget) deleteMutation.mutate(deleteTarget.id, { onSuccess: () => setDeleteTarget(null) });
            }}>{t('确认删除')}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

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
