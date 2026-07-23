import { screen, waitFor } from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it } from 'vitest';
import { AdminModelsPage } from './ModelsPage';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';

const model = {
  id: 1,
  model_id: 'gpt-4o',
  display_name: 'GPT-4o',
  provider: 'openai',
  model_type: 'chat',
  status: 1,
  category: 'large-language',
  tier: 'premium',
  is_public: true,
  channel_count: 3,
  subscription_count: 2,
};

const fullModel = {
  model: {
    id: 1,
    model_id: 'gpt-4o',
    display_name: 'GPT-4o',
    description: 'Most capable model',
    provider: 'openai',
    model_type: 'chat',
    context_window: 128000,
    pricing_input: 0.005,
    pricing_output: 0.015,
    status: 1,
    is_public: true,
    capabilities: ['vision', 'function_calling'],
    tags: ['large-context'],
    category: 'large-language',
    tier: 'premium',
    metadata: '',
    created_at: 1700000000,
    updated_at: 1700000000,
    channel_count: 3,
    subscription_count: 2,
  },
  aliases: [{ id: 1, model_pk: 1, alias: 'gpt4o', is_primary: true, created_at: 1700000000 }],
  channel_mappings: [{ id: 1, channel_id: 10, model_pk: 1, enabled: true, priority: 0, config: '', created_at: 1700000000, updated_at: 1700000000 }],
  subscription_mappings: [],
};

