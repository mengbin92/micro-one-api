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
import { AMOUNT_SCALE } from '@/lib/amount';

interface OptionItem {
  key: string;
  value: string;
}

interface PricingRow {
  id: string;
  model: string;
  inputPrice: string;
  outputPrice: string;
  cacheReadPrice: string;
}

interface ModelPrice {
  input_price?: number;
  output_price?: number;
  cache_read_price?: number;
}

const MODEL_RATIO_KEY = 'ModelRatio';
const COMPLETION_RATIO_KEY = 'CompletionRatio';
const MODEL_PRICE_KEY = 'ModelPrice';
const QUOTA_PER_UNIT_KEY = 'QuotaPerUnit';
const DEFAULT_QUOTA_PER_UNIT = AMOUNT_SCALE;
const MTOK = 1_000_000;

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

function parseModelPriceMap(value: string): Record<string, ModelPrice> {
  if (!value.trim()) return {};
  try {
    const parsed = JSON.parse(value) as Record<string, unknown>;
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {};
    const result: Record<string, ModelPrice> = {};
    Object.entries(parsed).forEach(([model, raw]) => {
      const name = model.trim();
      if (!name || !raw || typeof raw !== 'object' || Array.isArray(raw)) return;
      const price = raw as Record<string, unknown>;
      const inputPrice = Number(price.input_price);
      const outputPrice = Number(price.output_price);
      const cacheReadPrice = Number(price.cache_read_price);
      result[name] = {
        input_price: Number.isFinite(inputPrice) && inputPrice >= 0 ? inputPrice : undefined,
        output_price: Number.isFinite(outputPrice) && outputPrice >= 0 ? outputPrice : undefined,
        cache_read_price: Number.isFinite(cacheReadPrice) && cacheReadPrice >= 0 ? cacheReadPrice : undefined,
      };
    });
    return result;
  } catch {
    return {};
  }
}

function quotaPerUnitFromOptions(options: OptionItem[] | undefined) {
  const parsed = Number(optionValue(options, QUOTA_PER_UNIT_KEY, String(DEFAULT_QUOTA_PER_UNIT)));
  return Number.isFinite(parsed) && parsed > 0 ? parsed : DEFAULT_QUOTA_PER_UNIT;
}

function perTokenToMTok(value: number | undefined) {
  if (value === undefined) return '';
  return String(Number((value * MTOK).toPrecision(10)));
}

function mTokToPerToken(value: string) {
  const parsed = Number(value);
  if (!Number.isFinite(parsed) || parsed < 0) return undefined;
  return Number((parsed / MTOK).toPrecision(10));
}

function ratioToMTokPrice(ratio: number | undefined, quotaPerUnit: number) {
  if (ratio === undefined) return '';
  return String(Number(((ratio / quotaPerUnit) * MTOK).toPrecision(10)));
}

function rowsFromPricing(
  modelPrice: Record<string, ModelPrice>,
  modelRatio: Record<string, number>,
  completionRatio: Record<string, number>,
  quotaPerUnit: number,
): PricingRow[] {
  const models = Array.from(
    new Set([...Object.keys(modelPrice), ...Object.keys(modelRatio), ...Object.keys(completionRatio)]),
  ).sort();
  return models.map((model) => ({
    id: model,
    model,
    inputPrice:
      modelPrice[model]?.input_price !== undefined
        ? perTokenToMTok(modelPrice[model].input_price)
        : ratioToMTokPrice(modelRatio[model], quotaPerUnit),
    outputPrice:
      modelPrice[model]?.output_price !== undefined
        ? perTokenToMTok(modelPrice[model].output_price)
        : ratioToMTokPrice(
            modelRatio[model] === undefined ? undefined : modelRatio[model] * (completionRatio[model] ?? 1),
            quotaPerUnit,
          ),
    cacheReadPrice:
      modelPrice[model]?.cache_read_price !== undefined ? perTokenToMTok(modelPrice[model].cache_read_price) : '',
  }));
}

function modelPriceMapFromRows(rows: PricingRow[]) {
  const map: Record<string, ModelPrice> = {};
  rows.forEach((row) => {
    const model = row.model.trim();
    if (!model) return;
    const price: ModelPrice = {};
    const inputPrice = mTokToPerToken(row.inputPrice);
    const outputPrice = mTokToPerToken(row.outputPrice);
    const cacheReadPrice = mTokToPerToken(row.cacheReadPrice);
    if (inputPrice !== undefined) price.input_price = inputPrice;
    if (outputPrice !== undefined) price.output_price = outputPrice;
    if (cacheReadPrice !== undefined) price.cache_read_price = cacheReadPrice;
    if (Object.keys(price).length > 0) {
      map[model] = price;
    }
  });
  return map;
}

