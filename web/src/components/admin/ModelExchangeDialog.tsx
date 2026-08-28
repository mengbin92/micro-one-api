// Model import/export dialog (v0.11.0 Phase 4).
//
// Provides three flows:
//   1. Export — download a versioned JSON document of the model registry.
//   2. Import dry-run — upload a file and preview the create/update/skip/conflict diff.
//   3. Import apply — after confirming the dry-run, write the batch in one transaction.
//
// Prices are gated behind an explicit checkbox and require root role server-side.
// The dialog reuses the existing Dialog/Button/Input components so it matches
// the rest of the admin UI.

import { Download, Upload, FileJson, AlertTriangle, CheckCircle2, Loader2 } from 'lucide-react';
import { useCallback, useRef, useState } from 'react';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import {
  exportModels,
  dryRunImportModels,
  importModels,
  parseImportFile,
  downloadExportDocument,
  MODEL_EXCHANGE_SCHEMA_VERSION,
  type ModelExportDocument,
  type ImportModelsDryRunResponse,
  type ImportModelsResponse,
} from '@/lib/model-exchange';
import { t } from '@/lib/i18n';

type Mode = 'export' | 'import';

interface ModelExchangeDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Called after a successful import so the parent can refetch the list. */
  onImported?: () => void;
}

export function ModelExchangeDialog({ open, onOpenChange, onImported }: ModelExchangeDialogProps) {
  const [mode, setMode] = useState<Mode>('export');

  // Export state
  const [exportPrices, setExportPrices] = useState(false);
  const [exporting, setExporting] = useState(false);

  // Import state
  const [fileName, setFileName] = useState('');
  const [parsedDoc, setParsedDoc] = useState<ModelExportDocument | null>(null);
  const [parseError, setParseError] = useState('');
  const [conflictStrategy, setConflictStrategy] = useState<'reject' | 'update'>('reject');
  const [importPrices, setImportPrices] = useState(false);
  const [dryRunResult, setDryRunResult] = useState<ImportModelsDryRunResponse | null>(null);
  const [importResult, setImportResult] = useState<ImportModelsResponse | null>(null);
  const [busy, setBusy] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const resetImportState = useCallback(() => {
    setFileName('');
    setParsedDoc(null);
    setParseError('');
    setDryRunResult(null);
    setImportResult(null);
    setConflictStrategy('reject');
    setImportPrices(false);
  }, []);

  const handleModeSwitch = (newMode: Mode) => {
    setMode(newMode);
    resetImportState();
  };

  const handleExport = async () => {
    setExporting(true);
    try {
      const doc = await exportModels({ export_prices: exportPrices });
      const stamp = new Date().toISOString().slice(0, 19).replace(/[:T]/g, '-');
      downloadExportDocument(doc, `model-export-${stamp}.json`);
      toast.success(t(`已导出 ${doc.models.length} 个模型`));
    } catch (err) {
      toast.error((err as Error).message || t("导出失败"));
    } finally {
      setExporting(false);
    }
  };

  const handleFileSelect = (file: File) => {
    setParseError('');
    setDryRunResult(null);
    setImportResult(null);
    setFileName(file.name);
    const reader = new FileReader();
    reader.onload = () => {
      try {
        const doc = parseImportFile(String(reader.result));
        setParsedDoc(doc);
        toast.success(t(`已加载 ${doc.models.length} 个模型记录`));
      } catch (err) {
        setParsedDoc(null);
        setParseError((err as Error).message || t("文件解析失败"));
      }
    };
    reader.onerror = () => setParseError(t("文件读取失败"));
    reader.readAsText(file);
  };

  const handleDryRun = async () => {
    if (!parsedDoc) return;
    setBusy(true);
    try {
      const result = await dryRunImportModels({
        schema_version: parsedDoc.schema_version,
        models: parsedDoc.models,
        conflict_strategy: conflictStrategy,
        import_prices: importPrices,
      });
      setDryRunResult(result);
      if (result.conflicts > 0 || result.errors > 0) {
        toast.warning(t(`预检完成：${result.conflicts} 个冲突，${result.errors} 个错误`));
      } else {
        toast.success(t(`预检通过：新增 ${result.would_create}，更新 ${result.would_update}，跳过 ${result.would_skip}`));
      }
    } catch (err) {
      toast.error((err as Error).message || t("预检失败"));
    } finally {
      setBusy(false);
    }
  };

  const handleImport = async () => {
    if (!parsedDoc) return;
    setBusy(true);
    try {
      const result = await importModels({
        schema_version: parsedDoc.schema_version,
        models: parsedDoc.models,
        conflict_strategy: conflictStrategy,
        import_prices: importPrices,
      });
      setImportResult(result);
      if (result.success) {
        toast.success(t(`导入完成：新增 ${result.created}，更新 ${result.updated}，跳过 ${result.skipped}`));
        onImported?.();
      } else {
        toast.error(t(`导入完成但有冲突：${result.conflicts} 个冲突，${result.errors} 个错误`));
      }
    } catch (err) {
      toast.error((err as Error).message || t("导入失败"));
    } finally {
      setBusy(false);
    }
  };

  const canDryRun = parsedDoc !== null && !busy;
  const canImport = parsedDoc !== null && !busy && dryRunResult !== null && !dryRunResult.would_succeed === false;

  return (
    <Dialog open={open} onOpenChange={(o) => { onOpenChange(o); if (!o) resetImportState(); }}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t("模型导入 / 导出")}</DialogTitle>
          <DialogDescription>{t("版本化 JSON 配置迁移（schema v")}{MODEL_EXCHANGE_SCHEMA_VERSION}{t("）。导出不包含渠道 API Key 或 OAuth 凭据。")}</DialogDescription>
        </DialogHeader>

        {/* Mode tabs */}
        <div className="flex gap-2 border-b pb-3">
          <Button variant={mode === 'export' ? 'default' : 'outline'} size="sm" onClick={() => handleModeSwitch('export')}>
            <Download className="size-4" />{t("导出")}</Button>
          <Button variant={mode === 'import' ? 'default' : 'outline'} size="sm" onClick={() => handleModeSwitch('import')}>
            <Upload className="size-4" />{t("导入")}</Button>
        </div>

        {mode === 'export' && (
          <div className="space-y-4">
            <p className="text-sm text-muted-foreground">{t("导出当前模型注册表（含别名、渠道映射和订阅映射）为 JSON 文件，可用于多环境配置迁移或备份。")}</p>
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={exportPrices}
                onChange={(e) => setExportPrices(e.target.checked)}
                className="size-4 rounded border-input"
              />{t("包含用户售价（需要管理员权限）")}</label>
            <Button onClick={handleExport} disabled={exporting}>
              {exporting ? <Loader2 className="size-4 animate-spin" /> : <Download className="size-4" />}
              {exporting ? t("导出中…") : t("下载导出文件")}
            </Button>
          </div>
        )}

        {mode === 'import' && (
          <div className="space-y-4">
            {/* File picker */}
            <div className="space-y-2">
              <input
                ref={fileInputRef}
                type="file"
                accept="application/json,.json"
                className="hidden"
                onChange={(e) => {
                  const f = e.target.files?.[0];
                  if (f) handleFileSelect(f);
                }}
              />
              <Button variant="outline" onClick={() => fileInputRef.current?.click()}>
                <FileJson className="size-4" />{t("选择 JSON 文件")}</Button>
              {fileName && (
                <p className="text-sm text-muted-foreground">{t("已选择：")}{fileName}</p>
              )}
              {parseError && (
                <p className="text-sm text-red-600 dark:text-red-400 flex items-center gap-1">
                  <AlertTriangle className="size-4" />
                  {parseError}
                </p>
              )}
            </div>

            {/* Options */}
            <div className="grid grid-cols-2 gap-3">
              <label className="flex flex-col gap-1 text-sm">{t("冲突策略")}<select
                  value={conflictStrategy}
                  onChange={(e) => { setConflictStrategy(e.target.value as 'reject' | 'update'); setDryRunResult(null); }}
                  className="h-9 rounded-lg border border-input bg-transparent px-3"
                >
                  <option value="reject">{t("拒绝（reject）")}</option>
                  <option value="update">{t("覆盖（update）")}</option>
                </select>
              </label>
              <label className="flex items-end gap-2 text-sm pb-2">
                <input
                  type="checkbox"
                  checked={importPrices}
                  onChange={(e) => { setImportPrices(e.target.checked); setDryRunResult(null); }}
                  className="size-4 rounded border-input"
                />{t("导入价格（需要管理员权限）")}</label>
            </div>

            {/* Actions */}
            <div className="flex gap-2">
              <Button variant="outline" onClick={handleDryRun} disabled={!canDryRun}>
                {busy ? <Loader2 className="size-4 animate-spin" /> : <CheckCircle2 className="size-4" />}{t("预检（dry-run）")}</Button>
              <Button onClick={handleImport} disabled={!canImport}>
                {busy ? <Loader2 className="size-4 animate-spin" /> : <Upload className="size-4" />}{t("确认导入")}</Button>
            </div>

            {/* Dry-run result */}
            {dryRunResult && (
              <DryRunSummary result={dryRunResult} />
            )}

            {/* Import result */}
            {importResult && (
              <ImportResultSummary result={importResult} />
            )}
          </div>
        )}

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>{t("关闭")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function DryRunSummary({ result }: { result: ImportModelsDryRunResponse }) {
  return (
    <div className="rounded-lg border p-3 space-y-2">
      <div className="flex items-center gap-2 text-sm font-medium">
        {result.would_succeed ? (
          <CheckCircle2 className="size-4 text-green-600" />
        ) : (
          <AlertTriangle className="size-4 text-amber-600" />
        )}{t("预检结果")}</div>
      <div className="grid grid-cols-5 gap-2 text-center text-sm">
        <Stat label={t("新增")} value={result.would_create} color="text-green-600" />
        <Stat label={t("更新")} value={result.would_update} color="text-blue-600" />
        <Stat label={t("跳过")} value={result.would_skip} color="text-muted-foreground" />
        <Stat label={t("冲突")} value={result.conflicts} color="text-amber-600" />
        <Stat label={t("错误")} value={result.errors} color="text-red-600" />
      </div>
      {result.results.length > 0 && (
        <details className="text-xs">
          <summary className="cursor-pointer text-muted-foreground">{t("明细（")}{result.results.length}{t("条）")}</summary>
          <div className="mt-2 max-h-40 overflow-y-auto space-y-1">
            {result.results.map((r, i) => (
              <div key={i} className="flex gap-2">
                <span className="font-mono text-muted-foreground">{r.model_id}</span>
                <span className={
                  r.action === 'create' ? 'text-green-600' :
                  r.action === 'update' ? 'text-blue-600' :
                  r.action === 'conflict' ? 'text-amber-600' :
                  r.action === 'error' ? 'text-red-600' :
                  'text-muted-foreground'
                }>{r.action}</span>
                {r.message && <span className="text-muted-foreground">— {r.message}</span>}
              </div>
            ))}
          </div>
        </details>
      )}
    </div>
  );
}

function ImportResultSummary({ result }: { result: ImportModelsResponse }) {
  return (
    <div className="rounded-lg border p-3 space-y-2">
      <div className="flex items-center gap-2 text-sm font-medium">
        {result.success ? (
          <CheckCircle2 className="size-4 text-green-600" />
        ) : (
          <AlertTriangle className="size-4 text-amber-600" />
        )}{t("导入结果")}</div>
      <div className="grid grid-cols-5 gap-2 text-center text-sm">
        <Stat label={t("新增")} value={result.created} color="text-green-600" />
        <Stat label={t("更新")} value={result.updated} color="text-blue-600" />
        <Stat label={t("跳过")} value={result.skipped} color="text-muted-foreground" />
        <Stat label={t("冲突")} value={result.conflicts} color="text-amber-600" />
        <Stat label={t("错误")} value={result.errors} color="text-red-600" />
      </div>
    </div>
  );
}

function Stat({ label, value, color }: { label: string; value: number; color: string }) {
  return (
    <div>
      <div className={'text-lg font-bold ' + color}>{value}</div>
      <div className="text-xs text-muted-foreground">{label}</div>
    </div>
  );
}
