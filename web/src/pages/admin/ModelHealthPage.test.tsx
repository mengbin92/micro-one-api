import { screen } from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import { MemoryRouter } from 'react-router';
import { describe, expect, it } from 'vitest';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';
import { ModelHealthPage } from './ModelHealthPage';

describe('ModelHealthPage', () => {
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
