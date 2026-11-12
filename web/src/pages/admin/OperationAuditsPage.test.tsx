import { fireEvent, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { beforeEach, describe, expect, it } from 'vitest';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';
import { OperationAuditsPage } from './OperationAuditsPage';

describe('OperationAuditsPage', () => {
  beforeEach(() => localStorage.setItem('token', 'operation-audit-test'));

  it('lists business write audits and shows immutable evidence details', async () => {
    const user = userEvent.setup();
    server.use(
      http.get('/api/user/authorization', () => HttpResponse.json({
        authorization_mode: 'iam', session: { activation_state: 'active' },
        permitted_operations: ['admin.console.enter', 'iam.audit.read'],
      })),
      http.get('/api/v1/admin/operation-audits', () => HttpResponse.json({
        success: true,
        data: {
          items: [{
            event_id: 'event-1', actor_user_id: 7, actor_username: 'admin', operation: 'billing.pricing.update', resource_id: '1001',
            decision_versions: '{"policy":"3","catalog":"2"}', reason: 'reviewed', occurred_at: 1_700_000_000_000, result: 'success',
          }],
          total: 1, page: 1, page_size: 20,
        },
      })),
    );

    renderWithQuery(<OperationAuditsPage />);

    expect(await screen.findByRole('heading', { name: '操作审计' })).toBeVisible();
    expect(await screen.findByText('billing.pricing.update')).toBeVisible();
    expect(screen.getByText('admin')).toBeVisible();
    expect(screen.queryByText('#7')).not.toBeInTheDocument();
    expect(screen.getAllByText('成功').length).toBeGreaterThan(0);
    await user.click(screen.getByRole('button', { name: '查看' }));
    expect(screen.getByRole('dialog')).toBeVisible();
    expect(screen.getByText('reviewed')).toBeVisible();
    expect(screen.getByText(/"policy": "3"/)).toBeVisible();
  });

  it('sends exact filters to the read-only audit endpoint', async () => {
    const user = userEvent.setup();
    let requestURL = '';
    server.use(
      http.get('/api/user/authorization', () => HttpResponse.json({
        authorization_mode: 'iam', session: { activation_state: 'active' },
        permitted_operations: ['admin.console.enter', 'iam.audit.read'],
      })),
      http.get('/api/v1/admin/operation-audits', ({ request }) => {
        requestURL = request.url;
        return HttpResponse.json({ success: true, data: { items: [], total: 0, page: 1, page_size: 20 } });
      }),
    );

    renderWithQuery(<OperationAuditsPage />);
    await screen.findByRole('heading', { name: '操作审计' });
    await user.type(screen.getByLabelText('操作人用户 ID'), '7');
    await user.type(screen.getByLabelText('操作代码'), 'pricing');
    await user.selectOptions(screen.getByLabelText('结果'), 'failure');

    expect(await screen.findByText('暂无操作审计记录')).toBeVisible();
    const url = new URL(requestURL);
    expect(url.searchParams.get('actor_user_id')).toBe('7');
    expect(url.searchParams.get('operation')).toBe('pricing');
    expect(url.searchParams.get('result')).toBe('failure');
  });

  it('includes the whole selected end minute in the time range', async () => {
    let requestURL = '';
    server.use(
      http.get('/api/user/authorization', () => HttpResponse.json({
        authorization_mode: 'iam', session: { activation_state: 'active' },
        permitted_operations: ['admin.console.enter', 'iam.audit.read'],
      })),
      http.get('/api/v1/admin/operation-audits', ({ request }) => {
        requestURL = request.url;
        return HttpResponse.json({ success: true, data: { items: [], total: 0, page: 1, page_size: 20 } });
      }),
    );

    renderWithQuery(<OperationAuditsPage />);
    await screen.findByRole('heading', { name: '操作审计' });
    fireEvent.change(screen.getByLabelText('结束时间'), { target: { value: '2026-11-12T10:00' } });

    await waitFor(() => {
      expect(requestURL).not.toBe('');
      expect(new URL(requestURL).searchParams.get('end_time_ms')).not.toBeNull();
    });
    expect(Number(new URL(requestURL).searchParams.get('end_time_ms')))
      .toBe(new Date('2026-11-12T10:00').getTime() + 59_999);
  });
});
