import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { CalendarClock, Search, UserPlus } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import { adminApiClient } from '@/lib/api';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { EmptyState } from '@/components/EmptyState';
import { TableSkeleton } from '@/components/LoadingStates';
import {
  SubscriptionProgressCard,
  type SubscriptionProgressData,
} from '@/components/SubscriptionProgress';
import { ensureApiSuccess, unwrapApiData } from '@/lib/api-response';
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
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog';
import { locale, t } from '@/lib/i18n';

// Mirrors subscriptionDTO JSON tags (internal/admin/server/subscription.go).
interface UserSubscription {
  id: number;
  user_id: number;
  group_id: number;
  subscription_name: string;
  status: string;
  starts_at: number;
  expires_at: number;
  daily_usage_usd: number;
  weekly_usage_usd: number;
  monthly_usage_usd: number;
  metadata: string;
  created_at: number;
  updated_at: number;
}

interface SubscriptionGroupOption {
  id: number;
  name: string;
  display_name: string;
}

function formatTimestamp(unix: number) {
  if (!unix) return '—';
  return new Date(unix * 1000).toLocaleString(locale());
}

function statusBadgeClass(status: string) {
  switch (status) {
    case 'active':
      return 'bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200';
    case 'revoked':
      return 'bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-200';
    default:
      return 'bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-300';
  }
}

// Convert a datetime-local input value to a Unix seconds timestamp.
function toUnixSeconds(datetimeLocal: string): number {
  if (!datetimeLocal) return 0;
  const ms = new Date(datetimeLocal).getTime();
  return Number.isFinite(ms) ? Math.floor(ms / 1000) : 0;
}

