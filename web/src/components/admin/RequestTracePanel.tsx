import { useState } from 'react';
import { useAuthorizedQuery } from '@/lib/authorization';
import { adminApiClient } from '@/lib/api';
import { locale, t } from '@/lib/i18n';
import { PermissionButton as Button } from '@/components/admin/PermissionButton';

export interface TimingAttempt { number: number; request_id: string; reservation_id?: string; source_kind?: string; source_id?: number; outcome: string }
export interface TimingSummary {
  observation_id: string; observation_kind: string; connection_id?: string; user_id: number; root_request_id?: string;
  protocol: string; endpoint: string; outcome: string; started_at_ms: number;
  output_duration_ms?: number; handler_duration_ms?: number; rpc_duration_ms?: number; turn_duration_ms?: number;
  first_content_ms?: number; output_complete: boolean; output_outcome?: string; attempts?: TimingAttempt[];
  settlement_snapshot?: { status: string; observed_at_ms: number };
}
export interface TraceList { items: TimingSummary[]; next_page_token?: string; applicable?: boolean }
export interface TraceSpan {
  id: string; parent_id?: string; service: string; phase: string; attempt_number?: number;
  start_offset_ms: number; duration_ms: number; outcome: string; safe_attributes?: Record<string, string>;
}
export interface TraceDetail {
  summary: TimingSummary; detail_state: string; missing_reasons?: string[]; spans: TraceSpan[];
  milestones?: { kind: string; offset_ms: number; output_kind?: string }[];
  detail_retention_hours: number; truncated?: boolean;
  related_operations?: {trace_id: string; span_id: string; phase: string; start_offset_ms: number; duration_ms: number; outcome: string; shared: boolean}[];
  settlement_current?: { status: string; observed_at_ms: number; source: string };
}

function timingDuration(value?: number) {
  return value === undefined || value === null ? t('未观测') : value >= 1000 ? `${(value / 1000).toFixed(2)} s` : `${value.toFixed(2)} ms`;
}

const phaseNames: Record<string, string> = {
  'relay.request': '模型请求', 'request.parse': '读取与校验', 'relay.plan': '鉴权与路由', 'auth.snapshot': '鉴权',
  'relay.attempt': '调用尝试', 'admission.check': '准入检查', 'credential.resolve': '凭证解析',
  'request.transform': '请求转换', 'quota.reserve': '额度预占', 'quota.commit': '结算提交', 'quota.release': '额度释放',
  'upstream.http': '上游请求', 'network.connection': '连接获取', 'network.dns': 'DNS 查询', 'network.connect': 'TCP 建连',
  'network.tls': 'TLS 握手', 'response.transform': '响应转换', 'response.stream': '流传输与输出',
  'retry.backoff': '重试退避', 'route.reselect': '重新选源', 'usage.log': '用量日志', 'health.report': '健康反馈',
  'settlement.worker': '后台结算', 'execution.outbox.worker': '后台事件投递', 'log.flush': '共享日志批写', 'log.batch_item': '日志批次处理',
  'ws.accept': 'WS 握手', 'ws.first_frame': '首帧等待', 'ws.pool.acquire': '连接池获取', 'ws.upstream.dial': '上游 WS 拨号',
  'cache.l1': '内存缓存查询',
};
const milestoneNames: Record<string, string> = { 'output.first_content': '首有效输出', 'upstream.first_byte': '上游首字节', 'upstream.first_body': '上游首正文', 'upstream.first_content': '上游首有效输出', 'upstream.terminal': '上游终态', 'output.terminal': '应用输出终态', 'output.finished': '应用输出结束', 'handler.finished': '处理结束', 'turn.finished': '单轮处理结束', 'rpc.finished': 'RPC 结束' };
const stateNames: Record<string, string> = {
  available: '链路详情可用', pending: '链路正在汇集', partial: '链路部分可用', expired: '链路详情已过期',
  not_recorded: '此请求未采集详细链路', unavailable: '链路查询暂不可用',
};
const outcomeNames: Record<string, string> = { success: '成功', rejected: '已拒绝', failed: '失败', cancelled: '已取消', incomplete: '未完整输出', unknown: '未知' };
const settlementNames: Record<string, string> = { committed: '已入账', enqueued: '已入队', released: '已释放', failed: '失败', not_reserved: '未预占', reserved: '预占中', unknown: '未知' };

