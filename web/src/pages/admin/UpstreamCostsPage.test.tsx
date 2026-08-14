import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { toast } from 'sonner';
import { describe, expect, it, vi } from 'vitest';
import { AdminUpstreamCostsPage } from './UpstreamCostsPage';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';

const MTOK = 1_000_000;

describe('AdminUpstreamCostsPage', () => {
  it('lists canonical upstream cost entries and legacy keys separately', async () => {
    server.use(
      http.get('/api/admin/upstream-costs', () =>
        HttpResponse.json({
          entries: [
            {
              key: 'channel:1:deepseek-v4-flash-0731',
              source_kind: 'channel',
              source_id: 1,
              source_name: 'DeepSeek Main',
              upstream_model_id: 'deepseek-v4-flash-0731',
              public_model_id: 'deepseek-v4-flash-0731',
              input_price: 1.4e-7,
              output_price: 2.8e-7,
            },
          ],
          legacy_keys: [
            {
              key: '1:deepseek-v4-flash',
              source_kind: 'channel',
              source_id: 1,
              source_name: '',
              upstream_model_id: '',
              public_model_id: 'deepseek-v4-flash',
              input_price: 1.2e-7,
              output_price: 2.4e-7,
            },
          ],
          total: 2,
        }),
      ),
    );

    renderWithQuery(<AdminUpstreamCostsPage />);

    expect(await screen.findByText('渠道 1 · DeepSeek Main')).toBeInTheDocument();
    expect(screen.getAllByText('deepseek-v4-flash-0731').length).toBeGreaterThan(0);
    expect(screen.getByText(`$${1.4e-7 * MTOK} / 1M`)).toBeInTheDocument();
    expect(screen.getByText('legacy 键（旧格式，待迁移）· 1 条')).toBeInTheDocument();
    expect(screen.getByText('1:deepseek-v4-flash')).toBeInTheDocument();
  });

  it('shows empty state when no costs are configured', async () => {
    server.use(
      http.get('/api/admin/upstream-costs', () =>
        HttpResponse.json({ entries: [], legacy_keys: [], total: 0 }),
      ),
    );

    renderWithQuery(<AdminUpstreamCostsPage />);

    expect(await screen.findByText('暂无上游成本配置')).toBeInTheDocument();
  });

  it('creates a channel upstream cost via the dialog', async () => {
    const user = userEvent.setup();
    let posted: Record<string, unknown> | null = null;
    server.use(
      http.get('/api/admin/upstream-costs', () => HttpResponse.json({ entries: [], legacy_keys: [], total: 0 })),
      http.post('/api/admin/upstream-costs', async ({ request }) => {
        posted = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json({ success: true });
      }),
    );

    renderWithQuery(<AdminUpstreamCostsPage />);
    await screen.findByText('暂无上游成本配置');

    await user.click(screen.getByRole('button', { name: '添加上游成本' }));
    await user.type(screen.getByLabelText('来源 ID'), '1');
    await user.type(screen.getByLabelText('上游模型 ID'), 'deepseek-v4-flash-0731');
    await user.type(screen.getByLabelText('输入价格（$/1M tokens）'), '0.14');
    await user.type(screen.getByLabelText('输出价格（$/1M tokens）'), '0.28');
    await user.click(screen.getByRole('button', { name: '保存' }));

    await waitFor(() => {
      expect(posted).toEqual({
        source_kind: 'channel',
        source_id: 1,
        upstream_model_id: 'deepseek-v4-flash-0731',
        public_model_id: '',
        input_price: 0.14 / MTOK,
        output_price: 0.28 / MTOK,
      });
    });
  });

  it('validates required fields for channel costs before saving', async () => {
    const user = userEvent.setup();
    const errorSpy = vi.spyOn(toast, 'error').mockImplementation(() => '');
    server.use(
      http.get('/api/admin/upstream-costs', () => HttpResponse.json({ entries: [], legacy_keys: [], total: 0 })),
    );

    renderWithQuery(<AdminUpstreamCostsPage />);
    await screen.findByText('暂无上游成本配置');

    await user.click(screen.getByRole('button', { name: '添加上游成本' }));
    await user.click(screen.getByRole('button', { name: '保存' }));

    await waitFor(() => {
      expect(errorSpy).toHaveBeenCalledWith('渠道/订阅账号成本需要填写来源 ID 和上游模型 ID');
    });
    errorSpy.mockRestore();
  });

  it('deletes an entry after confirmation', async () => {
    const user = userEvent.setup();
    let deletedKey: string | null = null;
    server.use(
      http.get('/api/admin/upstream-costs', () =>
        HttpResponse.json({
          entries: [
            {
              key: 'deepseek-v4-flash-0731',
              source_kind: 'model',
              source_id: 0,
              source_name: '',
              upstream_model_id: '',
              public_model_id: 'deepseek-v4-flash-0731',
              input_price: 1.4e-7,
              output_price: 2.8e-7,
            },
          ],
          legacy_keys: [],
          total: 1,
        }),
      ),
      http.delete('/api/admin/upstream-costs', async ({ request }) => {
        const url = new URL(request.url);
        deletedKey = url.searchParams.get('key');
        return HttpResponse.json({ success: true });
      }),
    );

    renderWithQuery(<AdminUpstreamCostsPage />);
    await screen.findByText('全局默认');

    await user.click(screen.getByRole('button', { name: '删除 deepseek-v4-flash-0731' }));
    await user.click(screen.getByRole('button', { name: '确认删除' }));

    await waitFor(() => {
      expect(deletedKey).toBe('deepseek-v4-flash-0731');
    });
  });

  it('runs a dry-run migration and shows the plan', async () => {
    const user = userEvent.setup();
    const dryRuns: boolean[] = [];
    server.use(
      http.get('/api/admin/upstream-costs', () =>
        HttpResponse.json({
          entries: [],
          legacy_keys: [
            {
              key: '1:deepseek-v4-flash',
              source_kind: 'channel',
              source_id: 1,
              source_name: '',
              upstream_model_id: '',
              public_model_id: 'deepseek-v4-flash',
              input_price: 1.2e-7,
              output_price: 2.4e-7,
            },
          ],
          total: 1,
        }),
      ),
      http.post('/api/admin/upstream-costs/migrate', async ({ request }) => {
        const body = (await request.json()) as { dry_run?: boolean };
        dryRuns.push(body.dry_run ?? true);
        return HttpResponse.json({
          to_rewrite: [
            {
              old_key: '1:deepseek-v4-flash',
              new_key: 'channel:1:deepseek-v4-flash-0731',
              source_id: 1,
              public_model_id: 'deepseek-v4-flash',
              upstream_model_id: 'deepseek-v4-flash-0731',
            },
          ],
          skipped: [],
        });
      }),
    );

    renderWithQuery(<AdminUpstreamCostsPage />);
    await screen.findByText('legacy 键（旧格式，待迁移）· 1 条');

    await user.click(screen.getByRole('button', { name: '迁移 legacy 键' }));

    expect(await screen.findByText(/将重写 1 条/)).toBeInTheDocument();
    expect(screen.getAllByText('1:deepseek-v4-flash').length).toBeGreaterThan(0);
    expect(screen.getByText((content) => content.includes('channel:1:deepseek-v4-flash-0731'))).toBeInTheDocument();

    // Executing the plan must send dry_run=false and close the dialog so the
    // already-applied plan cannot be re-submitted.
    await user.click(screen.getByRole('button', { name: '确认执行迁移' }));

    await waitFor(() => {
      expect(screen.queryByText(/将重写 1 条/)).not.toBeInTheDocument();
    });
    expect(dryRuns).toEqual([true, false]);
  });
});
