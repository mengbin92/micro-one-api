import { useState, type FormEvent } from 'react';
import { useAuthorization, useAuthorizedQuery } from '@/lib/authorization';
import { adminApiClient } from '@/lib/api';
import { getApiErrorMessage } from '@/lib/api-error';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { t } from '@/lib/i18n';
import { blankContractDocument, contractCreatePermission, contractInteger, contractProtocols, contractPublishPermission, contractQuarantinePermission, contractReadPermission, contractWriteBody, exactContractScope, normalizeContractList, parseContractDocument, type ContractList, type ContractScope, type ContractSnapshot } from '@/lib/upstream-contracts';

type Authorization = ReturnType<typeof useAuthorization>;
const selectClass = 'h-10 w-full rounded-xl border border-input bg-background px-3 text-sm';
const stateLabel = (state: string) => ({ candidate: t('候选'), gray: t('灰度中'), active: t('已生效'), quarantined: t('已隔离') })[state] ?? (state || t('未绑定'));
const actionLabel = (action: string) => ({ publish: t('发布灰度'), promote: t('晋级全量'), rollback: t('回滚前序'), recover: t('恢复绑定'), quarantine: t('隔离绑定') })[action] ?? action;
const integerLabel = (value: unknown) => {
  try { return contractInteger(value); } catch (failure) { return getApiErrorMessage(failure); }
};
const dateLabel = (seconds: string | number | undefined) => {
  if (!seconds || seconds === '0') return '-';
  const date = new Date(Number(seconds) * 1000);
  return Number.isFinite(date.getTime()) ? date.toISOString() : String(seconds);
};

export function UpstreamContractsPage() {
  const auth = useAuthorization();
  return <div className="space-y-5">
    <div><h2 className="text-2xl font-bold">{t('上游契约与修复')}</h2><p className="text-sm text-muted-foreground">{t('按精确来源查询不可变契约、当前绑定和候选修复证据。')}</p></div>
    {auth.isPending || !auth.displaySnapshot ? <p>{auth.isError ? t('授权状态不可用') : t('加载中')}</p> : !auth.displayCan(contractReadPermission) ? <p role="alert">{t('没有读取上游契约的权限。')}</p> : <ContractWorkspace key={auth.generation} auth={auth} />}
  </div>;
}

function ContractWorkspace({ auth }: { auth: Authorization }) {
  const [draftScope, setDraftScope] = useState<ContractScope>({ source_kind: 'channel', source_id: '', upstream_model_id: '', protocol: 'openai_chat' });
  const [scope, setScope] = useState<ContractScope | null>(null);
  const [limit, setLimit] = useState(20);
  const [activeLimit, setActiveLimit] = useState(20);
  const [scopeError, setScopeError] = useState('');
  const query = useAuthorizedQuery<ContractList>({ permission: contractReadPermission, queryKey: ['admin-upstream-contracts', scope, activeLimit], enabled: !!scope,
    queryFn: async ({ signal }) => {
      const result = (await adminApiClient.get<ContractList>('/admin/upstream-contracts', { params: { ...scope, page_size: activeLimit }, signal })).data;
      return normalizeContractList(result, auth.displaySnapshot?.authorization_mode === 'legacy', scope!);
    } });
  const [queryGeneration, setQueryGeneration] = useState(0);
  const search = (event: FormEvent) => {
    event.preventDefault();
    try {
      const next = exactContractScope(draftScope);
      setScopeError(''); setScope(next); setActiveLimit(limit); setQueryGeneration(value => value + 1);
      if (JSON.stringify(next) === JSON.stringify(scope) && limit === activeLimit) void query.refetch();
    } catch (error) { setScopeError(getApiErrorMessage(error)); }
  };
  return <>
    <form onSubmit={search} className="flex flex-wrap items-end gap-3 rounded-lg border p-4">
      <label className="text-sm">{t('来源类型')}<select aria-label={t('来源类型')} className={selectClass} value={draftScope.source_kind} onChange={e => setDraftScope({ ...draftScope, source_kind: e.target.value })}><option value="channel">{t('渠道')}</option><option value="subscription">{t('订阅账号')}</option></select></label>
      <label className="text-sm">{t('来源 ID')}<Input aria-label={t('来源 ID')} inputMode="numeric" value={draftScope.source_id} onChange={e => setDraftScope({ ...draftScope, source_id: e.target.value })} /></label>
      <label className="text-sm">{t('上游模型 ID')}<Input aria-label={t('上游模型 ID')} value={draftScope.upstream_model_id} onChange={e => setDraftScope({ ...draftScope, upstream_model_id: e.target.value })} /></label>
      <label className="text-sm">{t('协议')}<select aria-label={t('协议')} className={selectClass} value={draftScope.protocol} onChange={e => setDraftScope({ ...draftScope, protocol: e.target.value })}>{contractProtocols.map(protocol => <option key={protocol} value={protocol}>{protocol}</option>)}</select></label>
      <label className="text-sm">{t('历史版本上限')}<select aria-label={t('历史版本上限')} className={selectClass} value={limit} onChange={e => setLimit(Number(e.target.value))}>{[20, 50, 100].map(value => <option key={value}>{value}</option>)}</select></label>
      <Button type="submit" disabled={!auth.can(contractReadPermission) || query.isFetching}>{t('查询契约')}</Button>
    </form>
    {scopeError && <p role="alert">{scopeError}</p>}
    {!scope && <p className="text-sm text-muted-foreground">{t('填写精确来源、上游模型和协议后查询，不支持通配符。')}</p>}
    {scope && <section className="space-y-4">
      <p className="break-all text-sm">{t('当前查询范围')}：{scope.source_kind} / {scope.source_id} / {scope.upstream_model_id} / {scope.protocol}</p>
      {query.isLoading && <p>{t('加载中')}</p>}
      {query.isError && <p role="alert">{t('读取契约失败，请检查权限并重新查询。')} {getApiErrorMessage(query.error)}</p>}
      {query.data && !query.isRestricted && <ContractResults key={`${JSON.stringify(scope)}:${activeLimit}:${queryGeneration}`} auth={auth} scope={scope} data={query.data} limit={activeLimit} refreshing={query.isFetching || query.isError} refresh={async () => { await query.refetch(); }} />}
    </section>}
  </>;
}

