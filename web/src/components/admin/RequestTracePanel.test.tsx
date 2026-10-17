import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { describe, expect, it } from 'vitest';
import { TraceWaterfall, RequestTracePanel, type TraceDetail } from './RequestTracePanel';
import { RequestTraceSearchDialog } from './RequestTraceSearchDialog';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';

const detail: TraceDetail = {
  summary: { observation_id: 'observation-1', observation_kind: 'http', protocol: 'sse', user_id: 7, endpoint: '/v1/responses', outcome: 'success', started_at_ms: 1000, output_complete: true, output_duration_ms: 0,
    attempts: [{ number: 1, request_id: 'failed-attempt', source_kind: 'channel', source_id: 1, outcome: 'failed' }, { number: 2, request_id: 'served-attempt', reservation_id: 'reserved', source_kind: 'subscription', source_id: 2, outcome: 'success' }], settlement_snapshot: { status: 'enqueued', observed_at_ms: 1001 } },
  detail_state: 'partial', detail_retention_hours: 48, missing_reasons: ['clock_skew'],
  spans: [{ id: 'root', phase: 'relay.request', service: 'relay', start_offset_ms: 0, duration_ms: 10, outcome: 'success' }, { id: 'failed', parent_id: 'root', phase: 'upstream.http', service: 'relay', start_offset_ms: -2, duration_ms: 3, outcome: 'failed', safe_attributes: { 'network.connection.reused': 'true' } }],
};

describe('request trace presentation', () => {
  it('distinguishes unobserved first content from measured zero and keeps failed attempts', async () => {
    const user = userEvent.setup(); render(<TraceWaterfall detail={detail} />);
    expect(screen.getByText('0.00 ms')).toBeInTheDocument(); expect(screen.getByText('未观测')).toBeInTheDocument();
    expect(screen.getByText('failed-attempt')).toBeInTheDocument(); expect(screen.getByText('served-attempt')).toBeInTheDocument();
    expect(screen.getByText('已入队')).toBeInTheDocument(); expect(screen.queryByText('已入账')).not.toBeInTheDocument();
    expect(screen.getByText('检测到跨服务时钟偏差，保留实际起始偏移。')).toBeInTheDocument();
    expect(screen.getByText(/-2\.0 \/ 3\.00 ms/)).toBeInTheDocument();
    await user.click(screen.getByText('上游请求'));
    expect(screen.getByText('复用，未新建')).toBeInTheDocument();
  });
  it('shows the live settlement state next to the request-end snapshot', () => {
    const withCurrent: TraceDetail = { ...detail, settlement_current: { status: 'committed', observed_at_ms: 2000, source: 'reservation+settlement_task' } };
    render(<TraceWaterfall detail={withCurrent} />);
    expect(screen.getByText('结束时结算')).toBeInTheDocument(); expect(screen.getByText('已入队')).toBeInTheDocument();
    expect(screen.getByText('当前结算')).toBeInTheDocument(); expect(screen.getByText('已入账')).toBeInTheDocument();
    expect(screen.getByText(/reservation\+settlement_task/)).toBeInTheDocument();
  });
  it('leaves the live settlement state unread when billing is unavailable or unauthorized', () => {
    render(<TraceWaterfall detail={detail} />);
    expect(screen.getByText('当前结算')).toBeInTheDocument(); expect(screen.getByText('未读取')).toBeInTheDocument();
  });
  it('uses transport RPC duration without inventing application output completion', () => {
    const grpc: TraceDetail = { ...detail, summary: { ...detail.summary, observation_kind: 'grpc', protocol: 'grpc', output_duration_ms: undefined, rpc_duration_ms: 25 } };
    render(<TraceWaterfall detail={grpc} />); expect(screen.getByText('RPC 耗时')).toBeInTheDocument(); expect(screen.getByText('25.00 ms')).toBeInTheDocument(); expect(screen.queryByText('应用输出耗时')).not.toBeInTheDocument();
  });
  it('loads the single ledger observation using the raw HTTP response contract', async () => {
    const requests: string[] = [];
    server.use(http.get('/api/log/68668/trace', () => HttpResponse.json({ applicable: true, items: [detail.summary], next_page_token: '' })),
      http.get('/api/log/request-traces/:id', ({ params }) => { requests.push(String(params.id)); return HttpResponse.json(detail); }));
    renderWithQuery(<RequestTracePanel ledgerID="68668" />);
    await screen.findByLabelText('请求调用链');
    expect(requests).toEqual(['observation-1']);
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });
  it('searches raw request summaries and opens their raw trace details', async () => {
    const user = userEvent.setup();
    server.use(http.get('/api/log/request-traces', () => HttpResponse.json({ items: [detail.summary], next_page_token: '' })),
      http.get('/api/log/request-traces/:id', () => HttpResponse.json(detail)));
    renderWithQuery(<RequestTraceSearchDialog />);
    await user.click(await screen.findByRole('button', { name: '请求链路查询' }));
    await user.click(screen.getByRole('button', { name: '查询' }));
    await user.click(await screen.findByRole('button', { name: /observation-1/ }));
    await screen.findByLabelText('请求调用链');
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });
  it('still reports an actual permission denial as an error', async () => {
    server.use(http.get('/api/log/68668/trace', () => HttpResponse.json({ success: false, message: 'permission denied' }, { status: 403 })));
    renderWithQuery(<RequestTracePanel ledgerID="68668" />);
    expect(await screen.findByRole('alert')).toHaveTextContent('调用链暂不可用或无权读取。');
  });
  it('requires selection for multiple observations linked to one reservation', async () => {
    const user = userEvent.setup(); const requests: string[] = [];
    server.use(http.get('/api/log/42/trace', () => HttpResponse.json({ applicable: true, items: [detail.summary, { ...detail.summary, observation_id: 'observation-2' }] })),
      http.get('/api/log/request-traces/:id', ({ params }) => { requests.push(String(params.id)); return HttpResponse.json(detail); }));
    renderWithQuery(<RequestTracePanel ledgerID="42" />);
    await screen.findByText('此预留关联多次请求，请选择一次观测。'); expect(requests).toEqual([]);
    const candidate = screen.getByRole('button', { name: /observation-2/ }); await user.click(candidate);
    await screen.findByLabelText('请求调用链'); expect(requests).toEqual(['observation-2']);
    expect(within(screen.getByLabelText('请求调用链')).getByText('failed-attempt')).toBeInTheDocument();
  });
});