function toDatetimeLocal(unix: number): string {
  if (!unix) return '';
  const date = new Date(unix * 1000);
  const pad = (value: number) => String(value).padStart(2, '0');
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

export function AdminSubscriptionsPage() {
  const [userIdInput, setUserIdInput] = useState('');
  // null => list every subscription; a positive id => filter to that user.
  const [filterUserId, setFilterUserId] = useState<number | null>(null);
  const [isAssignOpen, setIsAssignOpen] = useState(false);
  const [extendSubscription, setExtendSubscription] = useState<UserSubscription | null>(null);
  const queryClient = useQueryClient();

  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: ['admin-subscriptions'] });
    queryClient.invalidateQueries({ queryKey: ['admin-subscription-progress'] });
  };

  const { data: subscriptions, isLoading } = useQuery({
    queryKey: ['admin-subscriptions', filterUserId],
    queryFn: async () => {
      const query = filterUserId != null ? `?user_id=${filterUserId}` : '';
      const res = await adminApiClient.get(`/v1/admin/subscriptions${query}`);
      return unwrapApiData<UserSubscription[]>(res.data) ?? [];
    },
  });

  // Progress is per-user and best-effort: only fetched when filtering to a single
  // user; success:false (no active subscription) is surfaced as "no progress".
  const { data: progress } = useQuery({
    queryKey: ['admin-subscription-progress', filterUserId],
    enabled: filterUserId != null && filterUserId > 0,
    queryFn: async () => {
      const res = await adminApiClient.get(`/v1/subscriptions/progress?user_id=${filterUserId}`);
      if (res.data?.success === false) return null;
      return (res.data?.data as SubscriptionProgressData | null) ?? null;
    },
  });

  const { data: groups } = useQuery({
    queryKey: ['admin-subscription-groups-options'],
    queryFn: async () => {
      const res = await adminApiClient.get('/v1/admin/subscription-groups');
      return unwrapApiData<SubscriptionGroupOption[]>(res.data) ?? [];
    },
  });

  const assignMutation = useMutation({
    mutationFn: async (payload: {
      user_id: number;
      group_id: number;
      subscription_name: string;
      expires_at: number;
      metadata: string;
    }) => {
      const res = await adminApiClient.post('/v1/admin/subscriptions/assign', payload);
      ensureApiSuccess(res.data, t("分配订阅失败"));
    },
    onSuccess: () => {
      invalidate();
      setIsAssignOpen(false);
      toast.success(t("订阅已分配"));
    },
    onError: (error: Error) => toast.error(error.message),
  });

  const revokeMutation = useMutation({
    mutationFn: async ({ id, reason }: { id: number; reason: string }) => {
      const res = await adminApiClient.post(`/v1/admin/subscriptions/${id}/revoke`, { reason });
      ensureApiSuccess(res.data, t("撤销订阅失败"));
    },
    onSuccess: () => {
      invalidate();
      toast.success(t("订阅已撤销"));
    },
    onError: (error: Error) => toast.error(error.message),
  });

  const extendMutation = useMutation({
    mutationFn: async ({ id, expiresAt }: { id: number; expiresAt: number }) => {
      const res = await adminApiClient.post(`/v1/admin/subscriptions/${id}/extend`, {
        expires_at: expiresAt,
      });
      ensureApiSuccess(res.data, t("延长订阅失败"));
    },
    onSuccess: () => {
      invalidate();
      setExtendSubscription(null);
      toast.success(t("订阅到期时间已更新"));
    },
    onError: (error: Error) => toast.error(error.message),
  });

  const resetQuotaMutation = useMutation({
    mutationFn: async ({ id, scope }: { id: number; scope: string }) => {
      const res = await adminApiClient.post(`/v1/admin/subscriptions/${id}/reset-quota`, { scope });
      ensureApiSuccess(res.data, t("重置配额失败"));
    },
    onSuccess: () => {
      invalidate();
      toast.success(t("配额已重置"));
    },
    onError: (error: Error) => toast.error(error.message),
  });

  const handleFilter = () => {
    const trimmed = userIdInput.trim();
    if (trimmed === '') {
      setFilterUserId(null);
      return;
    }
    const parsed = parseInt(trimmed, 10);
    if (!Number.isFinite(parsed) || parsed <= 0) {
      toast.error(t("请输入有效的用户 ID"));
      return;
    }
    setFilterUserId(parsed);
  };

  const clearFilter = () => {
    setUserIdInput('');
    setFilterUserId(null);
  };

  const handleExtend = (sub: UserSubscription) => {
    setExtendSubscription(sub);
  };

  const handleRevoke = (sub: UserSubscription) => {
    if (!confirm(t(`确认撤销订阅「${sub.subscription_name || sub.id}」？`))) return;
    const reason = prompt(t("撤销原因(可选)："), '') ?? '';
    revokeMutation.mutate({ id: sub.id, reason });
  };

  const handleResetQuota = (sub: UserSubscription) => {
    const scope = prompt(t("重置范围(daily / weekly / monthly / all)："), 'all');
    if (scope == null) return;
    const normalized = scope.trim().toLowerCase();
    if (!['daily', 'weekly', 'monthly', 'all'].includes(normalized)) {
      toast.error(t("范围必须是 daily / weekly / monthly / all"));
      return;
    }
    resetQuotaMutation.mutate({ id: sub.id, scope: normalized });
  };

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-2xl font-semibold">{t("用户订阅")}</h2>
        <AssignDialog
          open={isAssignOpen}
          onOpenChange={setIsAssignOpen}
          groups={groups ?? []}
          defaultUserId={filterUserId ?? undefined}
          pending={assignMutation.isPending}
          onSubmit={(draft) =>
            assignMutation.mutate({
              user_id: draft.userId,
              group_id: draft.groupId,
              subscription_name: draft.subscriptionName,
              expires_at: draft.expiresAt,
              metadata: draft.metadata,
            })
          }
        />
      </div>

      {extendSubscription && (
        <ExtendDialog
          key={extendSubscription.id}
          open
          subscription={extendSubscription}
          pending={extendMutation.isPending}
          onOpenChange={(open) => {
            if (!open && !extendMutation.isPending) setExtendSubscription(null);
          }}
          onSubmit={(expiresAt) => extendMutation.mutate({ id: extendSubscription.id, expiresAt })}
        />
      )}

      <p className="text-sm text-muted-foreground">{t("默认列出全部订阅。可按用户 ID 筛选，筛选后额外展示该用户的配额进度。")}</p>

      <div className="flex items-center gap-2">
        <Input
          value={userIdInput}
          onChange={(e) => setUserIdInput(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && handleFilter()}
          placeholder={t("按用户 ID 筛选(留空为全部)")}
          type="number"
          className="max-w-64"
        />
        <Button onClick={handleFilter}>
          <Search className="size-4" />{t("筛选")}</Button>
        {filterUserId != null && (
          <Button variant="outline" onClick={clearFilter}>{t("全部")}</Button>
        )}
      </div>

      {filterUserId != null && progress && (
        <SubscriptionProgressCard
          progress={progress}
          title={t(`用户 ${filterUserId} 当前活跃订阅配额`)}
        />
      )}

      {isLoading ? (
        <TableSkeleton columns={['ID', t("用户"), t("名称"), t("分组"), t("状态"), t("开始"), t("到期"), t("日/周/月用量"), t("操作")]} />
      ) : !subscriptions || subscriptions.length === 0 ? (
        <EmptyState
          title={filterUserId != null ? t("该用户暂无订阅") : t("暂无订阅")}
          description={t("点击右上角「分配订阅」为用户分配一个订阅分组。")}
        />
      ) : (
        <div className="border rounded-lg">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>ID</TableHead>
                <TableHead>{t("用户")}</TableHead>
                <TableHead>{t("名称")}</TableHead>
                <TableHead className="hidden md:table-cell">{t("分组")}</TableHead>
                <TableHead>{t("状态")}</TableHead>
                <TableHead className="hidden lg:table-cell">{t("开始")}</TableHead>
                <TableHead className="hidden lg:table-cell">{t("到期")}</TableHead>
                <TableHead className="hidden xl:table-cell">{t("日/周/月用量 USD")}</TableHead>
                <TableHead className="text-right">{t("操作")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {subscriptions.map((sub) => (
                <TableRow key={sub.id}>
                  <TableCell className="font-mono text-sm">{sub.id}</TableCell>
                  <TableCell>
                    <button
                      type="button"
                      className="font-mono text-sm text-blue-600 hover:underline dark:text-blue-400"
                      onClick={() => {
                        setUserIdInput(String(sub.user_id));
                        setFilterUserId(sub.user_id);
                      }}
                    >
                      {sub.user_id}
                    </button>
                  </TableCell>
                  <TableCell className="font-medium">{sub.subscription_name || '—'}</TableCell>
                  <TableCell className="hidden md:table-cell">#{sub.group_id}</TableCell>
                  <TableCell>
                    <span
                      className={`inline-flex items-center px-2 py-1 rounded-full text-xs font-medium ${statusBadgeClass(sub.status)}`}
                    >
                      {sub.status}
                    </span>
                  </TableCell>
                  <TableCell className="hidden lg:table-cell">{formatTimestamp(sub.starts_at)}</TableCell>
                  <TableCell className="hidden lg:table-cell">{formatTimestamp(sub.expires_at)}</TableCell>
                  <TableCell className="hidden xl:table-cell text-xs">
                    ${sub.daily_usage_usd} / ${sub.weekly_usage_usd} / ${sub.monthly_usage_usd}
                  </TableCell>
                  <TableCell className="text-right space-x-2">
                    <Button variant="outline" size="sm" onClick={() => handleExtend(sub)}>
                      <CalendarClock className="size-3.5" />{t("延长")}</Button>
                    <Button variant="outline" size="sm" onClick={() => handleResetQuota(sub)}>{t("重置配额")}</Button>
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => handleRevoke(sub)}
                      disabled={sub.status === 'revoked'}
                    >{t("撤销")}</Button>
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

interface ExtendDialogProps {
  open: boolean;
  subscription: UserSubscription;
  pending: boolean;
  onOpenChange: (open: boolean) => void;
  onSubmit: (expiresAt: number) => void;
}

function ExtendDialog({ open, subscription, pending, onOpenChange, onSubmit }: ExtendDialogProps) {
  const [expiresAt, setExpiresAt] = useState(() => toDatetimeLocal(subscription.expires_at));

  const handleSubmit = () => {
    const parsed = toUnixSeconds(expiresAt);
    if (parsed <= 0) {
      toast.error(t("请选择有效的到期时间"));
      return;
    }
    onSubmit(parsed);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t("延长订阅")}</DialogTitle>
          <DialogDescription>{t("为「")}{subscription.subscription_name || t(`订阅 #${subscription.id}`)}{t("」选择新的到期日期和时间。")}</DialogDescription>
        </DialogHeader>
        <div className="grid gap-2 pt-2">
          <Label htmlFor="extend-subscription-expires">{t("新的到期时间")}</Label>
          <Input
            id="extend-subscription-expires"
            type="datetime-local"
            value={expiresAt}
            onChange={(event) => setExpiresAt(event.target.value)}
            disabled={pending}
          />
          <p className="text-xs text-muted-foreground">{t("当前到期：")}{formatTimestamp(subscription.expires_at)}
          </p>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={pending}>{t("取消")}</Button>
          <Button onClick={handleSubmit} disabled={pending}>
            {pending ? t("保存中...") : t("保存到期时间")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

interface AssignDraft {
  userId: number;
  groupId: number;
  subscriptionName: string;
  expiresAt: number;
  metadata: string;
}

interface AssignDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  groups: SubscriptionGroupOption[];
  defaultUserId?: number;
  pending: boolean;
  onSubmit: (draft: AssignDraft) => void;
}

function AssignDialog({ open, onOpenChange, groups, defaultUserId, pending, onSubmit }: AssignDialogProps) {
  const [userId, setUserId] = useState(defaultUserId ? String(defaultUserId) : '');
  const [groupId, setGroupId] = useState('');
  const [subscriptionName, setSubscriptionName] = useState('');
  const [expiresAt, setExpiresAt] = useState('');
  const [metadata, setMetadata] = useState('');

  const handleSubmit = () => {
    const parsedUser = parseInt(userId.trim(), 10);
    const parsedGroup = parseInt(groupId, 10);
    const expiresUnix = toUnixSeconds(expiresAt);
    if (!Number.isFinite(parsedUser) || parsedUser <= 0) {
      toast.error(t("请输入有效的用户 ID"));
      return;
    }
    if (!Number.isFinite(parsedGroup) || parsedGroup <= 0) {
      toast.error(t("请选择订阅分组"));
      return;
    }
    if (expiresUnix <= 0) {
      toast.error(t("请选择到期时间"));
      return;
    }
    onSubmit({
      userId: parsedUser,
      groupId: parsedGroup,
      subscriptionName: subscriptionName.trim(),
      expiresAt: expiresUnix,
      metadata: metadata.trim(),
    });
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger render={<Button />}>
        <UserPlus className="size-4" />{t("分配订阅")}</DialogTrigger>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t("分配订阅")}</DialogTitle>
          <DialogDescription>{t("为指定用户分配一个订阅分组并设置到期时间。")}</DialogDescription>
        </DialogHeader>
        <div className="grid gap-4 pt-2">
          <div className="space-y-2">
            <Label htmlFor="assign-user">{t("用户 ID")}</Label>
            <Input
              id="assign-user"
              type="number"
              value={userId}
              onChange={(e) => setUserId(e.target.value)}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="assign-group">{t("订阅分组")}</Label>
            <select
              id="assign-group"
              value={groupId}
              onChange={(e) => setGroupId(e.target.value)}
              className="h-8 w-full rounded-lg border border-input bg-background px-2.5 text-sm"
            >
              <option value="">{t("请选择分组")}</option>
              {groups.map((group) => (
                <option key={group.id} value={group.id}>
                  {group.display_name || group.name} (#{group.id})
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-2">
            <Label htmlFor="assign-name">{t("订阅名称(可选)")}</Label>
            <Input
              id="assign-name"
              value={subscriptionName}
              onChange={(e) => setSubscriptionName(e.target.value)}
              placeholder="alice-pro"
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="assign-expires">{t("到期时间")}</Label>
            <Input
              id="assign-expires"
              type="datetime-local"
              value={expiresAt}
              onChange={(e) => setExpiresAt(e.target.value)}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="assign-metadata">{t("Metadata(JSON，可选)")}</Label>
            <Input
              id="assign-metadata"
              value={metadata}
              onChange={(e) => setMetadata(e.target.value)}
            />
          </div>
          <Button onClick={handleSubmit} disabled={pending}>
            {pending ? t("分配中...") : t("分配")}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
