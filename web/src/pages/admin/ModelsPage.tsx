import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Eye, Pencil, Plus, Trash2 } from 'lucide-react';
import { useMemo, useState } from 'react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { EmptyState } from '@/components/EmptyState';
import { TableSkeleton } from '@/components/LoadingStates';
import { AdminPagination } from '@/components/admin/AdminPagination';
import { AdminTableToolbar } from '@/components/admin/AdminTableToolbar';
import { SortableHeader } from '@/components/admin/SortableHeader';
import { ModelDraftFields } from '@/components/admin/ModelDraftFields';
import {
  emptyDraft,
  PROVIDER_OPTIONS,
  TYPE_OPTIONS,
  STATUS_OPTIONS,
  CATEGORY_OPTIONS,
  TIER_OPTIONS,
  splitCsv,
  validateMetadata,
  type ModelDraft,
} from '@/lib/model-draft';
import { useAdminTableState } from '@/hooks/useAdminTableState';
import { buildAdminListParams } from '@/lib/admin-table-query';
import { sortRows, type SortState } from '@/lib/table-utils';
import {
  listModels,
  getModel,
  createModel,
  updateModel,
  deleteModel,
  changeModelStatus,
  batchModels,
  MODEL_STATUS_LABELS,
  statusBadgeClass,
  type ModelSummary,
  type CreateModelPayload,
  type UpdateModelPayload,
} from '@/lib/model-management';
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
} from '@/components/ui/dialog';
import { ModelDetailPanel } from './ModelDetailPanel';

function draftToCreatePayload(draft: ModelDraft): CreateModelPayload {
  return {
    model_id: draft.modelId.trim(),
    display_name: draft.displayName.trim(),
    description: draft.description.trim(),
    provider: draft.provider,
    model_type: draft.modelType,
    context_window: draft.contextWindow ? Number(draft.contextWindow) : 0,
    pricing_input: draft.pricingInput ? Number(draft.pricingInput) : 0,
    pricing_output: draft.pricingOutput ? Number(draft.pricingOutput) : 0,
    status: 1,
    is_public: draft.isPublic,
    capabilities: splitCsv(draft.capabilities),
    tags: splitCsv(draft.tags),
    category: draft.category,
    tier: draft.tier,
    metadata: draft.metadata,
  };
}

function draftToUpdatePayload(modelPk: number, draft: ModelDraft): UpdateModelPayload {
  return {
    model_pk: modelPk,
    display_name: draft.displayName.trim(),
    description: draft.description.trim(),
    provider: draft.provider,
    model_type: draft.modelType,
    context_window: draft.contextWindow ? Number(draft.contextWindow) : 0,
    pricing_input: draft.pricingInput ? Number(draft.pricingInput) : 0,
    pricing_output: draft.pricingOutput ? Number(draft.pricingOutput) : 0,
    is_public: draft.isPublic,
    capabilities: splitCsv(draft.capabilities),
    tags: splitCsv(draft.tags),
    category: draft.category,
    tier: draft.tier,
    metadata: draft.metadata,
  };
}

interface ModelInfoLike {
  model_id: string;
  display_name: string;
  description: string;
  provider: string;
  model_type: string;
  context_window: number;
  pricing_input: number;
  pricing_output: number;
  is_public: boolean;
  capabilities: string[];
  tags: string[];
  category: string;
  tier: string;
  metadata: string;
}

function modelInfoToDraft(model: ModelInfoLike): ModelDraft {
  return {
    modelId: model.model_id,
    displayName: model.display_name,
    description: model.description ?? '',
    provider: model.provider ?? '',
    modelType: model.model_type ?? 'chat',
    contextWindow: model.context_window ? String(model.context_window) : '',
    pricingInput: model.pricing_input ? String(model.pricing_input) : '',
    pricingOutput: model.pricing_output ? String(model.pricing_output) : '',
    category: model.category ?? '',
    tier: model.tier ?? '',
    isPublic: model.is_public,
    capabilities: (model.capabilities ?? []).join(', '),
    tags: (model.tags ?? []).join(', '),
    metadata: model.metadata ?? '',
  };
}

