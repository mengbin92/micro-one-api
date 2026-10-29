import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { createMemoryRouter, RouterProvider, useLocation } from 'react-router';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { http, HttpResponse } from 'msw';
import { PasswordRecoveryPage } from './PasswordRecoveryPage';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';
import { defaultRegistrationPolicy } from '@/test/auth-policy';
import { PasswordRecoveryFlowProvider } from '@/components/PasswordRecoveryFlowProvider';

function LocationCapture() {
  const location = useLocation();
  return <output aria-label="current location">{location.pathname}{location.search}{location.hash}</output>;
}

function renderRecovery(entry: string) {
  const router = createMemoryRouter([{ path: '*', element: <><PasswordRecoveryPage /><LocationCapture /></> }], { initialEntries: [entry] });
  return { ...renderWithQuery(<PasswordRecoveryFlowProvider router={router}><RouterProvider router={router} /></PasswordRecoveryFlowProvider>), router };
}

describe('PasswordRecoveryPage', () => {
  beforeEach(() => {
    server.use(http.get('/api/status', () => HttpResponse.json({ success: true, data: defaultRegistrationPolicy })));
  });

  it('uses a neutral acknowledgement for a recovery request through the BFF', async () => {
    const user = userEvent.setup();
    server.use(http.get('/api/reset_password', ({ request }) => {
      expect(new URL(request.url).searchParams.get('email')).toBe('unknown@example.com');
      return HttpResponse.json({ success: true, message: 'reset token sent' });
    }));
    renderRecovery('/forgot-password');
    await user.type(screen.getByLabelText('邮箱'), 'unknown@example.com');
    await user.click(screen.getByRole('button', { name: '发送重置邮件' }));
    expect(await screen.findByText('如果该邮箱关联了账号，您将收到密码重置邮件。')).toHaveAttribute('role', 'status');
    expect(screen.queryByText('reset token sent')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: /秒后可重发/ })).toBeDisabled();
  });

  it('scrubs reset URL secrets, enforces confirmation, then clears the previous browser session', async () => {
    const user = userEvent.setup();
    const reset = vi.fn();
    localStorage.setItem('token', 'previous-session');
    localStorage.setItem('userId', '7');
    server.use(http.post('/api/user/reset', async ({ request }) => {
      reset(await request.json());
      return HttpResponse.json({ success: true });
    }));
    renderRecovery('/reset-password?email=alice%40example.com#token=mail-secret');
    expect(screen.getByLabelText('邮箱')).toHaveValue('alice@example.com');
    expect(screen.getByLabelText('重置令牌')).toHaveValue('mail-secret');
    await waitFor(() => expect(screen.getByLabelText('current location')).toHaveTextContent(/^\/reset-password$/));
    await user.type(screen.getByLabelText('新密码'), 'new-password-1');
    await user.type(screen.getByLabelText('确认新密码'), 'different-password');
    await user.click(screen.getByRole('button', { name: '保存新密码' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('两次输入的密码不一致');
    expect(reset).not.toHaveBeenCalled();
    await user.clear(screen.getByLabelText('确认新密码'));
    await user.type(screen.getByLabelText('确认新密码'), 'new-password-1');
    await user.click(screen.getByRole('button', { name: '保存新密码' }));
    expect(await screen.findByText('密码已重置，原有会话已失效，请重新登录。')).toHaveAttribute('role', 'status');
    expect(reset).toHaveBeenCalledExactlyOnceWith({ email: 'alice@example.com', token: 'mail-secret', password: 'new-password-1' });
    expect(localStorage.getItem('token')).toBeNull();
    expect(localStorage.getItem('userId')).toBeNull();
    expect(screen.queryByLabelText('重置令牌')).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: '返回登录' })).toHaveAttribute('href', '/login');
  });

  it('prevents duplicate in-flight reset submits and exposes an expired-token error', async () => {
    const user = userEvent.setup();
    let resolveRequest!: () => void;
    const pending = new Promise<void>(resolve => { resolveRequest = resolve; });
    const reset = vi.fn();
    server.use(http.post('/api/user/reset', async () => {
      reset();
      await pending;
      return HttpResponse.json({ success: false, message: '重置链接非法或已过期' });
    }));
    renderRecovery('/reset-password?email=alice%40example.com&token=old-secret');
    await user.type(screen.getByLabelText('新密码'), 'new-password-1');
    await user.type(screen.getByLabelText('确认新密码'), 'new-password-1');
    const form = screen.getByRole('button', { name: '保存新密码' }).closest('form')!;
    fireEvent.submit(form);
    fireEvent.submit(form);
    await waitFor(() => expect(reset).toHaveBeenCalledOnce());
    expect(screen.getByRole('button', { name: '重置中…' })).toBeDisabled();
    expect(screen.getByRole('button', { name: '切换至英文' })).toBeDisabled();
    resolveRequest();
    expect(await screen.findByRole('alert')).toHaveTextContent('重置链接非法或已过期');
    expect(screen.getByRole('button', { name: '保存新密码' })).toBeEnabled();
  });

  it('blocks email requests without delivery and translates the recovery form', async () => {
    localStorage.setItem('web:language', JSON.stringify('en-US'));
    server.use(http.get('/api/status', () => HttpResponse.json({ success: true, data: { ...defaultRegistrationPolicy, email_delivery_configured: false } })));
    renderRecovery('/forgot-password');
    expect(await screen.findByRole('alert')).toHaveTextContent('Email delivery is temporarily unavailable. Please contact an administrator.');
    expect(screen.getByRole('button', { name: 'Send reset email' })).toBeDisabled();
    expect(screen.getByRole('heading', { name: 'Forgot password' })).toBeInTheDocument();
  });

  it('ignores an old in-flight reset response after entering another recovery flow', async () => {
    const user = userEvent.setup();
    let resolveRequest!: () => void;
    const pending = new Promise<void>(resolve => { resolveRequest = resolve; });
    const started = vi.fn();
    server.use(http.post('/api/user/reset', async () => {
      started();
      await pending;
      return HttpResponse.json({ success: true });
    }));
    const { router } = renderRecovery('/reset-password?email=alice%40example.com#token=old-flow-token');
    await user.type(screen.getByLabelText('新密码'), 'old-new-password');
    await user.type(screen.getByLabelText('确认新密码'), 'old-new-password');
    await user.click(screen.getByRole('button', { name: '保存新密码' }));
    await waitFor(() => expect(started).toHaveBeenCalledOnce());
    await act(async () => { await router.navigate('/login'); });
    await act(async () => { await router.navigate('/reset-password?email=bob%40example.com#token=new-flow-token'); });
    window.localStorage.setItem('token', 'later-browser-session');
    expect(screen.getByLabelText('邮箱')).toHaveValue('bob@example.com');
    expect(screen.getByLabelText('重置令牌')).toHaveValue('new-flow-token');
    resolveRequest();
    await waitFor(() => expect(screen.getByRole('button', { name: '保存新密码' })).toBeEnabled());
    expect(screen.getByLabelText('重置令牌')).toHaveValue('new-flow-token');
    expect(screen.getByLabelText('邮箱')).toHaveValue('bob@example.com');
    expect(window.localStorage.getItem('token')).toBe('later-browser-session');
    expect(screen.queryByText('密码已重置，原有会话已失效，请重新登录。')).not.toBeInTheDocument();
  });

  it('preserves a replacement session when a reset finishes on the same page', async () => {
    const user = userEvent.setup();
    let resolveRequest!: () => void;
    const pending = new Promise<void>(resolve => { resolveRequest = resolve; });
    const started = vi.fn();
    localStorage.setItem('token', 'original-session');
    server.use(http.post('/api/user/reset', async () => {
      started();
      await pending;
      return HttpResponse.json({ success: true });
    }));
    renderRecovery('/reset-password?email=alice%40example.com#token=mail-secret');
    await user.type(screen.getByLabelText('新密码'), 'new-password-1');
    await user.type(screen.getByLabelText('确认新密码'), 'new-password-1');
    await user.click(screen.getByRole('button', { name: '保存新密码' }));
    await waitFor(() => expect(started).toHaveBeenCalledOnce());
    localStorage.setItem('token', 'replacement-session');
    resolveRequest();
    await screen.findByText('密码已重置，原有会话已失效，请重新登录。');
    expect(localStorage.getItem('token')).toBe('replacement-session');
  });
});