function legacyRatioMapsFromRows(rows: PricingRow[], quotaPerUnit: number) {
  const modelRatio: Record<string, number> = {};
  const completionRatio: Record<string, number> = {};
  rows.forEach((row) => {
    const model = row.model.trim();
    const inputPrice = mTokToPerToken(row.inputPrice);
    const outputPrice = mTokToPerToken(row.outputPrice);
    if (!model || inputPrice === undefined || inputPrice <= 0) return;
    modelRatio[model] = Number((inputPrice * quotaPerUnit).toPrecision(10));
    if (outputPrice !== undefined) {
      completionRatio[model] = Number((outputPrice / inputPrice).toPrecision(10));
    }
  });
  return { modelRatio, completionRatio };
}

function formatJSON<T>(value: Record<string, T>) {
  const ordered: Record<string, T> = {};
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
    inputPrice: '',
    outputPrice: '',
    cacheReadPrice: '',
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
    const modelPrice = parseModelPriceMap(optionValue(options, MODEL_PRICE_KEY, '{}'));
    const modelRatio = parseRatioMap(optionValue(options, MODEL_RATIO_KEY, '{}'));
    const completionRatio = parseRatioMap(optionValue(options, COMPLETION_RATIO_KEY, '{}'));
    return rowsFromPricing(modelPrice, modelRatio, completionRatio, quotaPerUnitFromOptions(options));
  }, [options]);

  const rows = draftRows ?? savedRows;

  const saveMutation = useMutation({
    mutationFn: async () => {
      const normalizedRows = rows.map((row) => ({
        ...row,
        model: row.model.trim(),
        inputPrice: row.inputPrice.trim(),
        outputPrice: row.outputPrice.trim(),
        cacheReadPrice: row.cacheReadPrice.trim(),
      }));
      const duplicate = normalizedRows.find(
        (row, index) => row.model && normalizedRows.findIndex((candidate) => candidate.model === row.model) !== index,
      );
      if (duplicate) {
        throw new Error(`Duplicate model: ${duplicate.model}`);
      }
      for (const row of normalizedRows) {
        if (!row.model && (row.inputPrice || row.outputPrice || row.cacheReadPrice)) {
          throw new Error('Model name is required for every priced row');
        }
        for (const field of ['inputPrice', 'outputPrice', 'cacheReadPrice'] as const) {
          if (row[field] !== '') {
            const parsed = Number(row[field]);
            if (!Number.isFinite(parsed) || parsed < 0) {
              throw new Error('Prices must be non-negative numbers');
            }
          }
        }
      }
      const quotaPerUnit = quotaPerUnitFromOptions(options);
      const legacyRatios = legacyRatioMapsFromRows(normalizedRows, quotaPerUnit);
      const payloads: OptionItem[] = [
        { key: MODEL_PRICE_KEY, value: formatJSON(modelPriceMapFromRows(normalizedRows)) },
        { key: MODEL_RATIO_KEY, value: formatJSON(legacyRatios.modelRatio) },
        { key: COMPLETION_RATIO_KEY, value: formatJSON(legacyRatios.completionRatio) },
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
            按每 1M tokens 配置模型输入、输出和缓存读取价格。
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
          <CardTitle>当前价格</CardTitle>
          <CardDescription>价格按每 1M tokens 录入，保存后用于按输入和输出 token 独立计费。</CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <TableSkeleton columns={['模型名称', '输入价格', '输出价格', '缓存读取', '操作']} rows={8} />
          ) : rows.length === 0 ? (
            <div className="space-y-4">
              <EmptyState title="暂无模型价格" description="添加模型后会保存输入、输出和缓存读取价格。" />
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
                    <TableHead>模型名称</TableHead>
                    <TableHead>输入价格</TableHead>
                    <TableHead>输出价格</TableHead>
                    <TableHead>缓存读取</TableHead>
                    <TableHead className="w-20 text-right">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {rows.map((row) => (
                    <TableRow key={row.id}>
                      <TableCell>
                        <Input
                          value={row.model}
                          onChange={(event) => updateRow(row.id, { model: event.target.value })}
                          placeholder="gpt-5.5"
                          className="min-w-64 font-mono"
                        />
                      </TableCell>
                      <TableCell>
                        <Input
                          type="number"
                          min="0"
                          step="0.000001"
                          value={row.inputPrice}
                          onChange={(event) => updateRow(row.id, { inputPrice: event.target.value })}
                          placeholder="0.65"
                          className="min-w-32"
                        />
                      </TableCell>
                      <TableCell>
                        <Input
                          type="number"
                          min="0"
                          step="0.000001"
                          value={row.outputPrice}
                          onChange={(event) => updateRow(row.id, { outputPrice: event.target.value })}
                          placeholder="3.90"
                          className="min-w-32"
                        />
                      </TableCell>
                      <TableCell>
                        <Input
                          type="number"
                          min="0"
                          step="0.000001"
                          value={row.cacheReadPrice}
                          onChange={(event) => updateRow(row.id, { cacheReadPrice: event.target.value })}
                          placeholder="0.065"
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
