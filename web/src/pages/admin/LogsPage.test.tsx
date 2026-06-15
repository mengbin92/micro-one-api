import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it } from 'vitest';
import { AdminLogsPage } from './LogsPage';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';

describe('AdminLogsPage', () => {
  it('passes time range filters to the log list request', async () => {
    const listRequest = { url: null as URL | null };

    server.use(
      http.get('/api/log', ({ request }) => {
        listRequest.url = new URL(request.url);
        return HttpResponse.json({
          success: true,
          data: {
            logs: [],
            total: 0,
          },
        });
      }),
    );

    renderWithQuery(
      <MemoryRouter initialEntries={['/admin/logs?start_time=1760000000&end_time=1760003600']}>
        <AdminLogsPage />
      </MemoryRouter>,
    );

    await screen.findByText('No logs found');
    await waitFor(() => {
      expect(listRequest.url?.searchParams.get('start_time')).toBe('1760000000');
      expect(listRequest.url?.searchParams.get('end_time')).toBe('1760003600');
    });
  });

  it('opens log details from the billing log table', async () => {
    const user = userEvent.setup();

    server.use(
      http.get('/api/log', () =>
        HttpResponse.json({
          success: true,
          data: {
            logs: [
              {
                id: '123',
                userId: '42',
                type: 'consume',
                amount: '-1000000',
                balanceAfter: '4000000',
                referenceId: 'req-123',
                remark: 'summary row',
                createdAt: '1760000000',
              },
            ],
            total: 1,
          },
        }),
      ),
      http.get('/api/log/123', () =>
        HttpResponse.json({
          success: true,
          data: {
            id: '123',
            type: 'consume',
            user_id: 42,
            username: 'alice',
            source: 'relay',
            request_id: 'req-123',
            model_name: 'gpt-4o-mini',
            token_name: 'prod-key',
            channel: 7,
            quota: 1000000,
            prompt_tokens: 11,
            completion_tokens: 13,
            cache_read_tokens: 5,
            elapsed_time: 250,
            is_stream: true,
            created_at: 1760000000,
            message: 'full relay log message',
          },
        }),
      ),
    );

    renderWithQuery(
      <MemoryRouter>
        <AdminLogsPage />
      </MemoryRouter>,
    );

    await user.click(await screen.findByRole('button', { name: 'View log 123' }));

    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByRole('heading', { name: 'Log Details' })).toBeInTheDocument();
    expect(within(dialog).getByText('gpt-4o-mini')).toBeInTheDocument();
    expect(within(dialog).getByText('prod-key')).toBeInTheDocument();
    expect(within(dialog).getByText('full relay log message')).toBeInTheDocument();
    expect(within(dialog).getByText('250 ms')).toBeInTheDocument();
  });

  it('cleans logs with the current filters and cutoff time', async () => {
    const user = userEvent.setup();
    const deletedRequest = { url: null as URL | null };

    server.use(
      http.get('/api/log', () =>
        HttpResponse.json({
          success: true,
          data: {
            logs: [
              {
                id: '123',
                userId: '42',
                type: 'consume',
                amount: '-1000000',
                balanceAfter: '4000000',
                referenceId: 'req-123',
                remark: 'summary row',
                createdAt: '1760000000',
              },
            ],
            total: 1,
          },
        }),
      ),
      http.delete('/api/log', ({ request }) => {
        deletedRequest.url = new URL(request.url);
        return HttpResponse.json({ success: true, data: { deleted: 1 } });
      }),
    );

    renderWithQuery(
      <MemoryRouter initialEntries={['/admin/logs?user_id=42&type=consume']}>
        <AdminLogsPage />
      </MemoryRouter>,
    );

    await user.click(await screen.findByRole('button', { name: 'Clean' }));

    const dialog = await screen.findByRole('dialog');
    await user.type(within(dialog).getByLabelText('End Time'), '2026-06-15T12:30');
    await user.click(within(dialog).getByRole('button', { name: 'Clean Logs' }));

    expect(await screen.findByText('Billing Logs')).toBeInTheDocument();
    expect(deletedRequest.url).not.toBeNull();
    const deleteURL = deletedRequest.url;
    if (!deleteURL) throw new Error('delete request was not captured');
    expect(deleteURL.searchParams.get('user_id')).toBe('42');
    expect(deleteURL.searchParams.get('type')).toBe('consume');
    expect(Number(deleteURL.searchParams.get('end_time'))).toBeGreaterThan(0);
  });
});
