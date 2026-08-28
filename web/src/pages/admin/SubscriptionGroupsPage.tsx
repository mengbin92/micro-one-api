import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Layers, Pencil, Save } from 'lucide-react';
import { useMemo, useState } from 'react';
import { toast } from 'sonner';
import { adminApiClient } from '@/lib/api';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { EmptyState } from '@/components/EmptyState';
import { TableSkeleton } from '@/components/LoadingStates';
import { AdminTableToolbar } from '@/components/admin/AdminTableToolbar';
import { SortableHeader } from '@/components/admin/SortableHeader';
import { useAdminTableState } from '@/hooks/useAdminTableState';
import { unwrapApiData, ensureApiSuccess } from '@/lib/api-response';
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
import { t } from '@/lib/i18n';

// Mirrors subscriptionGroupDTO JSON tags returned by
// /api/v1/admin/subscription-groups (internal/admin/server/subscription.go).
interface SubscriptionGroup {
  id: number;
  name: string;
  display_name: string;
  platform: string;
  subscription_type: string;
  daily_limit_usd: number | null;
  weekly_limit_usd: number | null;
  monthly_limit_usd: number | null;
  rate_multiplier: number;
  status: number;
  price_quota: number;
  duration_days: number;
  created_at: number;
  updated_at: number;
}

// Payload decoded into subscription.biz.SubscriptionGroup on create/update.
interface GroupPayload {
  id?: number;
  name: string;
  display_name: string;
  platform: string;
  subscription_type: string;
  daily_limit_usd: number | null;
  weekly_limit_usd: number | null;
  monthly_limit_usd: number | null;
  rate_multiplier: number;
  status: number;
  price_quota: number;
  duration_days: number;
}

// Keep in sync with the platforms accepted by the hybrid relay adaptor layer.
const PLATFORM_OPTIONS: Array<{ value: string; label: string }> = [
  { value: 'anthropic', label: 'Anthropic / Claude' },
  { value: 'openai', label: 'OpenAI / Codex' },
  { value: 'gemini', label: 'Gemini' },
];

interface GroupDraft {
  id?: number;
  name: string;
  displayName: string;
  platform: string;
  subscriptionType: string;
  dailyLimit: string;
  weeklyLimit: string;
  monthlyLimit: string;
  rateMultiplier: string;
  status: string;
  price: string;
  durationDays: string;
}

const emptyDraft: GroupDraft = {
  name: '',
  displayName: '',
  platform: 'anthropic',
  subscriptionType: 'standard',
  dailyLimit: '',
  weeklyLimit: '',
  monthlyLimit: '',
  rateMultiplier: '1',
  status: '1',
  price: '0',
  durationDays: '0',
};

function platformLabel(platform: string) {
  return PLATFORM_OPTIONS.find((option) => option.value === platform)?.label ?? platform;
}

function formatLimit(value: number | null) {
  return value == null ? t("不限") : `$${value}`;
}

function formatPrice(value: number) {
  return `$${Number(value || 0).toFixed(2)}`;
}

function parsePrice(value: string) {
  const parsed = Number(value.trim() || '0');
  if (!Number.isFinite(parsed) || parsed <= 0) return 0;
  return Math.trunc(parsed);
}

function toDraft(group: SubscriptionGroup): GroupDraft {
  return {
    id: group.id,
    name: group.name,
    displayName: group.display_name,
    platform: group.platform,
    subscriptionType: group.subscription_type,
    dailyLimit: group.daily_limit_usd == null ? '' : String(group.daily_limit_usd),
    weeklyLimit: group.weekly_limit_usd == null ? '' : String(group.weekly_limit_usd),
    monthlyLimit: group.monthly_limit_usd == null ? '' : String(group.monthly_limit_usd),
    rateMultiplier: String(group.rate_multiplier ?? 1),
    status: String(group.status ?? 1),
    price: String(group.price_quota ?? 0),
    durationDays: String(group.duration_days ?? 0),
  };
}

// Empty input => unlimited (null); otherwise a parsed float.
function parseLimit(value: string): number | null {
  const trimmed = value.trim();
  if (trimmed === '') return null;
  const parsed = Number(trimmed);
  return Number.isFinite(parsed) ? parsed : null;
}

