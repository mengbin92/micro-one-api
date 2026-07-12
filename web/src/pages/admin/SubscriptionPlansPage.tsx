import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Package, Pencil, Save, ShoppingCart } from 'lucide-react';
import { useMemo, useState } from 'react';
import { toast } from 'sonner';
import { adminApiClient } from '@/lib/api';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { EmptyState } from '@/components/EmptyState';
import { TableSkeleton } from '@/components/LoadingStates';
import { AdminTableToolbar } from '@/components/admin/AdminTableToolbar';
import { unwrapApiData, ensureApiSuccess } from '@/lib/api-response';
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
} from '@/components/ui/dialog';

// Mirrors subscriptionPlanDTO JSON tags returned by
// /api/v1/admin/subscription-plans (internal/admin/server/subscription.go).
interface SubscriptionPlan {
  id: number;
  name: string;
  product_name: string;
  group_id: number;
  price_quota: number;
  validity_days: number;
  validity_unit: string;
  for_sale: boolean;
  sort_order: number;
  created_at: number;
  updated_at: number;
}

interface PlanPayload {
  id?: number;
  name: string;
  product_name: string;
  group_id: number;
  price_quota: number;
  validity_days: number;
  validity_unit: string;
  sort_order: number;
}

const emptyPayload: PlanPayload = {
  name: '',
  product_name: '',
  group_id: 0,
  price_quota: 0,
  validity_days: 30,
  validity_unit: 'day',
  sort_order: 0,
};

type SaleFilter = 'all' | 'on' | 'off';

function saleFilterParam(filter: SaleFilter): string | undefined {
  if (filter === 'on') return 'true';
  if (filter === 'off') return 'false';
  return undefined;
}