describe('AdminModelsPage', () => {
  it('lists models and renders key columns', async () => {
    server.use(
      http.get('/api/admin/models', () =>
        HttpResponse.json({ models: [model], total: 1 }),
      ),
    );

    renderWithQuery(
      <MemoryRouter>
        <AdminModelsPage />
      </MemoryRouter>,
    );

    const cell = await screen.findByText('gpt-4o');
    const row = cell.closest('tr');
    expect(row?.textContent).toContain('GPT-4o');
    expect(row?.textContent).toContain('openai');
    expect(row?.textContent).toContain('启用');
    expect(row?.textContent).toContain('3');
    expect(row?.textContent).toContain('2');
  });

  it('shows empty state when no models exist', async () => {
    server.use(
      http.get('/api/admin/models', () =>
        HttpResponse.json({ models: [], total: 0 }),
      ),
    );

    renderWithQuery(
      <MemoryRouter>
        <AdminModelsPage />
      </MemoryRouter>,
    );

    await waitFor(() => {
      expect(screen.getByText('暂无模型')).toBeInTheDocument();
    });
  });

  it('creates a model via the create dialog', async () => {
    const captured = { body: null as Record<string, unknown> | null };
    server.use(
      http.get('/api/admin/models', () =>
        HttpResponse.json({ models: [], total: 0 }),
      ),
      http.post('/api/admin/models', async ({ request }) => {
        captured.body = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json({ success: true, message: '', model_pk: 42 });
      }),
    );

    const { userEvent } = await import('@testing-library/user-event');
    const user = userEvent.setup();

    renderWithQuery(
      <MemoryRouter>
        <AdminModelsPage />
      </MemoryRouter>,
    );

    await waitFor(() => {
      expect(screen.getByText('暂无模型')).toBeInTheDocument();
    });

    await user.click(screen.getByText('新建模型'));
    await user.type(screen.getByPlaceholderText('如 gpt-4o, claude-3-5-sonnet'), 'claude-3-5-sonnet');
    await user.type(screen.getByPlaceholderText('如 GPT-4o'), 'Claude 3.5 Sonnet');
    await user.click(screen.getByText('创建'));

    await waitFor(() => {
      expect(captured.body).not.toBeNull();
    });
    expect(captured.body?.model_id).toBe('claude-3-5-sonnet');
    expect(captured.body?.display_name).toBe('Claude 3.5 Sonnet');
  });

  it('opens edit dialog with full model data fetched from getModel', async () => {
    const captured = { body: null as Record<string, unknown> | null };
    server.use(
      http.get('/api/admin/models', () =>
        HttpResponse.json({ models: [model], total: 1 }),
      ),
      http.get('/api/admin/models/1', () =>
        HttpResponse.json(fullModel),
      ),
      http.put('/api/admin/models', async ({ request }) => {
        captured.body = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json({ success: true, message: '' });
      }),
    );

    const { userEvent } = await import('@testing-library/user-event');
    const user = userEvent.setup();

    renderWithQuery(
      <MemoryRouter>
        <AdminModelsPage />
      </MemoryRouter>,
    );

    await screen.findByText('gpt-4o');
    await user.click(screen.getByText('编辑'));

    // Wait for the edit dialog to show the description fetched from getModel
    await waitFor(() => {
      expect(screen.getByDisplayValue('Most capable model')).toBeInTheDocument();
    });
    // context_window should be populated
    expect(screen.getByDisplayValue('128000')).toBeInTheDocument();
    // pricing fields should be populated
    expect(screen.getByDisplayValue('0.005')).toBeInTheDocument();
    expect(screen.getByDisplayValue('0.015')).toBeInTheDocument();
  });

  it('deletes a model via confirm dialog', async () => {
    const deleted = { pk: '' };
    server.use(
      http.get('/api/admin/models', () =>
        HttpResponse.json({ models: [model], total: 1 }),
      ),
      http.delete('/api/admin/models/1', () => {
        deleted.pk = '1';
        return HttpResponse.json({ success: true, message: '' });
      }),
    );

    const { userEvent } = await import('@testing-library/user-event');
    const user = userEvent.setup();

    renderWithQuery(
      <MemoryRouter>
        <AdminModelsPage />
      </MemoryRouter>,
    );

    await screen.findByText('gpt-4o');
    // Click the trash button in the row
    const deleteButtons = screen.getAllByRole('button', { name: '' });
    const deleteBtn = deleteButtons.find((btn) => btn.querySelector('svg.lucide-trash-2'));
    await user.click(deleteBtn!);

    // Confirm dialog should appear
    await waitFor(() => {
      expect(screen.getByText('删除模型')).toBeInTheDocument();
    });
    await user.click(screen.getByText('确认'));

    await waitFor(() => {
      expect(deleted.pk).toBe('1');
    });
  });

  it('shows detail panel with aliases and mappings', async () => {
    server.use(
      http.get('/api/admin/models', () =>
        HttpResponse.json({ models: [model], total: 1 }),
      ),
      http.get('/api/admin/models/1', () =>
        HttpResponse.json(fullModel),
      ),
      http.get('/api/admin/models/1/usage-stats', () =>
        HttpResponse.json({ stats: [], total: 0 }),
      ),
    );

    const { userEvent } = await import('@testing-library/user-event');
    const user = userEvent.setup();

    renderWithQuery(
      <MemoryRouter>
        <AdminModelsPage />
      </MemoryRouter>,
    );

    await screen.findByText('gpt-4o');
    await user.click(screen.getByText('详情'));

    // Detail panel should show alias and channel mapping
    await waitFor(() => {
      expect(screen.getByText('gpt4o')).toBeInTheDocument();
    });
    expect(screen.getByText('10')).toBeInTheDocument();
  });

  it('creates an alias via the detail panel', async () => {
    const captured = { body: null as Record<string, unknown> | null };
    server.use(
      http.get('/api/admin/models', () =>
        HttpResponse.json({ models: [model], total: 1 }),
      ),
      http.get('/api/admin/models/1', () =>
        HttpResponse.json(fullModel),
      ),
      http.get('/api/admin/models/1/usage-stats', () =>
        HttpResponse.json({ stats: [], total: 0 }),
      ),
      http.post('/api/admin/models/1/aliases', async ({ request }) => {
        captured.body = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json({ success: true, message: 'ok', alias_id: 99 });
      }),
    );

    const { userEvent } = await import('@testing-library/user-event');
    const user = userEvent.setup();

    renderWithQuery(
      <MemoryRouter>
        <AdminModelsPage />
      </MemoryRouter>,
    );

    await screen.findByText('gpt-4o');
    await user.click(screen.getByText('详情'));

    // Wait for detail panel to load
    await waitFor(() => {
      expect(screen.getByText('gpt4o')).toBeInTheDocument();
    });

    // Type a new alias and add it
    await user.type(screen.getByPlaceholderText('如 gpt4o'), 'gpt4');
    await user.click(screen.getByText('添加'));

    await waitFor(() => {
      expect(captured.body).not.toBeNull();
    });
    expect(captured.body?.alias).toBe('gpt4');
  });

  it('filters by tier via the tier select', async () => {
    const fetched = { tier: '' };
    server.use(
      http.get('/api/admin/models', ({ request }) => {
        const url = new URL(request.url);
        fetched.tier = url.searchParams.get('tier') ?? '';
        return HttpResponse.json({ models: [], total: 0 });
      }),
    );

    const { userEvent } = await import('@testing-library/user-event');
    const user = userEvent.setup();

    renderWithQuery(
      <MemoryRouter>
        <AdminModelsPage />
      </MemoryRouter>,
    );

    await waitFor(() => {
      expect(screen.getByText('暂无模型')).toBeInTheDocument();
    });

    const tierSelect = screen.getByLabelText('等级筛选');
    await user.selectOptions(tierSelect, 'premium');

    await waitFor(() => {
      expect(fetched.tier).toBe('premium');
    });
  });

  it('filters by status via the status select', async () => {
    const fetched = { status: '' };
    server.use(
      http.get('/api/admin/models', ({ request }) => {
        const url = new URL(request.url);
        fetched.status = url.searchParams.get('status') ?? '';
        return HttpResponse.json({ models: [], total: 0 });
      }),
    );

    const { userEvent } = await import('@testing-library/user-event');
    const user = userEvent.setup();

    renderWithQuery(
      <MemoryRouter>
        <AdminModelsPage />
      </MemoryRouter>,
    );

    await waitFor(() => {
      expect(screen.getByText('暂无模型')).toBeInTheDocument();
    });

    const statusSelect = screen.getByLabelText('状态筛选');
    await user.selectOptions(statusSelect, '1');

    await waitFor(() => {
      expect(fetched.status).toBe('1');
    });
  });
});