export function TraceWaterfall({ detail }: { detail: TraceDetail }) {
  const summary = detail.summary;
  const spans = detail.spans ?? [];
  const min = Math.min(0, ...spans.map(span => span.start_offset_ms));
  const max = Math.max(1, ...spans.map(span => span.start_offset_ms + span.duration_ms));
  const parent = new Map(spans.map(span => [span.id, span.parent_id]));
  const depth = (id: string) => {
    const seen = new Set<string>(); let level = 0; let next = parent.get(id);
    while (next && parent.has(next) && level < 12 && !seen.has(next)) { seen.add(next); level++; next = parent.get(next); }
    return level;
  };
  const duration = summary.observation_kind === 'grpc' ? summary.rpc_duration_ms : summary.observation_kind === 'ws_turn' ? summary.turn_duration_ms : summary.output_duration_ms;
  const durationLabel = summary.observation_kind === 'grpc' ? 'RPC 耗时' : summary.observation_kind === 'ws_turn' ? '单轮耗时' : '应用输出耗时';
  return <div className="space-y-3" aria-label={t('请求调用链')}>
    <div className="grid grid-cols-2 gap-2 rounded-lg border p-3 text-sm sm:grid-cols-5">
      <div>{t(durationLabel)}<div className="font-mono font-semibold">{timingDuration(duration)}</div></div>
      <div>{t('首有效输出')}<div className="font-mono">{timingDuration(summary.first_content_ms)}</div></div>
      <div>{t('重试次数')}<div>{Math.max(0, (summary.attempts?.length ?? 0) - 1)}</div></div>
      <div>{t('结束时结算')}<div>{t(settlementNames[summary.settlement_snapshot?.status ?? 'unknown'] ?? '未知')}</div></div>
      <div>{t('当前结算')}<div>{detail.settlement_current ? t(settlementNames[detail.settlement_current.status] ?? '未知') : t('未读取')}</div></div>
    </div>
    {detail.settlement_current && <p className="text-xs text-muted-foreground">{t('当前结算读取于 {time}（{source}）', { time: new Date(detail.settlement_current.observed_at_ms).toLocaleString(locale()), source: detail.settlement_current.source })}</p>}
    {summary.output_outcome === 'write_failed' && <p className="text-xs text-destructive">{t('客户端写出失败；业务结果与输出完成状态分别记录。')}</p>}
    <p className="text-xs text-muted-foreground">{t(stateNames[detail.detail_state] ?? '未知')} · {t('详细链路保留 {hours} 小时', { hours: detail.detail_retention_hours })}</p>
    {detail.missing_reasons?.length ? <p className="break-words text-xs text-amber-700">{t('未细分或缺失阶段')}：{detail.missing_reasons.join(' · ')}</p> : null}
    <p className="text-xs text-muted-foreground">{t('应用输出完成不代表客户端已收到；父子阶段与流水线可能重叠，不能相加。')}</p>
    {summary.attempts?.map(attempt => <div key={attempt.number} className="flex flex-wrap gap-x-3 rounded border px-2 py-1 text-xs">
      <span>{t('尝试 {number}', { number: attempt.number })}</span>
      <span>{t(attempt.source_kind === 'subscription' ? '订阅账号' : '渠道')} {attempt.source_id || '—'}</span>
      <span>{t(outcomeNames[attempt.outcome] ?? '未知')}</span>
      <span className="break-all font-mono">{attempt.request_id || t('未分配请求标识')}</span>
      <span>{attempt.reservation_id || t('未预占')}</span>
    </div>)}
    {spans.length > 0 && <div className="overflow-x-auto rounded border"><div className="min-w-[620px] space-y-1 p-2">
      <div className="grid grid-cols-[230px_1fr_125px] gap-2 text-xs text-muted-foreground"><span>{t('阶段与服务')}</span><span>{min.toFixed(0)} → {max.toFixed(0)} ms</span><span>{t('起始 / 耗时')}</span></div>
      {spans.map(span => <details key={span.id} className="text-xs">
        <summary className="grid cursor-pointer grid-cols-[230px_1fr_125px] items-center gap-2 rounded py-1 hover:bg-muted/40">
          <span className="min-w-0 truncate" style={{ paddingLeft: depth(span.id) * 8 }} title={`${span.phase} · ${span.service}`}>{t(phaseNames[span.phase] ?? span.phase)}{duration !== undefined && span.start_offset_ms >= duration ? ` · ${t('结束后处理')}` : ''} <span className="text-muted-foreground">{span.service}</span></span>
          <span className="relative block h-4 rounded bg-muted/40"><span className={`absolute top-0.5 h-3 min-w-px rounded ${span.outcome === 'failed' || span.outcome === 'incomplete' ? 'bg-red-500' : span.outcome === 'success' ? 'bg-emerald-500' : 'bg-sky-500'}`} style={{ left: `${(span.start_offset_ms - min) / (max - min) * 100}%`, width: `${Math.min(100, span.duration_ms / (max - min) * 100)}%` }} /></span>
          <span className="font-mono">{span.start_offset_ms.toFixed(1)} / {timingDuration(span.duration_ms)}</span>
        </summary>
        <div className="space-y-1 rounded bg-muted/30 p-2">
          <p>{t('状态')}：{t(outcomeNames[span.outcome] ?? '未知')}</p>
          {span.safe_attributes?.['network.connection.reused'] === 'true' && <p>{t('复用，未新建')}</p>}
          {Object.entries(span.safe_attributes ?? {}).map(([key, value]) => <div key={key} className="break-all font-mono">{key}: {value}</div>)}
        </div>
      </details>)}
    </div></div>}
    {detail.milestones?.length ? <div className="flex flex-wrap gap-2 text-xs">{detail.milestones.map((m, index) => <span key={`${m.kind}-${index}`} className="rounded border px-2 py-1">{t(milestoneNames[m.kind] ?? m.kind)} · {timingDuration(m.offset_ms)} {m.output_kind ?? ''}</span>)}</div> : null}
    {detail.related_operations?.map(operation => <div key={`${operation.trace_id}-${operation.span_id}`} className="rounded border bg-muted/20 p-2 text-xs">{t('共享批次（多请求共享，不重复累计）')} · {timingDuration(operation.duration_ms)} · {t(outcomeNames[operation.outcome] ?? '未知')}</div>)}
    {detail.missing_reasons?.includes('clock_skew') && <p className="text-xs text-amber-700">{t('检测到跨服务时钟偏差，保留实际起始偏移。')}</p>}
    <div className="break-all text-xs text-muted-foreground">{new Date(summary.started_at_ms).toLocaleString(locale())} · {summary.observation_id}{summary.connection_id ? ` · ${t('连接')} ${summary.connection_id}` : ''}</div>
  </div>;
}

