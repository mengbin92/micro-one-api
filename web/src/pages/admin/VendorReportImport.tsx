import { useEffect, useLayoutEffect, useRef, useState, type ChangeEvent } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { authorizationKey, can, type useAuthorization } from '@/lib/authorization';
import { adminApiClient } from '@/lib/api';
import { getApiErrorMessage } from '@/lib/api-error';
import { parseVendorReportImport, vendorReportImportLimit, vendorReportImportPermission, type VendorReportImportPreview } from '@/lib/vendor-report-import';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { t } from '@/lib/i18n';

export function VendorReportImport({ auth, refresh }: { auth: ReturnType<typeof useAuthorization>; refresh: (id: string) => Promise<void> }) {
  const client = useQueryClient();
  const [draft, setDraft] = useState<VendorReportImportPreview | null>(null);
  const [busy, setBusy] = useState(false);
  const [reading, setReading] = useState(false);
  const [error, setError] = useState('');
  const [importedID, setImportedID] = useState('');
  const [uncertainID, setUncertainID] = useState('');
  const [readbackID, setReadbackID] = useState('');
  const locked = useRef(false), selection = useRef(0), mounted = useRef(true), refreshPending = useRef('');
  const current = useRef(auth);
  useLayoutEffect(() => { current.current = auth; }, [auth]);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  const allowed = (display = false) => {
    const live = current.current;
    const state = client.getQueryState([...authorizationKey, live.identity]);
    const snapshot = display ? live.displaySnapshot : live.snapshot;
    return mounted.current && live.generation === auth.generation && state?.status === 'success' && (display || state.fetchStatus === 'idle') && state.data === snapshot && can(snapshot, vendorReportImportPermission);
  };
  useEffect(() => {
    if (!readbackID || busy || !allowed() || refreshPending.current !== readbackID) return;
    refreshPending.current = '';
    void refresh(readbackID).catch(() => { if (allowed(true)) setError(importedID ? t('报告已保存，但读取最新列表失败，请刷新核对。') : t('读取最新列表失败，请手动刷新并核对导入结果。')); });
  });
  const choose = async (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    event.target.value = '';
    if (!allowed() || locked.current) return;
    const chosen = ++selection.current;
    setDraft(null); setError(''); setImportedID(''); setUncertainID(''); setReadbackID(''); refreshPending.current = '';
    if (!file) return;
    if (file.size > vendorReportImportLimit) { setError(t('导入文件不能超过 2 MiB。')); return; }
    setReading(true);
    try {
      const preview = parseVendorReportImport(await file.text());
      if (allowed() && selection.current === chosen) setDraft(preview);
    } catch (failure) {
      if (allowed() && selection.current === chosen) setError(getApiErrorMessage(failure));
    } finally { if (mounted.current && selection.current === chosen) setReading(false); }
  };
  const submit = async () => {
    if (!allowed() || !draft || locked.current || reading) return;
    locked.current = true; setBusy(true); setError(''); setImportedID(''); setUncertainID('');
    try {
      const result = await adminApiClient.post<{ report_id: string }>('/admin/vendor-reports', draft.request);
      if (result.data?.report_id !== draft.request.expected_report_id) throw new Error('Unconfirmed report response');
      if (!mounted.current || current.current.generation !== auth.generation) return;
      setDraft(null);
      if (!allowed(true)) return;
      refreshPending.current = result.data.report_id; setReadbackID(result.data.report_id); setImportedID(result.data.report_id);
    } catch (failure) {
      if (!mounted.current || current.current.generation !== auth.generation) return;
      const status = (failure as { response?: { status?: number } })?.response?.status;
      const rejected = status !== undefined && status >= 400 && status < 500 && status !== 408;
      if (rejected) {
        if (allowed(true)) setError(getApiErrorMessage(failure, t('导入失败，请核对文件和权限；系统不会自动重试。')));
      } else {
        setDraft(null);
        if (!allowed(true)) return;
        const expected = draft.request.expected_report_id;
        refreshPending.current = expected; setReadbackID(expected); setUncertainID(expected);
      }
    } finally { locked.current = false; if (mounted.current) setBusy(false); }
  };
  if (!auth.displayCan(vendorReportImportPermission)) return null;
  return <section aria-label={t('导入供应商报告')} className="space-y-3 rounded-lg border p-4">
    <h3 className="font-semibold">{t('导入供应商报告')}</h3>
    <p className="text-sm text-muted-foreground">{t('选择已准备的报告请求文件（JSON，最多 2 MiB），核对预览后导入；服务端验证报告并判定核验状态。')}</p>
    <label className="block text-sm">{t('报告请求文件')}<Input aria-label={t('报告请求文件')} type="file" accept=".json,application/json" disabled={!auth.can(vendorReportImportPermission) || busy || reading} onChange={event => void choose(event)} /></label>
    {reading && <p role="status">{t('正在读取报告文件')}</p>}
    {draft && <div className="space-y-3" aria-label={t('导入预览')}>
      <dl className="grid min-w-0 gap-3 text-sm sm:grid-cols-2">{[
        ['供应商', draft.provider], ['上游模型', draft.model], ['账期开始', draft.start], ['账期结束（不含）', draft.end],
        ['报告版本', draft.version], ['更正前序', draft.supersedes || t('未提供')], ['预期报告 ID', draft.request.expected_report_id], ['导入原因', draft.request.reason],
      ].map(([label, value]) => <div key={label} className="min-w-0"><dt className="text-muted-foreground">{t(label)}</dt><dd className="break-all">{value}</dd></div>)}</dl>
      <Button disabled={!auth.can(vendorReportImportPermission) || busy || reading} onClick={() => void submit()}>{busy ? t('正在导入') : t('确认导入报告')}</Button>
    </div>}
    {error && <p role="alert">{error}</p>}
    {uncertainID && <p role="alert" className="break-all">{t('导入结果尚未确认，请先核对列表中的预期报告 ID；系统不会自动重试。')} {uncertainID}</p>}
    {importedID && <p role="status" className="break-all">{t('报告已导入')}：{importedID}</p>}
  </section>;
}
