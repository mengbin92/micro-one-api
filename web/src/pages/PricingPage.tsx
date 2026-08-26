import { useQuery } from '@tanstack/react-query';
import { Database } from 'lucide-react';
import { EmptyState } from '@/components/EmptyState';
import { TableSkeleton } from '@/components/LoadingStates';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { apiClient } from '@/lib/api';
import { unwrapApiData } from '@/lib/api-response';

interface PricingRow {
  model: string;
  input_price?: number;
  output_price?: number;
  cache_read_price?: number;
}

interface PricingPayload {
  prices?: PricingRow[];
  unit?: string;
}

function normalizePrice(value: unknown) {
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : undefined;
}

function normalizeRows(rows: PricingRow[] | undefined) {
  if (!Array.isArray(rows)) return [];
  return rows
    .map((row) => ({
      model: String(row.model || '').trim(),
      input_price: normalizePrice(row.input_price),
      output_price: normalizePrice(row.output_price),
      cache_read_price: normalizePrice(row.cache_read_price),
    }))
    .filter((row) => row.model)
    .sort((a, b) => a.model.localeCompare(b.model));
}

function formatPrice(value: number | undefined, unit: string) {
  if (value === undefined) return '-';
  return `$${value.toLocaleString(undefined, {
    minimumFractionDigits: 2,
    maximumFractionDigits: 6,
  })} / ${unit}`;
}

export function PricingPage() {
  const { data, isLoading } = useQuery({
    queryKey: ['readonly-pricing'],
    queryFn: async () => {
      const res = await apiClient.get('/pricing');
      return unwrapApiData<PricingPayload>(res.data);
    },
  });

  const unit = data?.unit || '1M tokens';
  const rows = normalizeRows(data?.prices);

  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h2 className="text-2xl font-black tracking-normal text-slate-950 dark:text-white">模型价格</h2>
          <p className="mt-1 text-sm font-medium text-slate-500 dark:text-slate-400">
            当前可用模型的输入、输出和缓存读取价格。
          </p>
        </div>
        <div className="inline-flex w-fit items-center gap-2 rounded-xl border border-border bg-card px-3 py-2 text-sm font-bold text-foreground shadow-surface-sm">
          <Database className="size-4 text-blue-600" />
          {rows.length.toLocaleString()} 个模型
        </div>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>当前价格</CardTitle>
          <CardDescription>价格单位为每 {unit}。</CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <TableSkeleton columns={['模型名称', '输入价格', '输出价格', '缓存读取']} rows={8} />
          ) : rows.length === 0 ? (
            <EmptyState title="暂无模型价格" description="管理员配置价格后会显示在这里。" />
          ) : (
            <div className="overflow-x-auto rounded-lg border">
              <Table>
                <TableHeader>
                  <TableRow className="bg-slate-50 hover:bg-slate-50 dark:bg-white/5">
                    <TableHead>模型名称</TableHead>
                    <TableHead>输入价格</TableHead>
                    <TableHead>输出价格</TableHead>
                    <TableHead>缓存读取</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {rows.map((row) => (
                    <TableRow key={row.model}>
                      <TableCell className="min-w-64 font-mono text-sm font-semibold">{row.model}</TableCell>
                      <TableCell className="min-w-44 font-semibold">{formatPrice(row.input_price, unit)}</TableCell>
                      <TableCell className="min-w-44 font-semibold">{formatPrice(row.output_price, unit)}</TableCell>
                      <TableCell className="min-w-44 font-semibold">{formatPrice(row.cache_read_price, unit)}</TableCell>
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