function ContractResults({ auth, scope, data, limit, refreshing, refresh }: { auth: Authorization; scope: ContractScope; data: ContractList; limit: number; refreshing: boolean; refresh: () => Promise<void> }) {
  const contracts = data.contracts ?? [];
  const binding = data.binding && (data.binding.status || data.binding.scope?.source_kind) ? data.binding : null;
  const [selected, setSelected] = useState<string | null>(null);
  const detail = contracts.find(contract => contract.revision === selected);
  const canCreate = auth.can(contractCreatePermission), canPublish = auth.can(contractPublishPermission), canQuarantine = auth.can(contractQuarantinePermission);
  const actions = [...(canPublish ? ['publish', 'promote', 'rollback', 'recover'] : []), ...(canQuarantine ? ['quarantine'] : [])];
  const [action, setAction] = useState(canPublish ? 'publish' : 'quarantine');
  const [targetRevision, setTargetRevision] = useState(contracts[0]?.revision ?? '');
  const [gray, setGray] = useState('1');
  const [reason, setReason] = useState('');
  const [documentJSON, setDocumentJSON] = useState(() => JSON.stringify(blankContractDocument(scope), null, 2));
  const [createReason, setCreateReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState('');
  const [error, setError] = useState('');
  const [needsRefresh, setNeedsRefresh] = useState(false);
  const target = action === 'publish' ? targetRevision : action === 'rollback' ? binding?.previous_revision ?? '' : binding?.current_revision ?? '';
  const legacy = auth.snapshot?.authorization_mode === 'legacy';
  const handleError = (failure: unknown) => {
    if ((failure as { response?: { status?: number } }).response?.status === 409) {
      setNeedsRefresh(true); setError(t('绑定版本已变化或发布门禁尚未通过。请刷新查询并重新核对证据，系统不会自动重试。'));
    } else setError(getApiErrorMessage(failure, t('操作失败，请核对文档和权限。')));
  };
  const createCandidate = async (event: FormEvent) => {
    event.preventDefault();
    if (!auth.can(contractCreatePermission) || busy || refreshing) return;
    setError(''); setMessage('');
    try {
      const document = parseContractDocument(documentJSON, scope);
      if (!createReason.trim()) throw new Error(t('变更原因不能为空'));
      const body = contractWriteBody({ document, reason: createReason.trim() }, legacy);
      setBusy(true);
      const result = await adminApiClient.post<{ revision: string }>('/admin/upstream-contracts', body);
      setTargetRevision(result.data.revision);
      setMessage(t('候选版本已创建，尚未发布。')); await refresh();
    } catch (failure) { handleError(failure); } finally { setBusy(false); }
  };
  const transition = async (event: FormEvent) => {
    event.preventDefault();
    if (!auth.can(action === 'quarantine' ? contractQuarantinePermission : contractPublishPermission) || !binding || needsRefresh || busy || refreshing) return;
    setError(''); setMessage('');
    try {
      if (!reason.trim()) throw new Error(t('变更原因不能为空'));
      if (!target || (action === 'publish' && (!Number.isInteger(Number(gray)) || Number(gray) < 1 || Number(gray) > 5))) throw new Error(t('请选择目标版本，首次灰度比例必须为 1 至 5。'));
      const body = contractWriteBody({ scope, revision: target, expected_revision: contractInteger(binding.revision), reason: reason.trim(), action, gray_percent: action === 'publish' ? Number(gray) : 0 }, legacy);
      setBusy(true); await adminApiClient.post('/admin/upstream-contracts/transition', body);
      setNeedsRefresh(true); setMessage(t('状态变更已提交，请重新查询确认当前绑定。')); await refresh();
    } catch (failure) { handleError(failure); } finally { setBusy(false); }
  };
  return <>
    <section className="rounded-lg border p-4"><h3 className="font-semibold">{t('当前绑定')}</h3>
      {binding ? <dl className="mt-3 grid gap-2 break-all text-sm sm:grid-cols-2">
        <div><dt className="text-muted-foreground">{t('状态')}</dt><dd>{stateLabel(binding.status)}</dd></div><div><dt className="text-muted-foreground">{t('灰度比例')}</dt><dd>{binding.gray_percent ?? 0}%</dd></div>
        <div><dt className="text-muted-foreground">{t('绑定版本（CAS）')}</dt><dd>{integerLabel(binding.revision)}</dd></div><div><dt className="text-muted-foreground">{t('事件版本')}</dt><dd>{integerLabel(binding.incident_revision)}</dd></div>
        <div><dt className="text-muted-foreground">{t('当前契约版本')}</dt><dd className="font-mono">{binding.current_revision || '-'}</dd></div><div><dt className="text-muted-foreground">{t('前序契约版本')}</dt><dd className="font-mono">{binding.previous_revision || '-'}</dd></div>
        <div><dt className="text-muted-foreground">{t('完整尝试 / 失败')}</dt><dd>{integerLabel(binding.complete_attempts ?? '0')} / {integerLabel(binding.failures ?? '0')}</dd></div><div><dt className="text-muted-foreground">{t('开始时间 / 冷却结束（UTC）')}</dt><dd>{dateLabel(binding.started_at)} / {dateLabel(binding.cooldown_until)}</dd></div>
      </dl> : <p className="mt-2 text-sm">{t('此范围尚无绑定。')}</p>}
    </section>
    <section className="space-y-2"><h3 className="font-semibold">{t('不可变版本历史')}</h3>
      <Table><TableHeader><TableRow>{['契约版本', '供应商', '适配器 / 解析器 / 用量版本', '候选修复', '操作'].map(label => <TableHead key={label}>{t(label)}</TableHead>)}</TableRow></TableHeader><TableBody>
        {contracts.map(contract => <TableRow key={contract.revision}><TableCell className="max-w-52 break-all font-mono text-xs">{contract.revision}</TableCell><TableCell>{contract.document.provider}</TableCell><TableCell className="break-all text-xs">{contract.document.adapter_build}<br />{contract.document.parser_revision}<br />{contract.document.usage_revision}</TableCell><TableCell>{contract.document.repair_review ? t('附审核证据') : t('普通契约')}</TableCell><TableCell><Button variant="outline" onClick={() => setSelected(contract.revision)}>{t('查看版本')}</Button></TableCell></TableRow>)}
      </TableBody></Table>
      {!contracts.length && <p className="text-sm">{t('此范围暂无契约版本。')}</p>}
      {contracts.length >= limit && <p className="text-sm text-muted-foreground">{t('已达到历史版本上限，可提高上限至 100；当前结果不代表全部历史。')}</p>}
    </section>
    {detail && <ContractEvidence snapshot={detail} close={() => setSelected(null)} clone={canCreate ? () => { setDocumentJSON(JSON.stringify(detail.document, null, 2)); setMessage(t('已复制为候选草稿，请核对基础版本与独立审核证据。')); } : undefined} />}
    {(canPublish || canQuarantine) && <form onSubmit={event => void transition(event)} className="space-y-3 rounded-lg border p-4"><h3 className="font-semibold">{t('变更绑定状态')}</h3><p className="text-sm text-muted-foreground">{t('发布仍需独立探测和审核签名；晋级需要满足服务端灰度观察门禁。')}</p>
      <div className="grid gap-3 md:grid-cols-2"><label className="text-sm">{t('操作')}<select className={selectClass} aria-label={t('状态变更操作')} value={action} onChange={e => setAction(e.target.value)}>{actions.map(value => <option key={value} value={value}>{actionLabel(value)}</option>)}</select></label>
        {action === 'publish' ? <><label className="text-sm">{t('目标契约版本')}<select className={selectClass} aria-label={t('目标契约版本')} value={targetRevision} onChange={e => setTargetRevision(e.target.value)}>{contracts.map(contract => <option key={contract.revision} value={contract.revision}>{contract.revision}</option>)}</select></label><label className="text-sm">{t('首次灰度比例（1–5%）')}<Input aria-label={t('首次灰度比例（1–5%）')} type="number" min="1" max="5" step="1" value={gray} onChange={e => setGray(e.target.value)} /></label></> : <p className="break-all text-sm">{t('目标契约版本')}：{target || '-'}</p>}
        <label className="text-sm">{t('变更原因')}<Input aria-label={t('变更原因')} maxLength={1024} value={reason} onChange={e => setReason(e.target.value)} /></label>
      </div><Button type="submit" disabled={busy || refreshing || needsRefresh || !binding || !target || !reason.trim()}>{t('提交状态变更')}</Button>
    </form>}
    {canCreate && <form onSubmit={event => void createCandidate(event)} className="space-y-3 rounded-lg border p-4"><h3 className="font-semibold">{t('创建候选契约')}</h3><p className="text-sm text-muted-foreground">{t('填写 API 文档 JSON；value_json 使用 Base64，整数 ID 和版本使用十进制字符串。创建只保存新版本，发布需另行提交。')}</p>
      <label className="block text-sm">{t('候选文档 JSON')}<textarea className="mt-1 min-h-64 w-full rounded-md border bg-background p-3 font-mono text-xs" aria-label={t('候选文档 JSON')} value={documentJSON} onChange={e => setDocumentJSON(e.target.value)} spellCheck={false} /></label><label className="block text-sm">{t('创建原因')}<Input aria-label={t('创建原因')} maxLength={1024} value={createReason} onChange={e => setCreateReason(e.target.value)} /></label><Button type="submit" disabled={busy || refreshing || !createReason.trim()}>{t('创建候选版本')}</Button>
    </form>}
    {message && <p role="status">{message}</p>}{error && <p role="alert">{error}</p>}
  </>;
}

function ContractEvidence({ snapshot, close, clone }: { snapshot: ContractSnapshot; close: () => void; clone?: () => void }) {
  const document = snapshot.document, review = document.repair_review;
  return <section className="space-y-3 rounded-lg border p-4"><div className="flex flex-wrap items-center justify-between gap-2"><h3 className="font-semibold">{t('版本与修复证据')}</h3><div className="flex gap-2">{clone && <Button variant="outline" onClick={clone}>{t('复制为候选草稿')}</Button>}<Button variant="ghost" onClick={close}>{t('关闭')}</Button></div></div>
    <dl className="grid gap-2 break-all text-sm sm:grid-cols-2">{[[t('契约版本'), snapshot.revision], [t('独立夹具证据'), document.fixture_evidence_id], [t('依据版本'), document.basis_revision], [t('快照绑定 / 事件版本'), `${integerLabel(snapshot.binding_revision)} / ${integerLabel(snapshot.incident_revision)}`]].map(([label, value]) => <div key={label}><dt className="text-muted-foreground">{label}</dt><dd>{value}</dd></div>)}</dl>
    {review ? <div className="space-y-2"><h4 className="font-semibold">{t('候选修复审核')}</h4><p className="text-sm text-muted-foreground">{t('签名和 CI 证据由服务端发布门禁验证，证据引用不代表审核通过。')}</p><dl className="grid gap-2 break-all text-sm sm:grid-cols-2">{[[t('提案 ID'), review.proposal_id], [t('基础契约版本'), review.base_revision || '-'], [t('审核绑定 / 事件版本'), `${integerLabel(review.binding_revision)} / ${integerLabel(review.incident_revision)}`], [t('来源版本'), review.source_revision], [t('重放证据'), review.replay_id], [t('独立 CI 证据'), review.ci_evidence_id || t('未提供')], [t('审核人'), review.reviewer || t('未提供')], [t('签名密钥 ID'), review.key_id || t('未提供')], [t('审核有效期（UTC）'), dateLabel(review.expires_at)], [t('审核签名'), review.signature || t('未提供')]].map(([label, value]) => <div key={label}><dt className="text-muted-foreground">{label}</dt><dd>{value}</dd></div>)}</dl></div> : <p className="text-sm">{t('此版本未附候选修复审核。')}</p>}
    <details><summary className="cursor-pointer text-sm">{t('查看完整不可变文档')}</summary><pre className="mt-2 max-h-96 overflow-auto rounded bg-muted p-3 text-xs">{JSON.stringify(document, null, 2)}</pre></details>
  </section>;
}
