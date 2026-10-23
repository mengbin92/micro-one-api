import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import { describe, expect, it, vi } from 'vitest';
import { useQueryClient, type QueryClient } from '@tanstack/react-query';
import { useEffect } from 'react';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';
import { authorizationKey, useAuthorizationLifecycle } from '@/lib/authorization';
import { refreshAuthorization } from '@/lib/authorization-events';
import { vendorReport } from '@/test/fixtures/vendor-report';
import { VendorReportsPage } from './VendorReportsPage';
import { adminApiClient } from '@/lib/api';

const reportID = 'a'.repeat(64);
const manifest = { scope: { provider: 'deepseek', upstream_model_id: 'deepseek-flash', start: '2026-10-21T00:00:00+08:00', end: '2026-10-23T00:00:00+08:00' }, version: '最终版本-v2', supersedes: 'b'.repeat(64) };
const encoded = btoa(String.fromCharCode(...new TextEncoder().encode(JSON.stringify(manifest))));
const request = { manifest_json: encoded, csv: 'usage,amount\n203,2.04700896', expected_report_id: reportID, reason: '维护者确认账期不再更正' };
function Page() { useAuthorizationLifecycle(); return <VendorReportsPage />; }
function grant(importAllowed = true, revision = '1') {
  return HttpResponse.json({ authorization_mode: 'iam', session: { activation_state: 'active', revision }, permitted_operations: ['admin.console.enter', 'billing.vendor_report.read', ...(importAllowed ? ['billing.vendor_report.import'] : [])] });
}
function file(text = JSON.stringify(request), size?: number) {
  const value = new File([text], 'owner-request.json', { type: 'application/json' });
  Object.defineProperty(value, 'text', { value: async () => text, configurable: true });
  if (size !== undefined) Object.defineProperty(value, 'size', { value: size });
  return value;
}
async function select(value = file()) {
  const picker = await screen.findByLabelText('报告请求文件');
  await waitFor(() => expect(picker).toBeEnabled());
  fireEvent.change(picker, { target: { files: [value] } });
}
function reads() { return http.get('/api/admin/vendor-reports', () => HttpResponse.json({ reports: [] })); }

