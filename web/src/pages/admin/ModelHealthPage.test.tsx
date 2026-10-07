import { act, screen } from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import { MemoryRouter, Outlet, Route, Routes } from 'react-router';
import { describe, expect, it, vi } from 'vitest';
import { AdminRoute } from '@/components/AdminRoute';
import { useAuthorizationLifecycle } from '@/lib/authorization';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';
import { ModelHealthPage } from './ModelHealthPage';

function AuthenticatedShell() {
  useAuthorizationLifecycle();
  return <Outlet />;
}

describe('ModelHealthPage', () => {
  it('keeps a persistent owner 403 visible without repeatedly remounting or requesting the page', async () => {
    const requests = vi.fn();
    const authRequests = vi.fn();
    const snapshot = { authorization_mode: 'iam', session: { activation_state: 'active', revision: '1' }, versions: { policy_revision: '1' }, permitted_operations: ['admin.console.enter', 'monitor.health.model.read'], valid_until: new Date(Date.now() + 60_000).toISOString() };
    server.use(
      http.get('/api/user/authorization', () => { authRequests(); return HttpResponse.json(snapshot); }),
      http.get('/api/admin/model-health', () => { requests(); return HttpResponse.json({ message: 'authorization denied', success: false }, { status: 403 }); }),
    );
    const view = renderWithQuery(
      <MemoryRouter initialEntries={['/admin/model-health']}>
        <Routes>
          <Route element={<AuthenticatedShell />}>
            <Route path="/admin" element={<AdminRoute />}>
              <Route path="model-health" element={<ModelHealthPage />} />
            </Route>
          </Route>
        </Routes>
      </MemoryRouter>,
    );
    try {
      expect(await screen.findByRole('alert')).toHaveTextContent('模型健康数据加载失败');
      await act(async () => { await new Promise(resolve => setTimeout(resolve, 100)); });
      expect(requests).toHaveBeenCalledTimes(1);
      expect(authRequests).toHaveBeenCalledTimes(2);
      expect(screen.queryByText('暂无模型健康数据')).not.toBeInTheDocument();
    } finally { view.unmount(); }
  });
  it('shows a load error instead of an empty-data message when the API fails', async () => {
    server.use(
      http.get('/api/admin/model-health', () => HttpResponse.json({ error: 'unavailable' }, { status: 503 })),
    );

    renderWithQuery(
      <MemoryRouter>
        <ModelHealthPage />
      </MemoryRouter>,
    );

    expect(await screen.findByRole('alert')).toHaveTextContent('模型健康数据加载失败');
    expect(screen.queryByText('暂无模型健康数据')).not.toBeInTheDocument();
  });

  it('renders the model-health controls and status in English', async () => {
    window.localStorage.setItem('web:language', JSON.stringify('en-US'));
    server.use(
      http.get('/api/admin/model-health', () => HttpResponse.json({
        states: [{
          id: 1,
          source_kind: 'channel',
          source_id: 7,
          model_id: 'gpt-4o',
          upstream_model_id: 'gpt-4o-2024-08-06',
          status: 'degraded',
          request_count: 2,
          success_count: 1,
          failure_count: 1,
          consecutive_failures: 1,
          avg_latency_ms: 125,
          last_error: 'upstream 503',
          last_checked_at: 1_700_000_000,
          last_success_at: 1_699_999_000,
          last_failure_at: 1_700_000_000,
        }],
        total: 1,
      })),
    );

    renderWithQuery(
      <MemoryRouter>
        <ModelHealthPage />
      </MemoryRouter>,
    );

    expect(await screen.findByRole('heading', { name: 'Model Health Monitoring' })).toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: 'Source Filter' })).toHaveDisplayValue('All Sources');
    expect(screen.getByRole('combobox', { name: 'Status Filter' })).toHaveDisplayValue('All statuses');
    expect(screen.getByText('Degraded')).toBeInTheDocument();
  });
});
