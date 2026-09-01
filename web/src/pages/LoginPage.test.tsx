import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { http, HttpResponse } from 'msw';
import { useQueryClient, type QueryClient } from '@tanstack/react-query';
import { useEffect } from 'react';
import { LoginPage } from './LoginPage';
import { renderWithQuery } from '@/test/render';
import { redirectToApiPath } from '@/lib/oauth';
import { server } from '@/test/msw/server';

function QueryClientCapture({ onCapture }: { onCapture: (queryClient: QueryClient) => void }) {
  const queryClient = useQueryClient();
  useEffect(() => onCapture(queryClient), [onCapture, queryClient]);
  return null;
}

vi.mock('@/lib/oauth', async () => {
  const actual = await vi.importActual<typeof import('@/lib/oauth')>('@/lib/oauth');
  return {
    ...actual,
    redirectToApiPath: vi.fn(),
  };
});

describe('LoginPage', () => {
  beforeEach(() => {
    vi.mocked(redirectToApiPath).mockClear();
    window.localStorage.clear();
  });

  afterEach(() => window.localStorage.clear());

  it('starts OAuth login from provider buttons', async () => {
    const user = userEvent.setup();

    renderWithQuery(
      <MemoryRouter initialEntries={['/login']}>
        <LoginPage />
      </MemoryRouter>,
    );

    await user.click(screen.getByRole('button', { name: 'GitHub' }));

    expect(redirectToApiPath).toHaveBeenCalledWith('/oauth/github');
  });

  it('clears the previous user identity when a new session starts', async () => {
    const user = userEvent.setup();
    const captureQueryClient = vi.fn<(queryClient: QueryClient) => void>();
    window.localStorage.setItem('token', 'old-token');
    window.localStorage.setItem('userId', '42');
    window.localStorage.setItem('userRole', '10');
    server.use(
      http.post('/api/user/login', () =>
        HttpResponse.json({ success: true, data: { token: 'new-token' } }),
      ),
    );

    renderWithQuery(
      <MemoryRouter initialEntries={['/login']}>
        <LoginPage />
        <QueryClientCapture onCapture={captureQueryClient} />
      </MemoryRouter>,
    );

    await waitFor(() => expect(captureQueryClient).toHaveBeenCalledOnce());
    const queryClient = captureQueryClient.mock.calls[0][0];
    queryClient.setQueryData(['old-session-data'], { secret: true });

    await user.type(screen.getByLabelText('用户名'), 'bob');
    await user.type(screen.getByLabelText('密码'), 'password-1');
    await user.click(screen.getByRole('button', { name: '登录' }));

    await waitFor(() => expect(window.localStorage.getItem('token')).toBe('new-token'));
    expect(window.localStorage.getItem('userId')).toBeNull();
    expect(window.localStorage.getItem('userRole')).toBeNull();
    expect(queryClient.getQueryData(['old-session-data'])).toBeUndefined();
  });

  it('switches between login and registration with accessible tabs', async () => {
    const user = userEvent.setup();

    renderWithQuery(
      <MemoryRouter initialEntries={['/login']}>
        <LoginPage />
      </MemoryRouter>,
    );

    const loginTab = screen.getByRole('tab', { name: '登录' });
    const registerTab = screen.getByRole('tab', { name: '注册' });
    expect(loginTab).toHaveAttribute('aria-selected', 'true');
    expect(screen.queryByLabelText('确认密码')).not.toBeInTheDocument();

    await user.click(registerTab);

    expect(screen.getByRole('tab', { name: '注册' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByLabelText('确认密码')).toHaveAttribute('autocomplete', 'new-password');
    expect(screen.getByRole('checkbox', { name: /我已阅读并同意/ })).not.toBeChecked();
    expect(screen.getByRole('link', { name: '《用户协议》' })).toHaveAttribute('href', '/terms');
    expect(screen.getByRole('link', { name: '《隐私政策》' })).toHaveAttribute('href', '/privacy');

    await user.keyboard('{ArrowLeft}');

    expect(screen.getByRole('tab', { name: '登录' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('tab', { name: '登录' })).toHaveFocus();
  });

  it('requires explicit agreement before registration', async () => {
    const user = userEvent.setup();

    renderWithQuery(
      <MemoryRouter initialEntries={['/register']}>
        <LoginPage />
      </MemoryRouter>,
    );

    await user.type(screen.getByLabelText('用户名'), 'alice');
    await user.type(screen.getByLabelText('密码'), 'password-1');
    await user.type(screen.getByLabelText('确认密码'), 'password-1');
    await user.click(screen.getByRole('button', { name: '注册账号' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('请先阅读并同意用户协议和隐私政策');
  });

  it('translates the registration legal consent and validation in English', async () => {
    const user = userEvent.setup();
    window.localStorage.setItem('web:language', JSON.stringify('en-US'));

    renderWithQuery(
      <MemoryRouter initialEntries={['/register']}>
        <LoginPage />
      </MemoryRouter>,
    );

    expect(screen.getByRole('checkbox', { name: /I have read and agree to/ })).not.toBeChecked();
    expect(screen.getAllByRole('link', { name: 'User Agreement' })).toHaveLength(2);
    expect(screen.getAllByRole('link', { name: 'Privacy Policy' })).toHaveLength(2);

    await user.type(screen.getByLabelText('Username'), 'alice');
    await user.type(screen.getByLabelText('Password'), 'password-1');
    await user.type(screen.getByLabelText('Confirm password'), 'password-1');
    await user.click(screen.getByRole('button', { name: 'Register an account' }));

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Please read and accept the User Agreement and Privacy Policy first',
    );
  });

  it('associates registration validation errors with the form panel', async () => {
    const user = userEvent.setup();

    renderWithQuery(
      <MemoryRouter initialEntries={['/register']}>
        <LoginPage />
      </MemoryRouter>,
    );

    await user.type(screen.getByLabelText('用户名'), 'alice');
    await user.type(screen.getByLabelText('密码'), 'password-1');
    await user.type(screen.getByLabelText('确认密码'), 'password-2');
    await user.click(screen.getByRole('button', { name: '注册账号' }));

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('两次输入的密码不一致');
    expect(screen.getByRole('tabpanel')).toHaveAttribute('aria-labelledby', 'auth-tab-register');
    expect(screen.getByRole('tabpanel').querySelector('form')).toHaveAttribute('aria-describedby', 'auth-error');
  });
});
