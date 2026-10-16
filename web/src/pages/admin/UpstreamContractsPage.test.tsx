import { fireEvent, screen, waitFor } from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import { describe, expect, it } from 'vitest';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';
import { useAuthorizationLifecycle } from '@/lib/authorization';
import { refreshAuthorization } from '@/lib/authorization-events';
import { UpstreamContractsPage } from './UpstreamContractsPage';

const read = 'channel.upstream_contract.read';
const publish = 'channel.upstream_contract.publish';
const create = 'channel.upstream_contract.create';
const scope = { source_kind: 'channel', source_id: '9007199254740993', upstream_model_id: 'claude-sonnet', protocol: 'anthropic_messages' };
const revision = 'a'.repeat(64);
const document = { scope, provider: 'supplier', adapter_build: 'build-1', parser_revision: 'parser-1', usage_revision: 'usage-1', inbound_protocols: ['anthropic_messages'], rule_sets: [], fixture_evidence_id: 'fixture-1', basis_revision: 'basis-1', repair_review: { proposal_id: 'proposal-1', base_revision: 'base-1', binding_revision: '7', incident_revision: '2', source_revision: 'source-1', replay_id: 'replay-1', ci_evidence_id: 'ci-1', reviewer: 'reviewer-1', key_id: 'key-1', signature: 'signed-1', expires_at: '1893456000' } };
const payload = { contracts: [{ document, revision, binding_revision: '7', incident_revision: '2' }], binding: { scope, current_revision: revision, previous_revision: '', revision: '9007199254740995', incident_revision: '2', status: 'gray', gray_percent: 3, complete_attempts: '100', failures: '0', started_at: '1792000000' } };
function authorization(operations: string[]) { server.use(http.get('/api/user/authorization', () => HttpResponse.json({ authorization_mode: 'iam', session: { activation_state: 'active', revision: '1' }, permitted_operations: ['admin.console.enter', ...operations] }))); }
function Page() { useAuthorizationLifecycle(); return <UpstreamContractsPage />; }
function fillScope() {
  fireEvent.change(screen.getByLabelText('来源 ID'), { target: { value: scope.source_id } });
  fireEvent.change(screen.getByLabelText('上游模型 ID'), { target: { value: scope.upstream_model_id } });
  fireEvent.change(screen.getByLabelText('协议'), { target: { value: scope.protocol } });
}
async function load(operations = [read]) {
  authorization(operations);
  server.use(http.get('/api/admin/upstream-contracts', ({ request }) => {
    const query = new URL(request.url).searchParams;
    expect(query.get('source_id')).toBe(scope.source_id);
    expect(query.get('source_kind')).toBe(scope.source_kind);
    expect(query.get('upstream_model_id')).toBe(scope.upstream_model_id);
    expect(query.get('protocol')).toBe(scope.protocol);
    return HttpResponse.json(payload);
  }));
  renderWithQuery(<Page />);
  await waitFor(() => expect(screen.getByRole('button', { name: '查询契约' })).toBeEnabled());
  fillScope();
  fireEvent.click(screen.getByRole('button', { name: '查询契约' }));
  await screen.findByText('supplier');
}

