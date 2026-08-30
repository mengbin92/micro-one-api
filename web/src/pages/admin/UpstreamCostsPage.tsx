import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Pencil, Plus, Trash2, Upload } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import { EmptyState } from '@/components/EmptyState';
import { TableSkeleton } from '@/components/LoadingStates';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { adminApiClient } from '@/lib/api';
import { ensureApiSuccess } from '@/lib/api-response';
import { t } from '@/lib/i18n';

// ── Types matching the /api/admin/upstream-costs contract ──────────────────

interface UpstreamCostEntry {
  key: string;
  source_kind: string; // channel | subscription | model
  source_id: number;
  source_name: string;
  upstream_model_id: string;
  public_model_id: string;
  input_price: number;
  output_price: number;
  cache_read_price?: number;
}

interface UpstreamCostView {
  entries: UpstreamCostEntry[];
  legacy_keys: UpstreamCostEntry[];
  total: number;
}

interface MigrationChange {
  old_key: string;
  new_key: string;
  source_id: number;
  public_model_id: string;
  upstream_model_id: string;
  reason?: string;
}

interface MigrationPlan {
  to_rewrite: MigrationChange[];
  skipped: MigrationChange[];
  executed?: number;
}

interface UpstreamCostSavePayload {
  source_kind: string;
  source_id: number;
  upstream_model_id: string;
  public_model_id: string;
  input_price: number;
  output_price: number;
  cache_read_price?: number;
  cache_read_price_set: boolean;
}

const MTOK = 1_000_000;

function perTokenToMTok(value: number | undefined) {
  if (value === undefined) return '';
  return String(Number((value * MTOK).toPrecision(10)));
}

function mTokToPerToken(value: string) {
  if (!value.trim()) return 0;
  const parsed = Number(value);
  if (!Number.isFinite(parsed) || parsed < 0) return 0;
  return Number((parsed / MTOK).toPrecision(10));
}

function formatPrice(value: number | undefined) {
  if (value === undefined) return '—';
  return `$${perTokenToMTok(value)} / 1M`;
}

function sourceLabel(kind: string, entry?: UpstreamCostEntry) {
  switch (kind) {
    case 'channel':
      return t(`渠道 ${entry?.source_id ?? ''}${entry?.source_name ? ` · ${entry.source_name}` : ''}`);
    case 'subscription':
      return t(`订阅账号 ${entry?.source_id ?? ''}${entry?.source_name ? ` · ${entry.source_name}` : ''}`);
    default:
      return t("全局默认");
  }
}

function emptyForm() {
  return {
    sourceKind: 'channel',
    sourceId: '',
    upstreamModelId: '',
    publicModelId: '',
    inputPrice: '',
    outputPrice: '',
    cacheReadPrice: '',
  };
}

type FormState = ReturnType<typeof emptyForm>;

