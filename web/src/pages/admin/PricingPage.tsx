import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Plus, Save, Trash2 } from 'lucide-react';
import { useMemo, useState } from 'react';
import { toast } from 'sonner';
import { EmptyState } from '@/components/EmptyState';
import { TableSkeleton } from '@/components/LoadingStates';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { adminApiClient } from '@/lib/api';
import { ensureApiSuccess, unwrapApiData } from '@/lib/api-response';

interface OptionItem {
  key: string;
  value: string;
}

interface PricingRow {
  id: string;
  model: string;
  modelRatio: string;
  completionRatio: string;
}

const MODEL_RATIO_KEY = 'ModelRatio';
const COMPLETION_RATIO_KEY = 'CompletionRatio';
function optionValue(options: OptionItem[] | undefined, key: string, fallback = '') {
  return options?.find((option) => option.key === key)?.value ?? fallback;
}

function parseRatioMap(value: string): Record<string, number> {
  if (!value.trim()) return {};
  try {
    const parsed = JSON.parse(value) as Record<string, unknown>;
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {};
    return Object.fromEntries(
      Object.entries(parsed)
        .map(([key, raw]) => [key.trim(), Number(raw)] as const)
        .filter(([key, raw]) => key && Number.isFinite(raw) && raw >= 0),
    );
  } catch {
    return {};
  }
}

function rowsFromMaps(modelRatio: Record<string, number>, completionRatio: Record<string, number>): PricingRow[] {
  const models = Array.from(new Set([...Object.keys(modelRatio), ...Object.keys(completionRatio)])).sort();
  return models.map((model) => ({
    id: model,
    model,
    modelRatio: String(modelRatio[model] ?? ''),
    completionRatio: String(completionRatio[model] ?? ''),
  }));
}

function ratioMapFromRows(rows: PricingRow[], field: 'modelRatio' | 'completionRatio') {
  const map: Record<string, number> = {};
  rows.forEach((row) => {
    const model = row.model.trim();
    const ratio = Number(row[field]);
    if (model && row[field].trim() !== '' && Number.isFinite(ratio) && ratio >= 0) {
      map[model] = ratio;
    }
  });
  return map;
}

function formatJSON(value: Record<string, number>) {
  const ordered: Record<string, number> = {};
  Object.keys(value)
    .sort()
    .forEach((key) => {
      ordered[key] = value[key];
    });
  return JSON.stringify(ordered);
}

function newRow(): PricingRow {
  return {
    id: `new-${Date.now()}-${Math.random().toString(36).slice(2)}`,
    model: '',
    modelRatio: '',
    completionRatio: '',
  };
}

