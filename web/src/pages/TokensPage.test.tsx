import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { describe, expect, it } from 'vitest';
import { TokensPage } from './TokensPage';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';

describe('TokensPage', () => {
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

    renderWithQuery(<TokensPage />);

    expect(await screen.findByText('No tokens yet')).toBeInTheDocument();
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

    renderWithQuery(<TokensPage />);

    await user.click(await screen.findByRole('button', { name: 'Create Token' }));
    await user.type(screen.getByLabelText('Token Name'), 'test key');
    await user.click(screen.getByRole('button', { name: 'Create' }));

    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByDisplayValue('test key')).toBeInTheDocument();
    expect(within(dialog).getByDisplayValue(fullKey)).toBeInTheDocument();

    await waitFor(() => {
      expect(screen.getByText(maskedKey)).toBeInTheDocument();
    });
    expect(screen.getByDisplayValue(fullKey)).toBeInTheDocument();

    await user.click(within(dialog).getByRole('button', { name: 'Done' }));

    await waitFor(() => {
      expect(screen.queryByDisplayValue(fullKey)).not.toBeInTheDocument();
    });
    expect(screen.queryByText(fullKey)).not.toBeInTheDocument();
    expect(screen.getByText(maskedKey)).toBeInTheDocument();
  });
});