export function AdminUpstreamCostsPage() {
  const queryClient = useQueryClient();
  const [dialogOpen, setDialogOpen] = useState(false);
  const [editing, setEditing] = useState<UpstreamCostEntry | null>(null);
  const [form, setForm] = useState<FormState>(emptyForm());
  const [migrationPlan, setMigrationPlan] = useState<MigrationPlan | null>(null);
  const [confirmKey, setConfirmKey] = useState<string | null>(null);

  const { data, isLoading } = useQuery<UpstreamCostView>({
    queryKey: ['admin-upstream-costs'],
    queryFn: async () => {
      const res = await adminApiClient.get<UpstreamCostView>('/admin/upstream-costs');
      return res.data;
    },
  });

  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: ['admin-upstream-costs'] });
  };

  const saveMutation = useMutation({
    mutationFn: async () => {
      const sourceKind = form.sourceKind;
      const inputPrice = mTokToPerToken(form.inputPrice);
      const outputPrice = mTokToPerToken(form.outputPrice);
      if (sourceKind === 'channel' || sourceKind === 'subscription') {
        if (!form.sourceId.trim() || !form.upstreamModelId.trim()) {
          throw new Error(t("渠道/订阅账号成本需要填写来源 ID 和上游模型 ID"));
        }
      } else if (!form.publicModelId.trim()) {
        throw new Error(t("全局默认成本需要填写公开模型 ID"));
      }
      const payload: UpstreamCostSavePayload = {
        source_kind: sourceKind,
        source_id: sourceKind === 'model' ? 0 : Number(form.sourceId),
        upstream_model_id: sourceKind === 'model' ? '' : form.upstreamModelId.trim(),
        public_model_id: sourceKind === 'model' ? form.publicModelId.trim().toLowerCase() : form.publicModelId.trim(),
        input_price: inputPrice,
        output_price: outputPrice,
        cache_read_price: form.cacheReadPrice.trim() ? mTokToPerToken(form.cacheReadPrice) : undefined,
        cache_read_price_set: true,
      };
      const res = await adminApiClient.post('/admin/upstream-costs', payload);
      ensureApiSuccess(res.data, t("保存上游成本失败"));
    },
    onSuccess: () => {
      setDialogOpen(false);
      setEditing(null);
      setForm(emptyForm());
      invalidate();
      toast.success(t("上游成本已保存"));
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t("保存上游成本失败"));
    },
  });

  const deleteMutation = useMutation({
    mutationFn: async (key: string) => {
      const res = await adminApiClient.delete('/admin/upstream-costs', { params: { key } });
      ensureApiSuccess(res.data, t("删除上游成本失败"));
    },
    onSuccess: () => {
      setConfirmKey(null);
      invalidate();
      toast.success(t("已删除"));
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t("删除失败"));
    },
  });

  const migrateMutation = useMutation({
    mutationFn: async (dryRun: boolean) => {
      const res = await adminApiClient.post<MigrationPlan>('/admin/upstream-costs/migrate', { dry_run: dryRun });
      return res.data;
    },
    onSuccess: (plan, dryRun) => {
      if (dryRun) {
        setMigrationPlan(plan);
        return;
      }
      // Executed: close the plan dialog so the (already applied) plan cannot
      // be re-submitted, and refresh the list to drop the migrated keys.
      setMigrationPlan(null);
      invalidate();
      const executed = plan.executed ?? plan.to_rewrite.length;
      const failedNote = plan.to_rewrite.length - executed > 0 ? t(`，${plan.to_rewrite.length - executed} 条因目标键已存在被跳过`) : '';
      const skippedNote = plan.skipped.length > 0 ? t(`，${plan.skipped.length} 条需人工处理`) : '';
      toast.success(t(`已迁移 ${executed} 个 legacy 键${failedNote}${skippedNote}`));
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : t("迁移失败"));
    },
  });

  const openCreate = () => {
    setEditing(null);
    setForm(emptyForm());
    setDialogOpen(true);
  };

  const openEdit = (entry: UpstreamCostEntry) => {
    setEditing(entry);
    setForm({
      sourceKind: entry.source_kind,
      sourceId: entry.source_id > 0 ? String(entry.source_id) : '',
      upstreamModelId: entry.upstream_model_id ?? '',
      publicModelId: entry.public_model_id ?? '',
      inputPrice: perTokenToMTok(entry.input_price),
      outputPrice: perTokenToMTok(entry.output_price),
      cacheReadPrice: perTokenToMTok(entry.cache_read_price),
    });
    setDialogOpen(true);
  };

  const entries = data?.entries ?? [];
  const legacyKeys = data?.legacy_keys ?? [];
  const migrationCount = migrationPlan ? migrationPlan.to_rewrite.length + migrationPlan.skipped.length : 0;

  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h2 className="text-2xl font-semibold">{t("上游成本")}</h2>
          <p className="mt-1 text-sm text-muted-foreground">{t("配置各渠道 / 订阅账号的上游采购成本，与用户售价（模型价格）相互独立；保存后用于毛利核算与告警。")}</p>
        </div>
        <div className="flex gap-2">
          {legacyKeys.length > 0 && (
            <Button
              variant="outline"
              onClick={() => migrateMutation.mutate(true)}
              disabled={migrateMutation.isPending}
            >
              <Upload className="size-4" />{t("迁移 legacy 键")}</Button>
          )}
          <Button onClick={openCreate}>
            <Plus className="size-4" />{t("添加上游成本")}</Button>
        </div>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>{t("当前上游成本")}</CardTitle>
          <CardDescription>{t("键格式：`channel:<id>:<上游模型ID>` / `subscription:<id>:<上游模型ID>`；全局默认使用裸公开模型 ID。")}</CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <TableSkeleton columns={[t("来源"), t("上游模型 ID"), t("公开模型 ID"), t("输入价格"), t("输出价格"), t("缓存读取价格"), t("操作")]} rows={6} />
          ) : entries.length === 0 && legacyKeys.length === 0 ? (
            <EmptyState
              title={t("暂无上游成本配置")}
              description={t("添加上游成本后，此处将按来源列出各渠道 / 订阅账号的采购价格。")}
            />
          ) : (
            <div className="overflow-x-auto rounded-lg border">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t("来源")}</TableHead>
                    <TableHead>{t("上游模型 ID")}</TableHead>
                    <TableHead>{t("公开模型 ID")}</TableHead>
                    <TableHead>{t("输入价格")}</TableHead>
                    <TableHead>{t("输出价格")}</TableHead>
                    <TableHead>{t("缓存读取价格")}</TableHead>
                    <TableHead className="w-24 text-right">{t("操作")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {entries.map((entry) => (
                    <TableRow key={entry.key}>
                      <TableCell className="whitespace-nowrap font-medium">{sourceLabel(entry.source_kind, entry)}</TableCell>
                      <TableCell className="font-mono text-sm">{entry.upstream_model_id || '—'}</TableCell>
                      <TableCell className="font-mono text-sm">{entry.public_model_id || '—'}</TableCell>
                      <TableCell>{formatPrice(entry.input_price)}</TableCell>
                      <TableCell>{formatPrice(entry.output_price)}</TableCell>
                      <TableCell>{formatPrice(entry.cache_read_price)}</TableCell>
                      <TableCell className="text-right">
                        <div className="flex justify-end gap-1">
                          <Button type="button" variant="ghost" size="icon-sm" aria-label={t(`编辑 ${entry.key}`)} onClick={() => openEdit(entry)}>
                            <Pencil className="size-4" />
                          </Button>
                          <Button type="button" variant="ghost" size="icon-sm" aria-label={t(`删除 ${entry.key}`)} onClick={() => setConfirmKey(entry.key)}>
                            <Trash2 className="size-4" />
                          </Button>
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}

          {legacyKeys.length > 0 && (
            <div className="mt-6">
              <h3 className="mb-2 text-sm font-semibold text-amber-600 dark:text-amber-400">
                {t(`legacy 键（旧格式，待迁移）· ${legacyKeys.length} 条`)}
              </h3>
              <div className="overflow-x-auto rounded-lg border">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>{t("旧键")}</TableHead>
                      <TableHead>{t("来源")}</TableHead>
                      <TableHead>{t("公开模型 ID")}</TableHead>
                      <TableHead>{t("输入价格")}</TableHead>
                      <TableHead>{t("输出价格")}</TableHead>
                      <TableHead>{t("缓存读取价格")}</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {legacyKeys.map((entry) => (
                      <TableRow key={entry.key}>
                        <TableCell className="font-mono text-sm">{entry.key}</TableCell>
                        <TableCell>{sourceLabel(entry.source_kind, entry)}</TableCell>
                        <TableCell className="font-mono text-sm">{entry.public_model_id || '—'}</TableCell>
                        <TableCell>{formatPrice(entry.input_price)}</TableCell>
                        <TableCell>{formatPrice(entry.output_price)}</TableCell>
                        <TableCell>{formatPrice(entry.cache_read_price)}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
              <p className="mt-2 text-xs text-muted-foreground">{t("legacy 键使用旧格式 `<channel_id>:<public_model_id>`，迁移后可解析为规范的 `channel:<id>:<upstream_model_id>`。可先点「迁移 legacy 键」预览，再确认执行。")}</p>
            </div>
          )}
        </CardContent>
      </Card>

      <Dialog open={dialogOpen} onOpenChange={(open) => !open && setDialogOpen(false)}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{editing ? t("编辑上游成本") : t("添加上游成本")}</DialogTitle>
            <DialogDescription>{t("按来源配置每 1M tokens 的上游采购成本（美元）。")}</DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="uc-source-kind">{t("来源类型")}</Label>
              <select
                id="uc-source-kind"
                value={form.sourceKind}
                onChange={(event) => setForm((current) => ({ ...current, sourceKind: event.target.value }))}
                className="h-8 w-full rounded-lg border border-input bg-background px-2.5 text-sm"
              >
                <option value="channel">{t("渠道（channel）")}</option>
                <option value="subscription">{t("订阅账号（subscription）")}</option>
                <option value="model">{t("全局默认（裸模型 ID）")}</option>
              </select>
            </div>

            {form.sourceKind !== 'model' && (
              <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                <div className="space-y-2">
                  <Label htmlFor="uc-source-id">{t("来源 ID")}</Label>
                  <Input
                    id="uc-source-id"
                    type="number"
                    min="1"
                    value={form.sourceId}
                    onChange={(event) => setForm((current) => ({ ...current, sourceId: event.target.value }))}
                    placeholder={form.sourceKind === 'channel' ? t("渠道 ID，如 1") : t("订阅账号 ID，如 4")}
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="uc-upstream-model">{t("上游模型 ID")}</Label>
                  <Input
                    id="uc-upstream-model"
                    value={form.upstreamModelId}
                    onChange={(event) => setForm((current) => ({ ...current, upstreamModelId: event.target.value }))}
                    placeholder="deepseek-v4-flash-0731"
                    className="font-mono"
                  />
                </div>
              </div>
            )}

            {form.sourceKind === 'model' && (
              <div className="space-y-2">
                <Label htmlFor="uc-public-model">{t("公开模型 ID")}</Label>
                <Input
                  id="uc-public-model"
                  value={form.publicModelId}
                  onChange={(event) => setForm((current) => ({ ...current, publicModelId: event.target.value }))}
                  placeholder="deepseek-v4-flash-0731"
                  className="font-mono"
                />
              </div>
            )}

            <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
              <div className="space-y-2">
                <Label htmlFor="uc-input-price">{t("输入价格（$/1M tokens）")}</Label>
                <Input
                  id="uc-input-price"
                  type="number"
                  min="0"
                  step="0.000001"
                  value={form.inputPrice}
                  onChange={(event) => setForm((current) => ({ ...current, inputPrice: event.target.value }))}
                  placeholder="0.14"
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="uc-output-price">{t("输出价格（$/1M tokens）")}</Label>
                <Input
                  id="uc-output-price"
                  type="number"
                  min="0"
                  step="0.000001"
                  value={form.outputPrice}
                  onChange={(event) => setForm((current) => ({ ...current, outputPrice: event.target.value }))}
                  placeholder="0.28"
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="uc-cache-read-price">{t("缓存读取价格（$/1M tokens）")}</Label>
                <Input
                  id="uc-cache-read-price"
                  type="number"
                  min="0"
                  step="0.000001"
                  value={form.cacheReadPrice}
                  onChange={(event) => setForm((current) => ({ ...current, cacheReadPrice: event.target.value }))}
                  placeholder="0.028"
                />
              </div>
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDialogOpen(false)}>{t("取消")}</Button>
            <Button onClick={() => saveMutation.mutate()} disabled={saveMutation.isPending}>
              {saveMutation.isPending ? t("保存中...") : t("保存")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={!!confirmKey} onOpenChange={(open) => !open && setConfirmKey(null)}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t("删除上游成本")}</DialogTitle>
            <DialogDescription>{t("确认删除以下上游成本配置？此操作不可撤销。")}</DialogDescription>
          </DialogHeader>
          {confirmKey && (
            <div className="rounded-lg border bg-muted/20 px-3 py-2 font-mono text-xs break-all">{confirmKey}</div>
          )}
          <DialogFooter>
            <Button variant="outline" onClick={() => setConfirmKey(null)}>{t("取消")}</Button>
            <Button
              variant="destructive"
              disabled={deleteMutation.isPending}
              onClick={() => confirmKey && deleteMutation.mutate(confirmKey)}
            >
              {deleteMutation.isPending ? t("删除中...") : t("确认删除")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={migrationPlan !== null} onOpenChange={(open) => !open && setMigrationPlan(null)}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{t("legacy 键迁移计划（")}{migrationCount}{t("条）")}</DialogTitle>
            <DialogDescription>
              {migrationPlan && migrationPlan.to_rewrite.length > 0
                ? t(`将重写 ${migrationPlan.to_rewrite.length} 条为规范键格式，${migrationPlan.skipped.length} 条需人工处理。`)
                : t("没有可自动重写的 legacy 键，全部需要人工处理。")}
            </DialogDescription>
          </DialogHeader>
          {migrationPlan && (
            <div className="max-h-72 space-y-3 overflow-y-auto">
              {migrationPlan.to_rewrite.map((change) => (
                <div key={change.old_key} className="rounded-lg border bg-muted/20 p-3 text-sm">
                  <div className="font-mono text-xs text-muted-foreground line-through">{change.old_key}</div>
                  <div className="font-mono text-xs">→ {change.new_key}</div>
                </div>
              ))}
              {migrationPlan.skipped.map((change) => (
                <div key={change.old_key} className="rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm dark:border-amber-500/30 dark:bg-amber-500/10">
                  <div className="font-mono text-xs">{change.old_key}</div>
                  <div className="mt-1 text-xs text-amber-600 dark:text-amber-400">{change.reason || t("无法解析")}</div>
                </div>
              ))}
            </div>
          )}
          <DialogFooter>
            <Button variant="outline" onClick={() => setMigrationPlan(null)}>{t("关闭")}</Button>
            {migrationPlan && migrationPlan.to_rewrite.length > 0 && (
              <Button onClick={() => migrateMutation.mutate(false)} disabled={migrateMutation.isPending}>
                {migrateMutation.isPending ? t("执行中...") : t("确认执行迁移")}
              </Button>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
