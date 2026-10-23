import { t } from '@/lib/i18n';

export const vendorReportImportPermission = 'billing.vendor_report.import';
export const vendorReportImportLimit = 2 * 1024 * 1024;
export interface VendorReportImportRequest { manifest_json: string; csv: string; expected_report_id: string; reason: string }
export interface VendorReportImportPreview {
  request: VendorReportImportRequest;
  provider: string; model: string; start: string; end: string; version: string; supersedes: string;
}
const record = (value: unknown): value is Record<string, unknown> => value !== null && typeof value === 'object' && !Array.isArray(value);

export function parseVendorReportImport(text: string): VendorReportImportPreview {
  const invalid = () => new Error(t('导入文件必须包含 manifest_json、csv、expected_report_id、reason 四个非空字符串字段，且不能有其他字段。'));
  let input: unknown;
  try { input = JSON.parse(text); } catch { throw invalid(); }
  const keys = ['manifest_json', 'csv', 'expected_report_id', 'reason'];
  if (!record(input) || Object.keys(input).length !== keys.length || keys.some(key => typeof input[key] !== 'string' || !(input[key] as string).trim())) throw invalid();
  const request = input as unknown as VendorReportImportRequest;
  if (!/^[a-f0-9]{64}$/.test(request.expected_report_id)) throw new Error(t('预期报告 ID 必须为 64 位小写十六进制值。'));
  let manifest: unknown;
  try {
    const decoded = atob(request.manifest_json);
    if (btoa(decoded) !== request.manifest_json) throw new Error();
    const bytes = Uint8Array.from(decoded, char => char.charCodeAt(0));
    manifest = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes));
  } catch { throw new Error(t('manifest_json 必须是以 Base64 编码的有效 UTF-8 JSON 文档。')); }
  const invalidPreview = () => new Error(t('报告文档缺少供应商、上游模型、账期或版本，无法预览。'));
  if (!record(manifest) || !record(manifest.scope)) throw invalidPreview();
  const scope = manifest.scope;
  if (['provider', 'upstream_model_id', 'start', 'end'].some(key => typeof scope[key] !== 'string' || !(scope[key] as string).trim()) || typeof manifest.version !== 'string' || !manifest.version.trim() || (manifest.supersedes !== undefined && typeof manifest.supersedes !== 'string')) throw invalidPreview();
  return { request, provider: scope.provider as string, model: scope.upstream_model_id as string, start: scope.start as string, end: scope.end as string, version: manifest.version, supersedes: manifest.supersedes as string ?? '' };
}
