import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { BrowserRouter, createBrowserRouter, RouterProvider } from 'react-router';
import { StrictMode } from 'react';
import { toast } from 'sonner';
import { ProfilePage } from './ProfilePage';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';
import { redirectToURL, type OAuthProvider } from '@/lib/oauth';

vi.mock('@/lib/oauth', async () => {
  const actual = await vi.importActual<typeof import('@/lib/oauth')>('@/lib/oauth');
  return { ...actual, redirectToURL: vi.fn() };
});

function mockProfile(bound: OAuthProvider['id'][] = []) {
  server.use(
    http.get('/api/user/self', () => HttpResponse.json({
      success: true,
      data: { id: 1, username: 'alice', display_name: 'Alice', email: 'alice@example.test', group: 'default', status: 1, role: 1, oauth_providers: bound },
    })),
    http.get('/api/user/dashboard', () => HttpResponse.json({ success: true, data: { balance: 100000, used_amount: 20000 } })),
  );
}

function renderProfile() {
  return renderWithQuery(<BrowserRouter><ProfilePage /></BrowserRouter>);
}

describe('ProfilePage', () => {
  beforeEach(() => {
    window.localStorage.setItem('token', 'session-token');
    mockProfile();
  });

  it('starts OAuth bind with the current session token', async () => {
    const user = userEvent.setup();
    server.use(http.get('/api/oauth/wechat/bind', ({ request }) => {
      expect(request.headers.get('Authorization')).toBe('Bearer session-token');
      return HttpResponse.json({ success: true, data: { auth_url: 'https://oauth.example.test/authorize?state=bind-state' } });
    }));
    renderProfile();
    await user.click(await screen.findByRole('button', { name: '绑定微信' }));
    await waitFor(() => expect(redirectToURL).toHaveBeenCalledWith('https://oauth.example.test/authorize?state=bind-state'));
  });

  it.each(['github', 'google'] as const)('shows the stored %s binding even without a bind route', async provider => {
    mockProfile([provider]);
    renderProfile();
    const button = await screen.findByRole('button', { name: new RegExp(provider, 'i') });
    expect(button).toBeDisabled();
    expect(button.textContent).toContain('已绑定');
    expect(screen.getByRole('button', { name: '绑定微信' })).toBeEnabled();
  });

  it.each(['zh-CN', 'en-US'] as const)('consumes the %s callback once while preserving router state, other params and hash', async language => {
    window.localStorage.setItem('web:language', JSON.stringify(language));
    mockProfile(['github', 'google']);
    const previousURL = window.location.href;
    const previousState = window.history.state;
    window.history.replaceState({ usr: { kept: true }, key: 'oauth-callback', idx: 0 }, '', '/profile?oauth_bind=success&provider=github&keep=1#anchor');
    const router = createBrowserRouter([{ path: '/profile', element: <ProfilePage /> }]);
    const success = vi.spyOn(toast, 'success');
    const view = renderWithQuery(<StrictMode><RouterProvider router={router} /></StrictMode>);
    try {
      await waitFor(() => expect(success).toHaveBeenCalledWith(language === 'zh-CN' ? 'GitHub 绑定成功' : 'GitHub linked successfully'));
      await waitFor(() => expect(router.state.location.search).toBe('?keep=1'));
      expect(window.location.search).toBe('?keep=1');
      expect(router.state.location.hash).toBe('#anchor');
      expect(router.state.location.state).toEqual({ kept: true });
      expect(window.history.state.usr).toEqual({ kept: true });
      expect(success).toHaveBeenCalledTimes(1);
      if (language === 'en-US') {
        expect(await screen.findByRole('button', { name: 'Linked to Google' })).toBeDisabled();
        expect(screen.getByRole('button', { name: 'Link WeChat' })).toBeEnabled();
      }
    } finally {
      view.unmount();
      router.dispose();
      window.history.replaceState(previousState, '', previousURL);
    }
  });
});