export function AdminSubscriptionPlansPage() {
  const queryClient = useQueryClient();
  const [saleFilter, setSaleFilter] = useState<SaleFilter>('all');
  const [keyword, setKeyword] = useState('');
  const [editing, setEditing] = useState<PlanPayload | null>(null);

  const { data: plans, isLoading } = useQuery({
    queryKey: ['admin', 'subscription-plans', saleFilter],
    queryFn: async () => {
      const forSale = saleFilterParam(saleFilter);
      const params = forSale ? { for_sale: forSale } : {};
      const res = await adminApiClient.get('/v1/admin/subscription-plans', { params });
      return unwrapApiData<SubscriptionPlan[]>(res.data);
    },
  });

  const filtered = useMemo(() => {
    const list = plans ?? [];
    const k = keyword.trim().toLowerCase();
    if (!k) return list;
    return list.filter(
      (p) =>
        p.name.toLowerCase().includes(k) ||
        p.product_name?.toLowerCase().includes(k) ||
        String(p.id) === k,
    );
  }, [plans, keyword]);

  const toggleForSale = useMutation({
    mutationFn: async ({ id, forSale }: { id: number; forSale: boolean }) => {
      const res = await adminApiClient.post(
        `/v1/admin/subscription-plans/${id}/for-sale`,
        { for_sale: forSale },
      );
      ensureApiSuccess(res.data);
    },
    onSuccess: (_d, vars) => {
      toast.success(vars.forSale ? '套餐已上架' : '套餐已下架');
      void queryClient.invalidateQueries({ queryKey: ['admin', 'subscription-plans'] });
    },
    onError: (e: unknown) => toast.error(String(e instanceof Error ? e.message : e)),
  });

  const savePlan = useMutation({
    mutationFn: async (payload: PlanPayload) => {
      if (payload.id) {
        const res = await adminApiClient.put(
          `/v1/admin/subscription-plans/${payload.id}`,
          payload,
        );
        ensureApiSuccess(res.data);
      } else {
        const res = await adminApiClient.post('/v1/admin/subscription-plans', payload);
        ensureApiSuccess(res.data);
      }
    },
    onSuccess: () => {
      toast.success('套餐已保存');
      setEditing(null);
      void queryClient.invalidateQueries({ queryKey: ['admin', 'subscription-plans'] });
    },
    onError: (e: unknown) => toast.error(String(e instanceof Error ? e.message : e)),
  });

  const startEdit = (p: SubscriptionPlan) =>
    setEditing({
      id: p.id,
      name: p.name,
      product_name: p.product_name,
      group_id: p.group_id,
      price_quota: p.price_quota,
      validity_days: p.validity_days,
      validity_unit: p.validity_unit,
      sort_order: p.sort_order,
    });

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-semibold">
            <Package className="h-5 w-5" /> 订阅套餐
          </h1>
          <p className="text-sm text-muted-foreground">
            套餐上下架、在售状态审计与用户侧展示收敛。
          </p>
        </div>
        <Button onClick={() => setEditing({ ...emptyPayload })}>
          <ShoppingCart className="mr-1 h-4 w-4" /> 新建套餐
        </Button>
      </div>

      <AdminTableToolbar
        search={keyword}
        searchPlaceholder="搜索套餐名称 / ID"
        onSearchChange={setKeyword}
        onClear={() => setKeyword('')}
        actions={
          <div className="flex items-center gap-2">
            {(['all', 'on', 'off'] as SaleFilter[]).map((f) => (
              <Button
                key={f}
                variant={saleFilter === f ? 'default' : 'outline'}
                size="sm"
                onClick={() => setSaleFilter(f)}
              >
                {f === 'all' ? '全部' : f === 'on' ? '在售' : '已下架'}
              </Button>
            ))}
          </div>
        }
      />

      {isLoading ? (
        <TableSkeleton columns={['ID', '名称', '分组', '价格', '有效期', '状态', '操作']} />
      ) : filtered.length === 0 ? (
        <EmptyState title="暂无套餐" description="新建套餐后会显示在这里。" />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>ID</TableHead>
              <TableHead>名称</TableHead>
              <TableHead>分组</TableHead>
              <TableHead>价格 (quota)</TableHead>
              <TableHead>有效期</TableHead>
              <TableHead>状态</TableHead>
              <TableHead className="text-right">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {filtered.map((p) => (
              <TableRow key={p.id}>
                <TableCell className="font-mono">{p.id}</TableCell>
                <TableCell className="font-medium">{p.name}</TableCell>
                <TableCell>{p.group_id}</TableCell>
                <TableCell>{p.price_quota}</TableCell>
                <TableCell>
                  {p.validity_days} {p.validity_unit === 'day' ? '天' : p.validity_unit}
                </TableCell>
                <TableCell>
                  {p.for_sale ? (
                    <span className="rounded bg-green-100 px-2 py-0.5 text-xs text-green-700 dark:bg-green-900 dark:text-green-300">
                      在售
                    </span>
                  ) : (
                    <span className="rounded bg-gray-100 px-2 py-0.5 text-xs text-gray-600 dark:bg-gray-800 dark:text-gray-400">
                      已下架
                    </span>
                  )}
                </TableCell>
                <TableCell className="text-right">
                  <div className="flex justify-end gap-2">
                    <Button variant="ghost" size="sm" onClick={() => startEdit(p)}>
                      <Pencil className="h-4 w-4" />
                    </Button>
                    {p.for_sale ? (
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => toggleForSale.mutate({ id: p.id, forSale: false })}
                      >
                        下架
                      </Button>
                    ) : (
                      <Button
                        variant="default"
                        size="sm"
                        onClick={() => toggleForSale.mutate({ id: p.id, forSale: true })}
                      >
                        上架
                      </Button>
                    )}
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      <Dialog open={editing !== null} onOpenChange={(o) => !o && setEditing(null)}>
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle>{editing?.id ? '编辑套餐' : '新建套餐'}</DialogTitle>
            <DialogDescription>
              价格、有效期变更不会影响已下单订单（按下单时的快照发放）。
            </DialogDescription>
          </DialogHeader>
          {editing && (
            <PlanEditForm
              key={editing.id ?? 'new'}
              initial={editing}
              saving={savePlan.isPending}
              onSave={(p) => savePlan.mutate(p)}
              onCancel={() => setEditing(null)}
            />
          )}
        </DialogContent>
      </Dialog>
    </div>
  );
}

function PlanEditForm({
  initial,
  saving,
  onSave,
  onCancel,
}: {
  initial: PlanPayload;
  saving: boolean;
  onSave: (p: PlanPayload) => void;
  onCancel: () => void;
}) {
  const [form, setForm] = useState<PlanPayload>(initial);
  const set = <K extends keyof PlanPayload>(k: K, v: PlanPayload[K]) =>
    setForm((f) => ({ ...f, [k]: v }));

  return (
    <form
      className="space-y-3"
      onSubmit={(e) => {
        e.preventDefault();
        onSave(form);
      }}
    >
      <div className="space-y-1">
        <Label htmlFor="plan-name">套餐名称</Label>
        <Input
          id="plan-name"
          value={form.name}
          onChange={(e) => set('name', e.target.value)}
          required
        />
      </div>
      <div className="space-y-1">
        <Label htmlFor="plan-product">产品名称</Label>
        <Input
          id="plan-product"
          value={form.product_name}
          onChange={(e) => set('product_name', e.target.value)}
        />
      </div>
      <div className="grid grid-cols-2 gap-3">
        <div className="space-y-1">
          <Label htmlFor="plan-group">分组 ID</Label>
          <Input
            id="plan-group"
            type="number"
            value={form.group_id}
            onChange={(e) => set('group_id', Number(e.target.value))}
            required
          />
        </div>
        <div className="space-y-1">
          <Label htmlFor="plan-price">价格 (quota)</Label>
          <Input
            id="plan-price"
            type="number"
            value={form.price_quota}
            onChange={(e) => set('price_quota', Number(e.target.value))}
            required
          />
        </div>
      </div>
      <div className="grid grid-cols-2 gap-3">
        <div className="space-y-1">
          <Label htmlFor="plan-validity">有效期</Label>
          <Input
            id="plan-validity"
            type="number"
            value={form.validity_days}
            onChange={(e) => set('validity_days', Number(e.target.value))}
            required
          />
        </div>
        <div className="space-y-1">
          <Label htmlFor="plan-sort">排序</Label>
          <Input
            id="plan-sort"
            type="number"
            value={form.sort_order}
            onChange={(e) => set('sort_order', Number(e.target.value))}
          />
        </div>
      </div>
      <div className="flex justify-end gap-2 pt-2">
        <Button type="button" variant="outline" onClick={onCancel}>
          取消
        </Button>
        <Button type="submit" disabled={saving}>
          <Save className="mr-1 h-4 w-4" /> 保存
        </Button>
      </div>
    </form>
  );
}