describe('upstream contract administration', () => {
  it('starts without a scope and never sends a broad or paid probe request', async () => {
    authorization([read]);
    let calls = 0;
    server.use(http.get('/api/admin/upstream-contracts', () => { calls++; return HttpResponse.json(payload); }));
    renderWithQuery(<Page />);
    await screen.findByText('填写精确来源、上游模型和协议后查询，不支持通配符。');
    expect(calls).toBe(0);
    fireEvent.click(screen.getByRole('button', { name: '查询契约' }));
    await screen.findByRole('alert');
    expect(calls).toBe(0);
  });
  it('shows exact binding versions, immutable documents and candidate review evidence for read-only operators', async () => {
    await load();
    expect(screen.getByText('9007199254740995')).toBeInTheDocument();
    expect(screen.getByText('3%')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '查看版本' }));
    expect(screen.getByText('proposal-1')).toBeInTheDocument();
    expect(screen.getByText('ci-1')).toBeInTheDocument();
    expect(screen.getByText('reviewer-1')).toBeInTheDocument();
    expect(screen.getByText('签名和 CI 证据由服务端发布门禁验证，证据引用不代表审核通过。')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '创建候选版本' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '提交状态变更' })).not.toBeInTheDocument();
  });
  it('does not query without read permission, even after entering an exact scope', async () => {
    authorization([]);
    let calls = 0;
    server.use(http.get('/api/admin/upstream-contracts', () => { calls++; return HttpResponse.json(payload); }));
    renderWithQuery(<Page />);
    expect(await screen.findByText('没有读取上游契约的权限。')).toBeInTheDocument();
    expect(calls).toBe(0);
  });
  it('uses the displayed binding CAS, preserves a Chinese reason and never retries a conflict', async () => {
    let calls = 0;
    server.use(http.post('/api/admin/upstream-contracts/transition', async ({ request }) => {
      calls++;
      expect(await request.json()).toMatchObject({ scope, revision, action: 'publish', gray_percent: 1, expected_revision: '9007199254740995', reason: '依据独立证据灰度发布' });
      expect(request.headers.has('x-authorization-reason')).toBe(false);
      return HttpResponse.json({ reason: 'UPSTREAM_CONTRACT_CONFLICT' }, { status: 409 });
    }));
    await load([read, publish]);
    fireEvent.change(screen.getByLabelText('变更原因'), { target: { value: '依据独立证据灰度发布' } });
    fireEvent.click(screen.getByRole('button', { name: '提交状态变更' }));
    expect(await screen.findByText('绑定版本已变化或发布门禁尚未通过。请刷新查询并重新核对证据，系统不会自动重试。')).toBeInTheDocument();
    expect(calls).toBe(1);
    expect(screen.getByRole('button', { name: '提交状态变更' })).toBeDisabled();
  });
  it('validates candidate scope before writing, then posts the immutable document with the reason', async () => {
    let calls = 0;
    server.use(http.post('/api/admin/upstream-contracts', async ({ request }) => {
      calls++;
      expect(await request.json()).toMatchObject({ document, reason: '补齐候选修复证据' });
      return HttpResponse.json({ revision });
    }));
    await load([read, create]);
    fireEvent.change(screen.getByLabelText('候选文档 JSON'), { target: { value: JSON.stringify({ ...document, scope: { ...scope, source_id: '12' } }) } });
    fireEvent.change(screen.getByLabelText('创建原因'), { target: { value: '补齐候选修复证据' } });
    fireEvent.click(screen.getByRole('button', { name: '创建候选版本' }));
    expect(await screen.findByText('候选文档的 scope 必须与当前查询完全一致。')).toBeInTheDocument();
    expect(calls).toBe(0);
    fireEvent.change(screen.getByLabelText('候选文档 JSON'), { target: { value: JSON.stringify(document) } });
    fireEvent.click(screen.getByRole('button', { name: '创建候选版本' }));
    expect(await screen.findByText('候选版本已创建，尚未发布。')).toBeInTheDocument();
    expect(calls).toBe(1);
  });
  it('shows only quarantine for quarantine-only operators and uses the current revision', async () => {
    let body: unknown;
    server.use(http.post('/api/admin/upstream-contracts/transition', async ({ request }) => {
      body = await request.json();
      return HttpResponse.json({ binding: { ...payload.binding, status: 'quarantined' } });
    }));
    await load([read, 'channel.upstream_contract.quarantine']);
    const select = screen.getByLabelText('状态变更操作');
    expect(select).toHaveValue('quarantine');
    expect(screen.queryByRole('option', { name: '发布灰度' })).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('变更原因'), { target: { value: '隔离异常协议' } });
    fireEvent.click(screen.getByRole('button', { name: '提交状态变更' }));
    await screen.findByText('状态变更已提交，请重新查询确认当前绑定。');
    expect(body).toMatchObject({ scope, revision, expected_revision: payload.binding.revision, action: 'quarantine', reason: '隔离异常协议' });
  });
  it('retains a confirmed create if its follow-up read fails and blocks duplicate submissions', async () => {
    let calls = 0;
    server.use(http.post('/api/admin/upstream-contracts', () => { calls++; return HttpResponse.json({ revision }); }));
    await load([read, create]);
    server.use(http.get('/api/admin/upstream-contracts', () => HttpResponse.json({ message: 'persistence unavailable' }, { status: 500 })));
    fireEvent.change(screen.getByLabelText('候选文档 JSON'), { target: { value: JSON.stringify(document) } });
    fireEvent.change(screen.getByLabelText('创建原因'), { target: { value: '保存候选' } });
    fireEvent.click(screen.getByRole('button', { name: '创建候选版本' }));
    expect(await screen.findByText(/读取契约失败，请检查权限并重新查询。/)).toBeInTheDocument();
    expect(screen.getByText('候选版本已创建，尚未发布。')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '创建候选版本' })).toBeDisabled();
    expect(calls).toBe(1);
  });
  it('publishes the first legacy candidate with an omitted zero CAS and can clone omitted empty rule sets', async () => {
    const legacyScope = { ...scope, source_id: 7 };
    const legacyDocument = { ...document, scope: legacyScope, repair_review: undefined, rule_sets: undefined };
    let created = false;
    let transitionBody: unknown;
    server.use(
      http.get('/api/admin/upstream-contracts', () => HttpResponse.json(created
        ? { contracts: [{ document: legacyDocument, revision }], binding: { scope: legacyScope, status: 'candidate' } }
        : { binding: { scope: {} } })),
      http.post('/api/admin/upstream-contracts', async ({ request }) => {
        expect(await request.json()).toMatchObject({ document: { scope: legacyScope, rule_sets: [] }, reason: '创建首个契约' });
        created = true;
        return HttpResponse.json({ revision });
      }),
      http.post('/api/admin/upstream-contracts/transition', async ({ request }) => {
        transitionBody = await request.json();
        return HttpResponse.json({ binding: { scope: legacyScope, status: 'gray', current_revision: revision, revision: 1 } });
      }),
    );
    renderWithQuery(<Page />);
    await waitFor(() => expect(screen.getByRole('button', { name: '查询契约' })).toBeEnabled());
    fillScope();
    fireEvent.change(screen.getByLabelText('来源 ID'), { target: { value: '7' } });
    fireEvent.click(screen.getByRole('button', { name: '查询契约' }));
    expect(await screen.findByText('此范围尚无绑定。')).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('候选文档 JSON'), { target: { value: JSON.stringify({ ...legacyDocument, rule_sets: [] }) } });
    fireEvent.change(screen.getByLabelText('创建原因'), { target: { value: '创建首个契约' } });
    fireEvent.click(screen.getByRole('button', { name: '创建候选版本' }));
    await screen.findByText('候选');
    fireEvent.click(screen.getByRole('button', { name: '查看版本' }));
    fireEvent.click(screen.getByRole('button', { name: '复制为候选草稿' }));
    expect(JSON.parse((screen.getByLabelText('候选文档 JSON') as HTMLTextAreaElement).value).rule_sets).toEqual([]);
    fireEvent.change(screen.getByLabelText('变更原因'), { target: { value: '发布首个契约' } });
    fireEvent.click(screen.getByRole('button', { name: '提交状态变更' }));
    await screen.findByText('状态变更已提交，请重新查询确认当前绑定。');
    expect(transitionBody).toMatchObject({ scope: legacyScope, revision, expected_revision: 0, reason: '发布首个契约', action: 'publish', gray_percent: 1 });
  });
  it('removes evidence and write drafts when authorization changes', async () => {
    await load([read, create, publish]);
    fireEvent.click(screen.getByRole('button', { name: '查看版本' }));
    expect(screen.getByText('proposal-1')).toBeInTheDocument();
    authorization([]);
    refreshAuthorization();
    expect(await screen.findByText('没有读取上游契约的权限。')).toBeInTheDocument();
    expect(screen.queryByText('proposal-1')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('候选文档 JSON')).not.toBeInTheDocument();
  });
});
