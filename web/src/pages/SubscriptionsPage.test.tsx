import { screen } from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, beforeEach } from 'vitest';
import { SubscriptionsPage } from './SubscriptionsPage';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';

describe('SubscriptionsPage', () => {
  beforeEach(() => {
    window.localStorage.setItem('userId', '42');
    server.use(
      http.get('/api/user/limits', () =>
        HttpResponse.json({
          success: true,
          data: {
            user_rpm_limit: 3,
          },
        }),
      ),
    );
  });

  it('renders daily/weekly/monthly quota bars for the active subscription', async () => {
    server.use(
      http.get('/api/v1/subscriptions/progress', () =>
        HttpResponse.json({
          success: true,
          data: {
            id: 7,
            status: 'active',
            starts_at: 1700000000,
            expires_at: 1800000000,
            daily_used: { used: 5, limit: 10, remaining: 5 },
            weekly_used: { used: 2, limit: null, remaining: 0 },
            monthly_used: { used: 20, limit: 100, remaining: 80 },
            remaining_seconds: 86400 * 3,
          },
        }),
      ),
    );

    renderWithQuery(
      <MemoryRouter>
        <SubscriptionsPage />
      </MemoryRouter>,
    );

    expect(await screen.findByText('当前订阅')).toBeInTheDocument();
    expect(screen.getByText('请求频率')).toBeInTheDocument();
    expect(screen.getByText('3 次/分钟')).toBeInTheDocument();
    expect(screen.getByText('$5.00 / $10.00')).toBeInTheDocument();
    expect(screen.getByText('无限制')).toBeInTheDocument(); // weekly limit null
    expect(screen.getByText('剩余 3 天')).toBeInTheDocument();
  });

  it('renders tiny subscription usage without rounding it to zero', async () => {
    server.use(
      http.get('/api/v1/subscriptions/progress', () =>
        HttpResponse.json({
          success: true,
          data: {
            id: 7,
            status: 'active',
            starts_at: 1700000000,
            expires_at: 1800000000,
            daily_used: { used: 0.000002, limit: 50, remaining: 49.999998 },
            weekly_used: null,
            monthly_used: null,
            remaining_seconds: 86400 * 3,
          },
        }),
      ),
    );

    renderWithQuery(
      <MemoryRouter>
        <SubscriptionsPage />
      </MemoryRouter>,
    );

    expect(await screen.findByText('$0.000002 / $50.00')).toBeInTheDocument();
  });

  it('shows an empty state when there is no active subscription', async () => {
    server.use(
      http.get('/api/v1/subscriptions/progress', () =>
        HttpResponse.json({ success: false, message: 'not found' }),
      ),
    );

    renderWithQuery(
      <MemoryRouter>
        <SubscriptionsPage />
      </MemoryRouter>,
    );

    expect(await screen.findByText('暂无活跃订阅')).toBeInTheDocument();
  });
});
