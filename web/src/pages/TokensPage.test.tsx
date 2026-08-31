import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { beforeEach, describe, expect, it } from 'vitest';
import { MemoryRouter } from 'react-router';
import { TokensPage } from './TokensPage';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';
import { takePlaygroundCredential } from '@/lib/playground-credential';
import { LanguageToggle } from '@/components/LanguageToggle';

function renderTokensPage() {
  return renderWithQuery(
    <MemoryRouter>
      <TokensPage />
    </MemoryRouter>,
  );
}

describe('TokensPage', () => {
  beforeEach(() => {
    server.use(
      http.get('/api/status', () => HttpResponse.json({ success: true, data: {} })),
      http.get('/api/pricing', () => HttpResponse.json({ success: true, data: { prices: [] } })),
    );
  });

  it('switches the page title and empty state together', async () => {
    server.use(
      http.get('/api/token', () => HttpResponse.json({ success: true, data: { items: [], total: 0 } })),
    );
    const user = userEvent.setup();

    renderWithQuery(
      <MemoryRouter>
        <LanguageToggle />
        <TokensPage />
      </MemoryRouter>,
    );

    expect(await screen.findByRole('heading', { name: 'API 密钥' })).toBeInTheDocument();
    expect(await screen.findByText('暂无 API 密钥')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: '切换至英文' }));

    expect(await screen.findByRole('heading', { name: 'API Keys' })).toBeInTheDocument();
    expect(screen.getByText('No API keys yet')).toBeInTheDocument();
    expect(document.body.textContent?.replaceAll('中文', '')).not.toMatch(/[\u3400-\u9fff]/);
  });

  it('does not show unnamed session tokens as API keys', async () => {
    server.use(
      http.get('/api/token', () =>
        HttpResponse.json({
          success: true,
          data: {
            items: [
              {
                id: 1,
                name: '',
                masked_key: 'sess********oken',
                status: 1,
                remain_quota: 1000,
                created_time: 1760000000,
              },
            ],
            total: 1,
          },
        }),
      ),
    );

    renderTokensPage();

    expect(await screen.findByText('暂无 API 密钥')).toBeInTheDocument();
    expect(screen.queryByText('sess********oken')).not.toBeInTheDocument();
  });

  it('shows the full API key only in the creation dialog', async () => {
    const user = userEvent.setup();
    const fullKey = 'sk-full-token-value-created-once';
    const maskedKey = 'sk-f************************once';
    let created = false;

    server.use(
      http.get('/api/token', () =>
        HttpResponse.json({
          success: true,
          data: {
            items: created
              ? [
                  {
                    id: 1,
                    name: 'test key',
                    masked_key: maskedKey,
                    status: 1,
                    remain_quota: 0,
                    created_time: 1760000000,
                  },
                ]
              : [],
            total: created ? 1 : 0,
          },
        }),
      ),
      http.post('/api/token', async () => {
        created = true;
        return HttpResponse.json({
          success: true,
          data: {
            id: 1,
            name: 'test key',
            key: fullKey,
            status: 1,
            remain_quota: 0,
            created_time: 1760000000,
          },
        });
      }),
    );

    renderTokensPage();

    await user.click(await screen.findByRole('button', { name: '创建 Token' }));
    await user.type(screen.getByLabelText('Token 名称'), 'test key');
    await user.click(screen.getByRole('button', { name: '创建' }));

    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByDisplayValue('test key')).toBeInTheDocument();
    expect(within(dialog).getByDisplayValue(fullKey)).toBeInTheDocument();

    await waitFor(() => {
      expect(screen.getByText(maskedKey)).toBeInTheDocument();
    });
    expect(screen.getByDisplayValue(fullKey)).toBeInTheDocument();

    await user.click(within(dialog).getByRole('button', { name: '在在线调试中使用' }));

    await waitFor(() => {
      expect(screen.queryByDisplayValue(fullKey)).not.toBeInTheDocument();
    });
    expect(takePlaygroundCredential()).toBe(fullKey);
    expect(screen.queryByText(fullKey)).not.toBeInTheDocument();
    expect(screen.getByText(maskedKey)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'CC Switch' })).not.toBeInTheDocument();
  });
});
