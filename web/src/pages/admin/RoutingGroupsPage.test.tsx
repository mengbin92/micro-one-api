import { screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { describe, expect, it } from 'vitest';
import { AdminRoutingGroupsPage } from './RoutingGroupsPage';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';

const group = { id: 2, key: 'vip', display_name: 'VIP', status: 'disabled', access_mode: 'restricted', model_access_mode: 'all_authorized', description: '' };
describe('AdminRoutingGroupsPage', () => {
  it('keeps model-only grants separate from members and lists mixed source IDs', async () => {
    server.use(http.get('/api/v1/admin/routing-groups', () => HttpResponse.json({ success: true, data: { groups: [group], next_page_token: '' } })),
      http.get('/api/v1/admin/routing-groups/2', () => HttpResponse.json({ success: true, data: { group,
        resources: [{ source_kind: 'channel', source_id: 1, priority: 7, weight: 5 }, { source_kind: 'subscription', source_id: 1, priority: 9, weight: 4 }],
        model_grants: [{ mapping_id: 3, account_id: 2, model: 'managed', upstream_model_id: 'upstream-model', priority: 13, enabled: true, extra_authorization: true }],
      } })),
    );
    renderWithQuery(<AdminRoutingGroupsPage />);
    await userEvent.click(await screen.findByRole('button', { name: '查看分组 vip' }));
    const detail = screen.getByRole('region', { name: '分组详情' });
    expect(await within(detail).findByText('模型级额外授权')).toBeVisible();
    expect(within(detail).getByText('API 渠道')).toBeVisible();
    expect(within(detail).getByText('上游订阅账号')).toBeVisible();
    expect(within(detail).getByText('upstream-model')).toBeVisible();
    expect(screen.queryByRole('button', { name: '删除' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '下一页' })).toBeDisabled();
  });
  it('paginates and binds an exact key filter', async () => {
    const queries: URLSearchParams[] = [];
    server.use(http.get('/api/v1/admin/routing-groups', ({ request }) => {
      const q = new URL(request.url).searchParams; queries.push(q);
      return HttpResponse.json({ success: true, data: { groups: [{ ...group, display_name: q.get('page_token') ? 'Second' : 'First' }], next_page_token: q.get('page_token') ? '' : 'next' } });
    }));
    renderWithQuery(<AdminRoutingGroupsPage />);
    await screen.findByText('First'); await userEvent.click(screen.getByRole('button', { name: '下一页' }));
    await screen.findByText('Second');
    await userEvent.type(screen.getByLabelText('分组键（精确匹配）'), 'a_b%');
    await userEvent.click(screen.getByRole('button', { name: '查询' }));
    await screen.findByText('First');
    expect(queries.at(-1)?.get('filter')).toBe('key = "a_b%"');
    expect(queries.at(-1)?.get('page_token')).toBe('');
  });
  it('shows an explicit error instead of an empty successful list', async () => {
    server.use(http.get('/api/v1/admin/routing-groups', () => HttpResponse.json({ success: false, message: 'unavailable' })));
    renderWithQuery(<AdminRoutingGroupsPage />);
    expect(await screen.findByRole('alert')).toHaveTextContent('分组加载失败');
    expect(screen.queryByText('暂无路由分组')).not.toBeInTheDocument();
  });
});