export function AdminModelsPage() {
  const {
    page,
    pageSize,
    search,
    sortKey,
    sortDirection,
    filters,
    setPage,
    setPageSize,
    setSearch,
    clearSearch,
    setSort,
    setFilter,
  } = useAdminTableState({
    storageKey: 'models',
    filters: ['status', 'model_type', 'provider', 'category', 'tier'],
  });

  const [isCreateOpen, setIsCreateOpen] = useState(false);
  const [createDraft, setCreateDraft] = useState<ModelDraft>(emptyDraft);
  const [editingModel, setEditingModel] = useState<{ pk: number; draft: ModelDraft } | null>(null);
  const [editLoading, setEditLoading] = useState(false);
  const [detailModelPk, setDetailModelPk] = useState<number | null>(null);
  const [selectedPks, setSelectedPks] = useState<Set<number>>(new Set());
  const [confirmState, setConfirmState] = useState<{
    title: string;
    message: string;
    onConfirm: () => void;
  } | null>(null);

  const queryClient = useQueryClient();
  const invalidateModels = () => queryClient.invalidateQueries({ queryKey: ['admin-models'] });

  const sort = {
    key: sortKey as keyof ModelSummary | null,
    direction: sortDirection,
  } satisfies SortState<ModelSummary>;
  const statusFilter = filters.status ?? '';
  const typeFilter = filters.model_type ?? '';
  const providerFilter = filters.provider ?? '';
  const categoryFilter = filters.category ?? '';
  const tierFilter = filters.tier ?? '';

  const { data, isLoading } = useQuery({
    queryKey: ['admin-models', page, pageSize, search, sortKey, sortDirection, filters],
    queryFn: async () => {
      const params = buildAdminListParams({ page, pageSize, search, sortKey, sortDirection, filters });
      return listModels({
        page: Number(params.get('page') || 1),
        page_size: Number(params.get('page_size') || 20),
        keyword: params.get('keyword') || undefined,
        status: params.get('status') ? Number(params.get('status')) : undefined,
        model_type: params.get('model_type') || undefined,
        provider: params.get('provider') || undefined,
        category: params.get('category') || undefined,
        tier: params.get('tier') || undefined,
      });
    },
  });

  const total = data?.total ?? 0;
  const sortedModels = useMemo(() => sortRows(data?.models ?? [], sort), [data, sort]);

  const createMutation = useMutation({
    mutationFn: (payload: CreateModelPayload) => createModel(payload),
    onSuccess: (resp) => {
      if (!resp.success) { toast.error(resp.message || '创建失败'); return; }
      toast.success('模型已创建');
      setIsCreateOpen(false);
      invalidateModels();
    },
    onError: (err: unknown) => toast.error((err as Error).message || '创建失败'),
  });

  const updateMutation = useMutation({
    mutationFn: (payload: UpdateModelPayload) => updateModel(payload),
    onSuccess: (resp) => {
      if (!resp.success) { toast.error(resp.message || '更新失败'); return; }
      toast.success('模型已更新');
      setEditingModel(null);
      invalidateModels();
    },
    onError: (err: unknown) => toast.error((err as Error).message || '更新失败'),
  });

  const deleteMutation = useMutation({
    mutationFn: (pk: number) => deleteModel(pk),
    onSuccess: (resp) => {
      if (!resp.success) { toast.error(resp.message || '删除失败'); return; }
      toast.success('模型已删除');
      invalidateModels();
    },
    onError: (err: unknown) => toast.error((err as Error).message || '删除失败'),
  });

  const toggleStatusMutation = useMutation({
    mutationFn: ({ pk, status }: { pk: number; status: number }) => changeModelStatus(pk, status),
    onSuccess: (resp) => {
      if (!resp.success) { toast.error(resp.message || '状态更新失败'); return; }
      toast.success('状态已更新');
      invalidateModels();
    },
    onError: (err: unknown) => toast.error((err as Error).message || '状态更新失败'),
  });

  const batchMutation = useMutation({
    mutationFn: (payload: { action: 'enable' | 'disable' | 'delete'; model_pks: number[] }) =>
      batchModels(payload),
    onSuccess: (resp) => {
      if (!resp.success) { toast.error(resp.message || '批量操作失败'); return; }
      toast.success('已处理 ' + resp.affected + ' 个模型');
      setSelectedPks(new Set());
      invalidateModels();
    },
    onError: (err: unknown) => toast.error((err as Error).message || '批量操作失败'),
  });

  const handleCreate = () => {
    if (!createDraft.modelId.trim() || !createDraft.displayName.trim()) {
      toast.error('模型 ID 和显示名称不能为空');
      return;
    }
    const metadataError = validateMetadata(createDraft.metadata);
    if (metadataError) { toast.error(metadataError); return; }
    createMutation.mutate(draftToCreatePayload(createDraft));
  };

  const handleUpdate = () => {
    if (!editingModel) return;
    const metadataError = validateMetadata(editingModel.draft.metadata);
    if (metadataError) { toast.error(metadataError); return; }
    updateMutation.mutate(draftToUpdatePayload(editingModel.pk, editingModel.draft));
  };

  const handleDelete = (pk: number) => {
    setConfirmState({
      title: '删除模型',
      message: '确认删除此模型？相关映射将一并删除。',
      onConfirm: () => { deleteMutation.mutate(pk); setConfirmState(null); },
    });
  };

  const toggleSelect = (pk: number) => {
    setSelectedPks((prev) => {
      const next = new Set(prev);
      if (next.has(pk)) next.delete(pk);
      else next.add(pk);
      return next;
    });
  };

  const toggleSelectAll = () => {
    if (selectedPks.size === sortedModels.length) {
      setSelectedPks(new Set());
    } else {
      setSelectedPks(new Set(sortedModels.map((m) => m.id)));
    }
  };

  const handleBatch = (action: 'enable' | 'disable' | 'delete') => {
    if (selectedPks.size === 0) { toast.error('请先选择模型'); return; }
    if (action === 'delete') {
      setConfirmState({
        title: '批量删除',
        message: '确认批量删除 ' + selectedPks.size + ' 个模型？',
        onConfirm: () => {
          batchMutation.mutate({ action, model_pks: [...selectedPks] });
          setConfirmState(null);
        },
      });
      return;
    }
    batchMutation.mutate({ action, model_pks: [...selectedPks] });
  };

  const openEdit = async (model: ModelSummary) => {
    setEditLoading(true);
    setEditingModel({ pk: model.id, draft: emptyDraft });
    try {
      const detail = await getModel(model.id);
      setEditingModel({ pk: model.id, draft: modelInfoToDraft(detail.model) });
    } catch (err) {
      toast.error((err as Error).message || '加载模型详情失败');
      setEditingModel(null);
    } finally {
      setEditLoading(false);
    }
  };

  const updateEditDraft = (patch: Partial<ModelDraft>) =>
    setEditingModel((prev) => (prev ? { ...prev, draft: { ...prev.draft, ...patch } } : prev));

  return (
    <div className="space-y-6 p-6">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-2xl font-bold">模型管理</h2>
          <p className="text-sm text-muted-foreground">
            统一管理所有可用模型，支持启用/禁用、分组和映射
          </p>
        </div>
        <Button onClick={() => { setCreateDraft(emptyDraft); setIsCreateOpen(true); }}>
          <Plus className="size-4" />
          新建模型
        </Button>
      </div>

      <AdminTableToolbar
        search={search}
        searchPlaceholder="搜索模型 ID 或名称…"
        onSearchChange={setSearch}
        onClear={clearSearch}
        actions={
          <div className="flex items-center gap-2">
            <select
              value={statusFilter}
              onChange={(e) => setFilter('status', e.target.value)}
              className="h-8 rounded-lg border border-input bg-transparent px-2.5 text-sm"
              aria-label="状态筛选"
            >
              {STATUS_OPTIONS.map((o) => (
                <option key={o.value} value={o.value}>{o.label}</option>
              ))}
            </select>
            <select
              value={typeFilter}
              onChange={(e) => setFilter('model_type', e.target.value)}
              className="h-8 rounded-lg border border-input bg-transparent px-2.5 text-sm"
              aria-label="类型筛选"
            >
              {TYPE_OPTIONS.map((o) => (
                <option key={o.value} value={o.value}>{o.label}</option>
              ))}
            </select>
            <select
              value={providerFilter}
              onChange={(e) => setFilter('provider', e.target.value)}
              className="h-8 rounded-lg border border-input bg-transparent px-2.5 text-sm"
              aria-label="提供商筛选"
            >
              <option value="">全部提供商</option>
              {PROVIDER_OPTIONS.filter((o) => o.value).map((o) => (
                <option key={o.value} value={o.value}>{o.label}</option>
              ))}
            </select>
            <select
              value={categoryFilter}
              onChange={(e) => setFilter('category', e.target.value)}
              className="h-8 rounded-lg border border-input bg-transparent px-2.5 text-sm"
              aria-label="分类筛选"
            >
              {CATEGORY_OPTIONS.map((o) => (
                <option key={o.value} value={o.value}>{o.label}</option>
              ))}
            </select>
            <select
              value={tierFilter}
              onChange={(e) => setFilter('tier', e.target.value)}
              className="h-8 rounded-lg border border-input bg-transparent px-2.5 text-sm"
              aria-label="等级筛选"
            >
              <option value="">全部等级</option>
              {TIER_OPTIONS.filter((o) => o.value).map((o) => (
                <option key={o.value} value={o.value}>{o.label}</option>
              ))}
            </select>
            {selectedPks.size > 0 && (
              <div className="flex items-center gap-2">
                <span className="text-sm text-muted-foreground">已选 {selectedPks.size} 个</span>
                <Button variant="outline" size="sm" onClick={() => handleBatch('enable')}>启用</Button>
                <Button variant="outline" size="sm" onClick={() => handleBatch('disable')}>禁用</Button>
                <Button variant="outline" size="sm" onClick={() => handleBatch('delete')}>
                  <Trash2 className="size-3.5" />
                  删除
                </Button>
              </div>
            )}
          </div>
        }
      />

      {isLoading ? (
        <TableSkeleton columns={['ID', '模型', '提供商', '类型', '状态', '渠道', '订阅', '操作']} />
      ) : sortedModels.length === 0 ? (
        <EmptyState title="暂无模型" description="点击右上角新建模型开始管理" />
      ) : (
        <>
          <div className="overflow-x-auto rounded-lg border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-10">
                    <input
                      type="checkbox"
                      checked={selectedPks.size === sortedModels.length && sortedModels.length > 0}
                      onChange={toggleSelectAll}
                      className="size-4 rounded border-input"
                      aria-label="全选"
                    />
                  </TableHead>
                  <SortableHeader<ModelSummary> columnKey="id" sort={sort} onSortChange={setSort} className="font-mono text-sm">
                    ID
                  </SortableHeader>
                  <SortableHeader<ModelSummary> columnKey="model_id" sort={sort} onSortChange={setSort}>
                    模型 ID
                  </SortableHeader>
                  <SortableHeader<ModelSummary> columnKey="display_name" sort={sort} onSortChange={setSort}>
                    显示名称
                  </SortableHeader>
                  <SortableHeader<ModelSummary> columnKey="provider" sort={sort} onSortChange={setSort}>
                    提供商
                  </SortableHeader>
                  <SortableHeader<ModelSummary> columnKey="model_type" sort={sort} onSortChange={setSort} className="hidden md:table-cell">
                    类型
                  </SortableHeader>
                  <SortableHeader<ModelSummary> columnKey="status" sort={sort} onSortChange={setSort}>
                    状态
                  </SortableHeader>
                  <TableHead className="hidden lg:table-cell">渠道数</TableHead>
                  <TableHead className="hidden lg:table-cell">订阅数</TableHead>
                  <TableHead className="text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {sortedModels.map((m) => (
                  <TableRow key={m.id}>
                    <TableCell>
                      <input
                        type="checkbox"
                        checked={selectedPks.has(m.id)}
                        onChange={() => toggleSelect(m.id)}
                        className="size-4 rounded border-input"
                        aria-label={'选择 ' + m.model_id}
                      />
                    </TableCell>
                    <TableCell className="font-mono text-sm">{m.id}</TableCell>
                    <TableCell className="font-mono text-sm">{m.model_id}</TableCell>
                    <TableCell className="font-medium">{m.display_name}</TableCell>
                    <TableCell>{m.provider || '—'}</TableCell>
                    <TableCell className="hidden md:table-cell">{m.model_type || '—'}</TableCell>
                    <TableCell>
                      <span className={'inline-flex items-center px-2 py-1 rounded-full text-xs font-medium ' + statusBadgeClass(m.status)}>
                        {MODEL_STATUS_LABELS[m.status] ?? String(m.status)}
                      </span>
                    </TableCell>
                    <TableCell className="hidden lg:table-cell">{m.channel_count}</TableCell>
                    <TableCell className="hidden lg:table-cell">{m.subscription_count}</TableCell>
                    <TableCell className="text-right space-x-2">
                      <Button variant="outline" size="sm" onClick={() => setDetailModelPk(m.id)}>
                        <Eye className="size-3.5" />
                        详情
                      </Button>
                      <Button variant="outline" size="sm" onClick={() => openEdit(m)} disabled={editLoading}>
                        <Pencil className="size-3.5" />
                        编辑
                      </Button>
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => toggleStatusMutation.mutate({ pk: m.id, status: m.status === 1 ? 0 : 1 })}
                        disabled={toggleStatusMutation.isPending}
                      >
                        {m.status === 1 ? '禁用' : '启用'}
                      </Button>
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => handleDelete(m.id)}
                        disabled={deleteMutation.isPending}
                      >
                        <Trash2 className="size-3.5" />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>

          <AdminPagination
            page={page}
            pageSize={pageSize}
            hasNextPage={page * pageSize < total}
            onPageChange={setPage}
            onPageSizeChange={setPageSize}
          />
        </>
      )}

      {/* Create dialog */}
      <Dialog open={isCreateOpen} onOpenChange={setIsCreateOpen}>
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>新建模型</DialogTitle>
            <DialogDescription>填写模型信息创建新的模型记录</DialogDescription>
          </DialogHeader>
          <ModelDraftFields draft={createDraft} onChange={(patch) => setCreateDraft((prev) => ({ ...prev, ...patch }))} />
          <DialogFooter>
            <Button variant="outline" onClick={() => setIsCreateOpen(false)}>取消</Button>
            <Button onClick={handleCreate} disabled={createMutation.isPending}>
              {createMutation.isPending ? '创建中…' : '创建'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Edit dialog */}
      <Dialog open={!!editingModel} onOpenChange={(open) => { if (!open) setEditingModel(null); }}>
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>编辑模型</DialogTitle>
            <DialogDescription>{editingModel?.draft.modelId}</DialogDescription>
          </DialogHeader>
          {editingModel && (
            <ModelDraftFields draft={editingModel.draft} onChange={updateEditDraft} isEdit />
          )}
          <DialogFooter>
            <Button variant="outline" onClick={() => setEditingModel(null)}>取消</Button>
            <Button onClick={handleUpdate} disabled={updateMutation.isPending || editLoading}>
              {editLoading ? '加载中…' : updateMutation.isPending ? '保存中…' : '保存'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Confirm dialog */}
      <Dialog open={!!confirmState} onOpenChange={(open) => { if (!open) setConfirmState(null); }}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{confirmState?.title ?? '确认'}</DialogTitle>
            <DialogDescription>{confirmState?.message ?? ''}</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setConfirmState(null)}>取消</Button>
            <Button variant="destructive" onClick={() => confirmState?.onConfirm()}>确认</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Detail panel */}
      <ModelDetailPanel modelPk={detailModelPk} onClose={() => setDetailModelPk(null)} />
    </div>
  );
}