describe('supplier report import', () => {
  it('does not offer import or send POST without import permission', async () => {
    let posts = 0;
    server.use(http.get('/api/user/authorization', () => grant(false)), reads(), http.post('/api/admin/vendor-reports', () => { posts++; return HttpResponse.json({}); }));
    renderWithQuery(<Page />);
    await waitFor(() => expect(screen.getByRole('button', { name: '导出复盘材料' })).toBeEnabled());
    expect(screen.queryByLabelText('报告请求文件')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '确认导入报告' })).not.toBeInTheDocument();
    expect(posts).toBe(0);
  });
  it.each([
    ['invalid JSON', '{', undefined],
    ['unknown fields', JSON.stringify({ ...request, ignored: true }), undefined],
    ['wrong types', JSON.stringify({ ...request, csv: [] }), undefined],
    ['missing fields', JSON.stringify({ ...request, reason: '' }), undefined],
    ['invalid Base64', JSON.stringify({ ...request, manifest_json: '%%%' }), undefined],
    ['invalid UTF-8', JSON.stringify({ ...request, manifest_json: '/w==' }), undefined],
    ['oversized file', JSON.stringify(request), 2 * 1024 * 1024 + 1],
  ])('rejects %s without POST', async (_name, text, size) => {
    let posts = 0;
    server.use(http.get('/api/user/authorization', () => grant()), reads(), http.post('/api/admin/vendor-reports', () => { posts++; return HttpResponse.json({}); }));
    renderWithQuery(<Page />);
    await select(file(text, size));
    expect(await screen.findByRole('alert')).toBeInTheDocument();
    expect(screen.queryByLabelText('导入预览')).not.toBeInTheDocument();
    expect(posts).toBe(0);
  });
  it('previews UTF-8 metadata and posts the exact request once before refreshing and showing returned ID', async () => {
    let body: unknown, getCount = 0, imported = false;
    server.use(http.get('/api/user/authorization', () => grant()), http.get('/api/admin/vendor-reports', () => { getCount++; return HttpResponse.json({ reports: imported ? [{ ...vendorReport, id: reportID }] : [] }); }), http.post('/api/admin/vendor-reports', async ({ request: req }) => { body = await req.json(); imported = true; return HttpResponse.json({ report_id: reportID }); }));
    renderWithQuery(<Page />);
    await select();
    const preview = within(await screen.findByLabelText('导入预览'));
    for (const text of [manifest.scope.provider, manifest.scope.upstream_model_id, manifest.scope.start, manifest.scope.end, manifest.version, manifest.supersedes, reportID, request.reason]) expect(preview.getByText(text, { exact: true })).toBeInTheDocument();
    expect(screen.queryByText(encoded)).not.toBeInTheDocument();
    expect(body).toBeUndefined();
    fireEvent.click(screen.getByRole('button', { name: '确认导入报告' }));
    expect(await screen.findByText(`报告已导入：${reportID}`)).toBeInTheDocument();
    expect(body).toEqual(request);
    await waitFor(() => expect(getCount).toBeGreaterThanOrEqual(2));
    expect(screen.queryByLabelText('导入预览')).not.toBeInTheDocument();
    expect(await screen.findByRole('region', { name: '报告证据' })).toBeInTheDocument();
  });
  it('blocks duplicate clicks while the single POST is pending', async () => {
    let posts = 0, finish!: () => void;
    const pending = new Promise<void>(resolve => { finish = resolve; });
    server.use(http.get('/api/user/authorization', () => grant()), reads(), http.post('/api/admin/vendor-reports', async () => { posts++; await pending; return HttpResponse.json({ report_id: reportID }); }));
    renderWithQuery(<Page />);
    await select();
    const button = await screen.findByRole('button', { name: '确认导入报告' });
    act(() => { button.click(); button.click(); });
    await waitFor(() => expect(posts).toBe(1));
    expect(screen.getByRole('button', { name: '正在导入' })).toBeDisabled();
    expect(screen.getByLabelText('报告请求文件')).toBeDisabled();
    finish();
    await screen.findByText(`报告已导入：${reportID}`);
    expect(posts).toBe(1);
  });
  it.each(['revocation', 'session change'])('clears the draft after %s and never restores it on regrant', async boundary => {
    let allowed = true, revision = '1', posts = 0;
    server.use(http.get('/api/user/authorization', () => grant(allowed, revision)), reads(), http.post('/api/admin/vendor-reports', () => { posts++; return HttpResponse.json({ report_id: reportID }); }));
    renderWithQuery(<Page />);
    await select();
    await screen.findByLabelText('导入预览');
    if (boundary === 'revocation') allowed = false; else revision = '2';
    act(() => refreshAuthorization());
    await waitFor(() => expect(screen.queryByLabelText('导入预览')).not.toBeInTheDocument());
    allowed = true;
    act(() => refreshAuthorization());
    await waitFor(() => expect(screen.getByLabelText('报告请求文件')).toBeEnabled());
    expect(screen.queryByLabelText('导入预览')).not.toBeInTheDocument();
    expect(posts).toBe(0);
  });
  it('keeps server rejection visible and never retries automatically', async () => {
    let posts = 0;
    server.use(http.get('/api/user/authorization', () => grant()), reads(), http.post('/api/admin/vendor-reports', () => { posts++; return HttpResponse.json({ message: 'report validation failed' }, { status: 400 }); }));
    renderWithQuery(<Page />);
    await select();
    fireEvent.click(await screen.findByRole('button', { name: '确认导入报告' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('report validation failed');
    expect(posts).toBe(1);
    expect(screen.getByLabelText('导入预览')).toBeInTheDocument();
  });
  it.each(['revocation', 'session change'])('discards a file read that resolves after %s', async boundary => {
    let allowed = true, revision = '1', finish!: (value: string) => void, posts = 0;
    const pending = new Promise<string>(resolve => { finish = resolve; });
    const selectedFile = file();
    Object.defineProperty(selectedFile, 'text', { value: () => pending, configurable: true });
    server.use(http.get('/api/user/authorization', () => grant(allowed, revision)), reads(), http.post('/api/admin/vendor-reports', () => { posts++; return HttpResponse.json({ report_id: reportID }); }));
    renderWithQuery(<Page />);
    await select(selectedFile);
    await screen.findByText('正在读取报告文件');
    if (boundary === 'revocation') allowed = false; else revision = '2';
    act(() => refreshAuthorization());
    if (boundary === 'revocation') await waitFor(() => expect(screen.queryByLabelText('报告请求文件')).not.toBeInTheDocument());
    else await waitFor(() => expect(screen.queryByText('正在读取报告文件')).not.toBeInTheDocument());
    allowed = true;
    act(() => refreshAuthorization());
    await waitFor(() => expect(screen.getByLabelText('报告请求文件')).toBeEnabled());
    await act(async () => finish(JSON.stringify(request)));
    expect(screen.queryByLabelText('导入预览')).not.toBeInTheDocument();
    expect(posts).toBe(0);
  });
  it('keeps a selected draft through an unchanged permission refresh', async () => {
    server.use(http.get('/api/user/authorization', () => grant()), reads());
    renderWithQuery(<Page />);
    await select();
    await screen.findByLabelText('导入预览');
    act(() => refreshAuthorization());
    await waitFor(() => expect(screen.getByRole('button', { name: '确认导入报告' })).toBeEnabled());
    expect(screen.getByLabelText('导入预览')).toBeInTheDocument();
  });
  it('consumes a successful draft while unchanged authorization is refreshing, then refreshes without another POST', async () => {
    let client!: QueryClient, holdAuthorization = false, posts = 0, getCount = 0, finishPost!: () => void, finishAuthorization!: () => void;
    const pendingPost = new Promise<void>(resolve => { finishPost = resolve; });
    const pendingAuthorization = new Promise<void>(resolve => { finishAuthorization = resolve; });
    server.use(http.get('/api/user/authorization', async () => { if (holdAuthorization) await pendingAuthorization; return grant(); }), http.get('/api/admin/vendor-reports', () => { getCount++; return HttpResponse.json({ reports: [] }); }), http.post('/api/admin/vendor-reports', async () => { posts++; await pendingPost; return HttpResponse.json({ report_id: reportID }); }));
    function Shell() { const queryClient = useQueryClient(); useEffect(() => { client = queryClient; }, [queryClient]); return <Page />; }
    renderWithQuery(<Shell />);
    await select();
    fireEvent.click(await screen.findByRole('button', { name: '确认导入报告' }));
    await waitFor(() => expect(posts).toBe(1));
    holdAuthorization = true;
    act(() => { void client.refetchQueries({ queryKey: authorizationKey }); });
    await waitFor(() => expect(client.getQueryState([...authorizationKey, 0])?.fetchStatus ?? client.getQueryCache().findAll({ queryKey: authorizationKey })[0]?.state.fetchStatus).toBe('fetching'));
    finishPost();
    await waitFor(() => expect(screen.queryByLabelText('导入预览')).not.toBeInTheDocument());
    expect(await screen.findByText(`报告已导入：${reportID}`)).toBeInTheDocument();
    holdAuthorization = false; finishAuthorization();
    await waitFor(() => expect(screen.getByLabelText('报告请求文件')).toBeEnabled());
    await waitFor(() => expect(getCount).toBeGreaterThanOrEqual(2));
    expect(screen.queryByRole('button', { name: '确认导入报告' })).not.toBeInTheDocument();
    expect(posts).toBe(1);
  });
  it.each(['network failure', '503 response'])('consumes an ambiguous saved draft after %s during authorization refresh and checks the list once', async transport => {
    let client!: QueryClient, holdAuthorization = false, posts = 0, getCount = 0, saved = false, finishPost!: () => void, finishAuthorization!: () => void;
    const pendingPost = new Promise<void>(resolve => { finishPost = resolve; });
    const pendingAuthorization = new Promise<void>(resolve => { finishAuthorization = resolve; });
    server.use(http.get('/api/user/authorization', async () => { if (holdAuthorization) await pendingAuthorization; return grant(); }), http.get('/api/admin/vendor-reports', () => { getCount++; return HttpResponse.json({ reports: saved ? [{ ...vendorReport, id: reportID }] : [] }); }), http.post('/api/admin/vendor-reports', async () => { posts++; saved = true; await pendingPost; return transport === 'network failure' ? HttpResponse.error() : HttpResponse.json({ message: 'upstream timeout after save' }, { status: 503 }); }));
    function Shell() { const queryClient = useQueryClient(); useEffect(() => { client = queryClient; }, [queryClient]); return <Page />; }
    renderWithQuery(<Shell />);
    await select();
    fireEvent.click(await screen.findByRole('button', { name: '确认导入报告' }));
    await waitFor(() => expect(posts).toBe(1));
    holdAuthorization = true;
    act(() => { void client.refetchQueries({ queryKey: authorizationKey }); });
    await waitFor(() => expect(client.getQueryCache().findAll({ queryKey: authorizationKey })[0]?.state.fetchStatus).toBe('fetching'));
    finishPost();
    const notice = await screen.findByRole('alert');
    expect(notice).toHaveTextContent('导入结果尚未确认，请先核对列表中的预期报告 ID；系统不会自动重试。');
    expect(notice).toHaveTextContent(reportID);
    expect(screen.queryByLabelText('导入预览')).not.toBeInTheDocument();
    holdAuthorization = false; finishAuthorization();
    await waitFor(() => expect(screen.getByLabelText('报告请求文件')).toBeEnabled());
    await waitFor(() => expect(getCount).toBe(2));
    expect(await screen.findByRole('region', { name: '报告证据' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '确认导入报告' })).not.toBeInTheDocument();
    expect(posts).toBe(1);
  });
  it('treats a transport timeout as uncertain and does not leave a retryable draft', async () => {
    server.use(http.get('/api/user/authorization', () => grant()), reads());
    const post = vi.spyOn(adminApiClient, 'post').mockRejectedValueOnce(Object.assign(new Error('timeout of 30000ms exceeded'), { code: 'ECONNABORTED' }));
    renderWithQuery(<Page />);
    await select();
    fireEvent.click(await screen.findByRole('button', { name: '确认导入报告' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('导入结果尚未确认，请先核对列表中的预期报告 ID；系统不会自动重试。');
    expect(screen.queryByLabelText('导入预览')).not.toBeInTheDocument();
    expect(post).toHaveBeenCalledTimes(1);
  });
  it('keeps a known validation rejection visible while unchanged authorization is refreshing', async () => {
    let client!: QueryClient, holdAuthorization = false, posts = 0, finishPost!: () => void, finishAuthorization!: () => void;
    const pendingPost = new Promise<void>(resolve => { finishPost = resolve; });
    const pendingAuthorization = new Promise<void>(resolve => { finishAuthorization = resolve; });
    server.use(http.get('/api/user/authorization', async () => { if (holdAuthorization) await pendingAuthorization; return grant(); }), reads(), http.post('/api/admin/vendor-reports', async () => { posts++; await pendingPost; return HttpResponse.json({ message: 'report validation failed' }, { status: 400 }); }));
    function Shell() { const queryClient = useQueryClient(); useEffect(() => { client = queryClient; }, [queryClient]); return <Page />; }
    renderWithQuery(<Shell />);
    await select();
    fireEvent.click(await screen.findByRole('button', { name: '确认导入报告' }));
    await waitFor(() => expect(posts).toBe(1));
    holdAuthorization = true;
    act(() => { void client.refetchQueries({ queryKey: authorizationKey }); });
    await waitFor(() => expect(client.getQueryCache().findAll({ queryKey: authorizationKey })[0]?.state.fetchStatus).toBe('fetching'));
    finishPost();
    expect(await screen.findByRole('alert')).toHaveTextContent('report validation failed');
    expect(screen.getByLabelText('导入预览')).toBeInTheDocument();
    holdAuthorization = false; finishAuthorization();
    await waitFor(() => expect(screen.getByRole('button', { name: '确认导入报告' })).toBeEnabled());
    expect(screen.getByRole('alert')).toHaveTextContent('report validation failed');
    expect(posts).toBe(1);
  });
});
