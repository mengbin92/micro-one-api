import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { describe, expect, it, vi } from 'vitest';
import { ProfilePage } from './ProfilePage';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';
import { redirectToURL } from '@/lib/oauth';

vi.mock('@/lib/oauth', async () => {
  const actual = await vi.importActual<typeof import('@/lib/oauth')>('@/lib/oauth');
  return {
    ...actual,
    redirectToURL: vi.fn(),
  };
});

describe('ProfilePage', () => {
  it('starts OAuth bind with the current session token', async () => {
    const user = userEvent.setup();
    window.localStorage.setItem('token', 'session-token');

    server.use(
      http.get('/api/user/self', () =>
        HttpResponse.json({
          success: true,
          data: {
            id: 1,
            username: 'alice',
            display_name: 'Alice',
            email: 'alice@example.test',
            group: 'default',
            status: 1,
            role: 1,
          },
        }),
      ),
      http.get('/api/user/dashboard', () =>
        HttpResponse.json({
          success: true,
          data: {
            balance: 100000,
            used_amount: 20000,
          },
        }),
      ),
      http.get('/api/oauth/wechat/bind', ({ request }) => {
        expect(request.headers.get('Authorization')).toBe('Bearer session-token');
        return HttpResponse.json({
          success: true,
          data: {
            auth_url: 'https://oauth.example.test/authorize?state=bind-state',
          },
        });
      }),
    );

    renderWithQuery(<ProfilePage />);

    await user.click(await screen.findByRole('button', { name: '绑定微信' }));

    await waitFor(() => {
      expect(redirectToURL).toHaveBeenCalledWith('https://oauth.example.test/authorize?state=bind-state');
    });
  });
});
