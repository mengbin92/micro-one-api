import { act, screen, waitFor } from '@testing-library/react';
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
import { defaultRegistrationPolicy } from '@/test/auth-policy';

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
    server.use(http.get('/api/status', () => HttpResponse.json({ success: true, data: defaultRegistrationPolicy })));
  });

  afterEach(() => { window.localStorage.clear(); delete window.turnstile; });

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

  it('submits verified email and invitation fields through the BFF and returns to sign in', async () => {
    const user = userEvent.setup();
    const registration = vi.fn();
    server.use(
      http.get('/api/status', () => HttpResponse.json({ success: true, data: { ...defaultRegistrationPolicy, email_verification: true, email_domain_restriction_enabled: true, email_domain_whitelist: ['example.com'] } })),
      http.get('/api/verification', ({ request }) => {
        expect(new URL(request.url).searchParams.get('email')).toBe('alice@example.com');
        return HttpResponse.json({ success: true, message: 'verification code sent' });
      }),
      http.post('/api/user/register', async ({ request }) => {
        registration(await request.json());
        return HttpResponse.json({ success: true, data: { user_id: 8 } });
      }),
    );
    renderWithQuery(<MemoryRouter initialEntries={['/register?aff=invite-1']}><LoginPage /></MemoryRouter>);

    expect(await screen.findByText('允许的邮箱域名：example.com')).toBeInTheDocument();
    expect(screen.getByLabelText('邮箱')).toBeRequired();
    expect(screen.getByLabelText('邀请码（可选）')).toHaveValue('invite-1');
    await user.type(screen.getByLabelText('用户名'), 'alice');
    await user.type(screen.getByLabelText('密码'), 'password-1');
    await user.type(screen.getByLabelText('确认密码'), 'password-1');
    await user.type(screen.getByLabelText('邮箱'), 'alice@example.com');
    await user.click(screen.getByRole('button', { name: '发送验证码' }));
    expect(await screen.findByText('验证码已发送，请查收邮件。')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /秒后可重发/ })).toBeDisabled();
    await user.type(screen.getByLabelText('邮箱验证码'), 'received-secret');
    await user.click(screen.getByRole('checkbox', { name: /我已阅读并同意/ }));
    await user.click(screen.getByRole('button', { name: '注册账号' }));

    await waitFor(() => expect(registration).toHaveBeenCalledOnce());
    expect(registration).toHaveBeenCalledWith({ username: 'alice', password: 'password-1', email: 'alice@example.com', aff_code: 'invite-1', verification_code: 'received-secret', turnstile_token: '' });
    await waitFor(() => expect(screen.getByRole('tab', { name: '登录' })).toHaveAttribute('aria-selected', 'true'));
    expect(screen.getByLabelText('密码')).toHaveValue('');
  });

  it('blocks unavailable registration policy and allows retry without affecting sign in', async () => {
    const user = userEvent.setup();
    let unavailable = true;
    server.use(http.get('/api/status', () => unavailable ? new HttpResponse(null, { status: 503 }) : HttpResponse.json({ success: true, data: { ...defaultRegistrationPolicy, registration_enabled: false } })));
    renderWithQuery(<MemoryRouter initialEntries={['/register']}><LoginPage /></MemoryRouter>);
    expect(await screen.findByText('注册策略暂不可用，请重试。')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '注册账号' })).toBeDisabled();
    unavailable = false;
    await user.click(screen.getByRole('button', { name: '重试' }));
    expect(await screen.findByText('当前暂不开放注册。')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '注册账号' })).toBeDisabled();
    await user.click(screen.getByRole('tab', { name: '登录' }));
    expect(screen.getByRole('button', { name: '登录' })).toBeEnabled();
    expect(screen.getByRole('link', { name: '忘记密码？' })).toHaveAttribute('href', '/forgot-password');
  });

  it('does not claim delivery succeeded after an email failure', async () => {
    const user = userEvent.setup();
    server.use(
      http.get('/api/status', () => HttpResponse.json({ success: true, data: { ...defaultRegistrationPolicy, email_verification: true } })),
      http.get('/api/verification', () => HttpResponse.json({ success: false, message: 'delivery unavailable' }, { status: 503 })),
    );
    renderWithQuery(<MemoryRouter initialEntries={['/register']}><LoginPage /></MemoryRouter>);
    await user.type(await screen.findByLabelText('邮箱'), 'alice@example.com');
    await user.click(screen.getByRole('button', { name: '发送验证码' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('delivery unavailable');
    expect(screen.queryByText('验证码已发送，请查收邮件。')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '发送验证码' })).toBeEnabled();
  });

  it('requires configured email delivery when verification is mandatory', async () => {
    server.use(http.get('/api/status', () => HttpResponse.json({ success: true, data: { ...defaultRegistrationPolicy, email_verification: true, email_delivery_configured: false } })));
    renderWithQuery(<MemoryRouter initialEntries={['/register']}><LoginPage /></MemoryRouter>);
    expect(await screen.findByText('邮件服务暂不可用，请联系管理员。')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '发送验证码' })).toBeDisabled();
    expect(screen.getByRole('button', { name: '注册账号' })).toBeDisabled();
  });

  it('requires a fresh Turnstile token after expiry and after a rejected registration', async () => {
    const user = userEvent.setup();
    const renderWidget = vi.fn().mockReturnValue('widget-1');
    window.turnstile = { ready: callback => callback(), render: renderWidget, remove: vi.fn() };
    const register = vi.fn();
    server.use(
      http.get('/api/status', () => HttpResponse.json({ success: true, data: { ...defaultRegistrationPolicy, turnstile_check: true, turnstile_site_key: 'public-site-key' } })),
      http.post('/api/user/register', async ({ request }) => { register(await request.json()); return HttpResponse.json({ success: false, message: 'user already exists' }); }),
    );
    renderWithQuery(<MemoryRouter initialEntries={['/register']}><LoginPage /></MemoryRouter>);
    await waitFor(() => expect(renderWidget).toHaveBeenCalledOnce());
    await user.type(screen.getByLabelText('用户名'), 'alice');
    await user.type(screen.getByLabelText('密码'), 'password-1');
    await user.type(screen.getByLabelText('确认密码'), 'password-1');
    await user.click(screen.getByRole('checkbox', { name: /我已阅读并同意/ }));
    act(() => renderWidget.mock.calls[0][1].callback('expired-token'));
    act(() => renderWidget.mock.calls[0][1]['expired-callback']());
    await user.click(screen.getByRole('button', { name: '注册账号' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('请完成安全验证');
    expect(register).not.toHaveBeenCalled();
    act(() => renderWidget.mock.calls[0][1].callback('fresh-token'));
    await user.click(screen.getByRole('button', { name: '注册账号' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('用户名或邮箱已被使用');
    expect(register.mock.calls[0][0].turnstile_token).toBe('fresh-token');
    await waitFor(() => expect(renderWidget).toHaveBeenCalledTimes(2));
    await user.click(screen.getByRole('button', { name: '注册账号' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('请完成安全验证');
    expect(register).toHaveBeenCalledOnce();
  });

  it('requires verification for invitation rewards when ordinary registration does not require email', async () => {
    const user = userEvent.setup();
    renderWithQuery(<MemoryRouter initialEntries={['/register?aff=invite-1']}><LoginPage /></MemoryRouter>);
    expect(await screen.findByLabelText('邮箱验证码')).toBeRequired();
    expect(screen.getByLabelText('邮箱')).toBeRequired();
    await user.clear(screen.getByLabelText('邀请码（可选）'));
    expect(screen.queryByLabelText('邮箱验证码')).not.toBeInTheDocument();
    expect(screen.getByLabelText('邮箱（可选）')).not.toBeRequired();
  });

  it('explains unavailable invitation verification and allows registration after removing the invitation', async () => {
    const user = userEvent.setup();
    server.use(http.get('/api/status', () => HttpResponse.json({ success: true, data: { ...defaultRegistrationPolicy, email_delivery_configured: false } })));
    renderWithQuery(<MemoryRouter initialEntries={['/register?aff=invite-1']}><LoginPage /></MemoryRouter>);
    expect(await screen.findByRole('alert')).toHaveTextContent('邀请奖励需要验证邮箱；邮件服务不可用，请移除邀请码后注册。');
    expect(screen.getByRole('button', { name: '注册账号' })).toBeDisabled();
    await user.clear(screen.getByLabelText('邀请码（可选）'));
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '注册账号' })).toBeEnabled();
  });
});