export function AdminPricingPage() {
  const queryClient = useQueryClient();
  const [draftRows, setDraftRows] = useState<PricingRow[] | null>(null);

  const { data: options, isLoading } = useQuery({
    queryKey: ['admin-options'],
    queryFn: async () => {
      const res = await adminApiClient.get('/option/');
      return unwrapApiData<OptionItem[]>(res.data);
    },
  });

  const savedRows = useMemo(() => {
    const modelRatio = parseRatioMap(optionValue(options, MODEL_RATIO_KEY, '{}'));
    const completionRatio = parseRatioMap(optionValue(options, COMPLETION_RATIO_KEY, '{}'));
    return rowsFromMaps(modelRatio, completionRatio);
  }, [options]);

  const rows = draftRows ?? savedRows;

  const saveMutation = useMutation({
    mutationFn: async () => {
      const normalizedRows = rows.map((row) => ({
        ...row,
        model: row.model.trim(),
        modelRatio: row.modelRatio.trim(),
        completionRatio: row.completionRatio.trim(),
      }));
      const duplicate = normalizedRows.find(
        (row, index) => row.model && normalizedRows.findIndex((candidate) => candidate.model === row.model) !== index,
      );
      if (duplicate) {
        throw new Error(`Duplicate model: ${duplicate.model}`);
      }
      for (const row of normalizedRows) {
        if (!row.model && (row.modelRatio || row.completionRatio)) {
          throw new Error('Model name is required for every priced row');
        }
        for (const field of ['modelRatio', 'completionRatio'] as const) {
          if (row[field] !== '') {
            const parsed = Number(row[field]);
            if (!Number.isFinite(parsed) || parsed < 0) {
              throw new Error('Ratios must be non-negative numbers');
            }
          }
        }
      }
      const payloads: OptionItem[] = [
        { key: MODEL_RATIO_KEY, value: formatJSON(ratioMapFromRows(normalizedRows, 'modelRatio')) },
        { key: COMPLETION_RATIO_KEY, value: formatJSON(ratioMapFromRows(normalizedRows, 'completionRatio')) },
      ];
      await Promise.all(
        payloads.map(async (payload) => {
          const res = await adminApiClient.put('/option/', payload);
          ensureApiSuccess(res.data, `${payload.key} save failed`);
        }),
      );
    },
    onSuccess: () => {
      setDraftRows(null);
      queryClient.invalidateQueries({ queryKey: ['admin-options'] });
      queryClient.invalidateQueries({ queryKey: ['admin-summary'] });
      toast.success('Pricing saved');
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : 'Pricing save failed');
    },
  });

  const updateRow = (id: string, patch: Partial<PricingRow>) => {
    setDraftRows((current) => (current ?? savedRows).map((row) => (row.id === id ? { ...row, ...patch } : row)));
  };

  const addRow = () => {
    setDraftRows((current) => [...(current ?? savedRows), newRow()]);
  };

  const removeRow = (id: string) => {
    setDraftRows((current) => (current ?? savedRows).filter((row) => row.id !== id));
  };

  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h2 className="text-2xl font-semibold">模型价格</h2>
          <p className="mt-1 text-sm text-muted-foreground">
            配置 one-api 兼容的模型倍率、输出倍率和配额换算基数。
          </p>
        </div>
        <div className="flex gap-2">
          <Button variant="outline" onClick={addRow}>
            <Plus className="size-4" />
            添加模型
          </Button>
          <Button onClick={() => saveMutation.mutate()} disabled={saveMutation.isPending}>
            <Save className="size-4" />
            {saveMutation.isPending ? '保存中...' : '保存价格'}
          </Button>
        </div>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>模型倍率</CardTitle>
          <CardDescription>ModelRatio 控制基础消耗倍率，CompletionRatio 控制输出 token 倍率。</CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <TableSkeleton columns={['Model', 'ModelRatio', 'CompletionRatio', 'Actions']} rows={8} />
          ) : rows.length === 0 ? (
            <div className="space-y-4">
              <EmptyState title="暂无模型价格" description="添加模型后会保存到 ModelRatio 和 CompletionRatio。" />
              <Button variant="outline" onClick={addRow}>
                <Plus className="size-4" />
                添加模型
              </Button>
            </div>
          ) : (
            <div className="overflow-x-auto rounded-lg border">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Model</TableHead>
                    <TableHead>ModelRatio</TableHead>
                    <TableHead>CompletionRatio</TableHead>
                    <TableHead className="w-20 text-right">Actions</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {rows.map((row) => (
                    <TableRow key={row.id}>
                      <TableCell>
                        <Input
                          value={row.model}
                          onChange={(event) => updateRow(row.id, { model: event.target.value })}
                          placeholder="gpt-4o-mini"
                          className="min-w-64 font-mono"
                        />
                      </TableCell>
                      <TableCell>
                        <Input
                          type="number"
                          min="0"
                          step="0.0001"
                          value={row.modelRatio}
                          onChange={(event) => updateRow(row.id, { modelRatio: event.target.value })}
                          placeholder="0.15"
                          className="min-w-32"
                        />
                      </TableCell>
                      <TableCell>
                        <Input
                          type="number"
                          min="0"
                          step="0.0001"
                          value={row.completionRatio}
                          onChange={(event) => updateRow(row.id, { completionRatio: event.target.value })}
                          placeholder="1"
                          className="min-w-32"
                        />
                      </TableCell>
                      <TableCell className="text-right">
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon-sm"
                          aria-label={`删除 ${row.model || 'model row'}`}
                          onClick={() => removeRow(row.id)}
                        >
                          <Trash2 className="size-4" />
                        </Button>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