export function RequestTracePanel({ ledgerID, observationID }: { ledgerID?: string; observationID?: string }) {
  const [selected, setSelected] = useState('');
  // Trace endpoints return their payload directly, without an API data envelope.
  const candidates = useAuthorizedQuery({ permission: 'log.request.read', queryKey: ['ledger-request-traces', ledgerID], enabled: !!ledgerID,
    queryFn: async () => (await adminApiClient.get<TraceList>(`/log/${ledgerID}/trace`)).data, retry: false });
  const effectiveID = observationID || selected || (candidates.data?.items.length === 1 ? candidates.data.items[0].observation_id : '');
  const query = useAuthorizedQuery({ permission: 'log.request.read', queryKey: ['request-trace', effectiveID], enabled: !!effectiveID,
    queryFn: async () => (await adminApiClient.get<TraceDetail>(`/log/request-traces/${effectiveID}`)).data, retry: false,
    refetchInterval: query => (query.state.data?.detail_state === 'pending' && query.state.dataUpdateCount < 4 ? 3000 : false) });
  if (candidates.isLoading || query.isLoading) return <p className="text-sm">{t('正在读取调用链…')}</p>;
  if (candidates.isError || query.isError) return <p role="alert" className="text-sm text-destructive">{t('调用链暂不可用或无权读取。')}</p>;
  if (candidates.data?.applicable === false) return <p className="text-sm text-muted-foreground">{t('此账单不对应模型调用。')}</p>;
  if (query.data) return <TraceWaterfall detail={query.data} />;
  if (candidates.data && candidates.data.items.length > 1) return <div className="space-y-2"><p>{t('此预留关联多次请求，请选择一次观测。')}</p>{candidates.data.items.map(summary => <Button key={summary.observation_id} variant="outline" className="h-auto w-full justify-start whitespace-normal" onClick={() => setSelected(summary.observation_id)}>{new Date(summary.started_at_ms).toLocaleString(locale())} · {t(outcomeNames[summary.outcome] ?? '未知')} · {summary.observation_id}</Button>)}</div>;
  return <p className="text-sm text-muted-foreground">{t('此请求未采集耗时摘要，历史记录不会推算阶段耗时。')}</p>;
}
