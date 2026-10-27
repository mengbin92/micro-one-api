import { act, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { describe, expect, it } from 'vitest';
import { server } from '@/test/msw/server';
import { renderWithQuery } from '@/test/render';
import { SecurityCenter } from './SecurityCenter';
import { SecurityVerificationDialog } from './SecurityVerificationDialog';
import { apiClient } from '@/lib/api';

describe('security center', () => {
  it('verifies before setup and only displays recovery codes after confirmation', async () => {
    const user = userEvent.setup();
    let verified = false;
    localStorage.setItem('token', 'session-before-enrollment');
    server.use(
      http.get('/api/user/security', () => HttpResponse.json({ configured: true, enabled: false })),
      http.post('/api/user/security/verify', async ({ request }) => {
        expect(await request.json()).toEqual({ password: 'current-password' });
        verified = true; return HttpResponse.json({});
      }),
      http.post('/api/user/security/setup', () => {
        if (!verified) return HttpResponse.json({ reason: 'STEP_UP_REQUIRED' }, { status: 403 });
        return HttpResponse.json({ secret: 'AUTHENTICATORSECRET', otpauth_uri: 'otpauth://totp/test?secret=AUTHENTICATORSECRET' });
      }),
      http.post('/api/user/security/confirm', async ({ request }) => {
        expect(await request.json()).toEqual({ code: '123456' });
        return HttpResponse.json({ recovery_codes: ['one-time-recovery-code'] });
      }),
    );
    renderWithQuery(<><SecurityCenter /><SecurityVerificationDialog /></>);
    await user.click(await screen.findByRole('button', { name: '设置双重认证' }));
    await user.type(await screen.findByLabelText('当前密码'), 'current-password');
    await user.click(screen.getByRole('button', { name: '验证并继续' }));
    expect(await screen.findByText('AUTHENTICATORSECRET')).toBeInTheDocument();
    expect(screen.queryByText('one-time-recovery-code')).not.toBeInTheDocument();
    await user.type(screen.getByLabelText('验证器验证码'), '123456');
    await user.click(screen.getByRole('button', { name: '验证并启用' }));
    expect(await screen.findByText('one-time-recovery-code')).toBeInTheDocument();
    expect(localStorage.getItem('token')).toBeNull();
    expect(localStorage.getItem('recovery_codes')).toBeNull();
    expect(screen.queryByText('AUTHENTICATORSECRET')).not.toBeInTheDocument();
  });

  it('retries a denied action only after verification and never retries repeatedly', async () => {
    const user = userEvent.setup();
    let calls = 0;
    localStorage.setItem('token', 'same-session');
    server.use(
      http.get('/api/user/security', () => HttpResponse.json({ enabled: true })),
      http.post('/api/user/security/verify', async ({ request }) => {
        expect(await request.json()).toEqual({ code: '123456' });
        return HttpResponse.json({});
      }),
      http.post('/api/token', () => { calls++; return HttpResponse.json({ reason: 'STEP_UP_REQUIRED' }, { status: 403 }); }),
    );
    renderWithQuery(<SecurityVerificationDialog />);
    let result: Promise<unknown>;
    await act(async () => { result = apiClient.post('/token', {}).catch(error => error); });
    await user.type(await screen.findByLabelText('验证码或恢复码'), '123456');
    await user.click(screen.getByRole('button', { name: '验证并继续' }));
    await result!;
    await waitFor(() => expect(calls).toBe(2));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });
});