function draftToPayload(draft: GroupDraft): GroupPayload {
  return {
    ...(draft.id ? { id: draft.id } : {}),
    name: draft.name.trim(),
    display_name: draft.displayName.trim(),
    platform: draft.platform,
    subscription_type: draft.subscriptionType.trim() || 'standard',
    daily_limit_usd: parseLimit(draft.dailyLimit),
    weekly_limit_usd: parseLimit(draft.weeklyLimit),
    monthly_limit_usd: parseLimit(draft.monthlyLimit),
    rate_multiplier: Number(draft.rateMultiplier.trim() || '1'),
    status: parseInt(draft.status || '1', 10),
    price_quota: parsePrice(draft.price),
    duration_days: Math.max(0, Math.trunc(Number(draft.durationDays.trim() || '0'))),
  };
}

export function AdminSubscriptionGroupsPage() {
  const { search, sortKey, sortDirection, setSearch, clearSearch, setSort } = useAdminTableState({
    storageKey: 'subscription-groups',
  });
  const [isCreateOpen, setIsCreateOpen] = useState(false);
  const [editingDraft, setEditingDraft] = useState<GroupDraft | null>(null);
  const queryClient = useQueryClient();

  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: ['admin-subscription-groups'] });
  };

  const sort = {
    key: sortKey as keyof SubscriptionGroup | null,
    direction: sortDirection,
  } satisfies SortState<SubscriptionGroup>;

  const { data: groups, isLoading } = useQuery({
    queryKey: ['admin-subscription-groups'],
    queryFn: async () => {
      const res = await adminApiClient.get('/v1/admin/subscription-groups');
      return unwrapApiData<SubscriptionGroup[]>(res.data) ?? [];
    },
  });

  const createMutation = useMutation({
    mutationFn: async (payload: GroupPayload) => {
      const res = await adminApiClient.post('/v1/admin/subscription-groups', payload);
      ensureApiSuccess(res.data, t("分组创建失败"));
    },
    onSuccess: () => {
      invalidate();
      setIsCreateOpen(false);
      toast.success(t("订阅分组已创建"));
    },
    onError: (error: Error) => toast.error(error.message),
  });

  const updateMutation = useMutation({
    mutationFn: async (payload: GroupPayload) => {
      const res = await adminApiClient.put(`/v1/admin/subscription-groups/${payload.id}`, payload);
      ensureApiSuccess(res.data, t("分组更新失败"));
    },
    onSuccess: () => {
      invalidate();
      setEditingDraft(null);
      toast.success(t("订阅分组已保存"));
    },
    onError: (error: Error) => toast.error(error.message),
  });

  const deleteMutation = useMutation({
    mutationFn: async (id: number) => {
      const res = await adminApiClient.delete(`/v1/admin/subscription-groups/${id}`);
      ensureApiSuccess(res.data, t("分组删除失败"));
    },
    onSuccess: () => {
      invalidate();
      toast.success(t("订阅分组已删除"));
    },
    onError: (error: Error) => toast.error(error.message),
  });

  const visibleGroups = useMemo(() => {
    const keyword = search.trim().toLowerCase();
    const filtered = (groups ?? []).filter((group) => {
      if (!keyword) return true;
      return (
        group.name.toLowerCase().includes(keyword) ||
        group.display_name.toLowerCase().includes(keyword)
      );
    });
    return sortRows(filtered, sort);
  }, [groups, search, sort]);

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-2xl font-semibold">{t("订阅分组")}</h2>
        <GroupDialog
          mode="create"
          open={isCreateOpen}
          onOpenChange={setIsCreateOpen}
          onSubmit={(payload) => createMutation.mutate(payload)}
          pending={createMutation.isPending}
        />
      </div>

      <AdminTableToolbar
        search={search}
        searchPlaceholder={t("按名称搜索...")}
        onSearchChange={setSearch}
        onClear={clearSearch}
      />

      {isLoading ? (
        <TableSkeleton columns={['ID', t("名称"), t("平台"), t("类型"), t("日限额"), t("周限额"), t("月限额"), t("倍率"), t("售价/有效期"), t("状态"), t("操作")]} />
      ) : !groups || groups.length === 0 ? (
        <EmptyState title={t("暂无订阅分组")} description={t("新建一个订阅分组以配置平台、配额限额与计费倍率。")} />
      ) : visibleGroups.length === 0 ? (
        <EmptyState title={t("没有匹配的分组")} description={t("清除搜索条件以查看全部分组。")} />
      ) : (
        <div className="border rounded-lg">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>ID</TableHead>
                <SortableHeader<SubscriptionGroup> columnKey="name" sort={sort} onSortChange={setSort}>{t("名称")}</SortableHeader>
                <SortableHeader<SubscriptionGroup> columnKey="platform" sort={sort} onSortChange={setSort}>{t("平台")}</SortableHeader>
                <TableHead className="hidden lg:table-cell">{t("类型")}</TableHead>
                <TableHead className="hidden lg:table-cell">{t("日限额")}</TableHead>
                <TableHead className="hidden xl:table-cell">{t("周限额")}</TableHead>
                <TableHead className="hidden xl:table-cell">{t("月限额")}</TableHead>
                <TableHead className="hidden md:table-cell">{t("倍率")}</TableHead>
                <TableHead className="hidden lg:table-cell">{t("售价/有效期")}</TableHead>
                <TableHead>{t("状态")}</TableHead>
                <TableHead className="text-right">{t("操作")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {visibleGroups.map((group) => (
                <TableRow key={group.id}>
                  <TableCell className="font-mono text-sm">{group.id}</TableCell>
                  <TableCell className="font-medium">
                    <div>{group.display_name || group.name}</div>
                    <div className="text-xs text-muted-foreground">{group.name}</div>
                  </TableCell>
                  <TableCell>{platformLabel(group.platform)}</TableCell>
                  <TableCell className="hidden lg:table-cell">{group.subscription_type}</TableCell>
                  <TableCell className="hidden lg:table-cell">{formatLimit(group.daily_limit_usd)}</TableCell>
                  <TableCell className="hidden xl:table-cell">{formatLimit(group.weekly_limit_usd)}</TableCell>
                  <TableCell className="hidden xl:table-cell">{formatLimit(group.monthly_limit_usd)}</TableCell>
                  <TableCell className="hidden md:table-cell">×{group.rate_multiplier}</TableCell>
                  <TableCell className="hidden lg:table-cell">
                    {group.price_quota > 0 && group.duration_days > 0
                      ? t(`${formatPrice(group.price_quota)} / ${group.duration_days}天`)
                      : t("不可购买")}
                  </TableCell>
                  <TableCell>
                    <span
                      className={`inline-flex items-center px-2 py-1 rounded-full text-xs font-medium ${
                        group.status === 1
                          ? 'bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200'
                          : 'bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-200'
                      }`}
                    >
                      {group.status === 1 ? t("启用") : t("停用")}
                    </span>
                  </TableCell>
                  <TableCell className="text-right space-x-2">
                    <Button variant="outline" size="sm" onClick={() => setEditingDraft(toDraft(group))}>
                      <Pencil className="size-3.5" />{t("编辑")}</Button>
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => {
                        if (confirm(t(`确认删除订阅分组「${group.display_name || group.name}」？`))) {
                          deleteMutation.mutate(group.id);
                        }
                      }}
                      disabled={deleteMutation.isPending}
                    >{t("删除")}</Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      {editingDraft && (
        <GroupDialog
          // Remount per edit target so the form always seeds from fresh values.
          key={editingDraft.id}
          mode="edit"
          draft={editingDraft}
          open
          onOpenChange={(open) => !open && setEditingDraft(null)}
          onSubmit={(payload) => updateMutation.mutate(payload)}
          pending={updateMutation.isPending}
        />
      )}
    </div>
  );
}

interface GroupDialogProps {
  mode: 'create' | 'edit';
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSubmit: (payload: GroupPayload) => void;
  pending: boolean;
  draft?: GroupDraft | null;
}

function GroupDialog({ mode, open, onOpenChange, onSubmit, pending, draft }: GroupDialogProps) {
  // In edit mode the parent remounts this component per target (via key), so the
  // initial value from `draft` is always fresh — no in-place sync needed.
  const [form, setForm] = useState<GroupDraft>(draft ?? emptyDraft);

  const handleSubmit = () => {
    if (!form.name.trim()) {
      toast.error(t("分组名称为必填项"));
      return;
    }
    onSubmit(draftToPayload(form));
    if (mode === 'create') setForm({ ...emptyDraft });
  };

  const body = (
    <DialogContent className="sm:max-w-lg">
      <DialogHeader>
        <DialogTitle>{mode === 'create' ? t("新建订阅分组") : t("编辑订阅分组")}</DialogTitle>
        <DialogDescription>{t("配置平台、订阅类型、日/周/月 USD 配额限额(留空表示不限)、计费倍率，以及用户自助购买的价格与有效期。")}</DialogDescription>
      </DialogHeader>
      <div className="grid gap-4 pt-2 sm:grid-cols-2">
        <div className="space-y-2">
          <Label htmlFor="grp-name">{t("名称(唯一)")}</Label>
          <Input
            id="grp-name"
            value={form.name}
            onChange={(e) => setForm({ ...form, name: e.target.value })}
            placeholder="claude-pro"
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="grp-display">{t("显示名称")}</Label>
          <Input
            id="grp-display"
            value={form.displayName}
            onChange={(e) => setForm({ ...form, displayName: e.target.value })}
            placeholder={t("Claude Pro 订阅")}
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="grp-platform">{t("平台")}</Label>
          <select
            id="grp-platform"
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
          <Label htmlFor="grp-type">{t("订阅类型")}</Label>
          <Input
            id="grp-type"
            value={form.subscriptionType}
            onChange={(e) => setForm({ ...form, subscriptionType: e.target.value })}
            placeholder="standard"
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="grp-daily">{t("日限额 USD(留空不限)")}</Label>
          <Input
            id="grp-daily"
            type="number"
            value={form.dailyLimit}
            onChange={(e) => setForm({ ...form, dailyLimit: e.target.value })}
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="grp-weekly">{t("周限额 USD(留空不限)")}</Label>
          <Input
            id="grp-weekly"
            type="number"
            value={form.weeklyLimit}
            onChange={(e) => setForm({ ...form, weeklyLimit: e.target.value })}
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="grp-monthly">{t("月限额 USD(留空不限)")}</Label>
          <Input
            id="grp-monthly"
            type="number"
            value={form.monthlyLimit}
            onChange={(e) => setForm({ ...form, monthlyLimit: e.target.value })}
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="grp-rate">{t("计费倍率")}</Label>
          <Input
            id="grp-rate"
            type="number"
            step="0.1"
            value={form.rateMultiplier}
            onChange={(e) => setForm({ ...form, rateMultiplier: e.target.value })}
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="grp-price">{t("购买价格(USD)")}</Label>
          <Input
            id="grp-price"
            type="number"
            min="0"
            value={form.price}
            onChange={(e) => setForm({ ...form, price: e.target.value })}
            placeholder="10"
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="grp-duration">{t("有效期(天)")}</Label>
          <Input
            id="grp-duration"
            type="number"
            min="0"
            value={form.durationDays}
            onChange={(e) => setForm({ ...form, durationDays: e.target.value })}
            placeholder="30"
          />
        </div>
        <p className="text-xs text-muted-foreground sm:col-span-2">{t("价格与有效期任一为 0 时，该分组仅供管理员分配、不在用户端出售。")}</p>
        <div className="space-y-2 sm:col-span-2">
          <Label htmlFor="grp-status">{t("状态")}</Label>
          <select
            id="grp-status"
            value={form.status}
            onChange={(e) => setForm({ ...form, status: e.target.value })}
            className="h-8 w-full rounded-lg border border-input bg-background px-2.5 text-sm"
          >
            <option value="1">{t("启用")}</option>
            <option value="2">{t("停用")}</option>
          </select>
        </div>
        <Button onClick={handleSubmit} disabled={pending} className="sm:col-span-2">
          <Save className="size-4" />
          {pending ? t("保存中...") : mode === 'create' ? t("创建") : t("保存配置")}
        </Button>
      </div>
    </DialogContent>
  );

  if (mode === 'create') {
    return (
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogTrigger render={<Button />}>
          <Layers className="size-4" />{t("新建分组")}</DialogTrigger>
        {body}
      </Dialog>
    );
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      {body}
    </Dialog>
  );
}
