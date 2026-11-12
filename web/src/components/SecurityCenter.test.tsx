import { act, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { describe, expect, it, vi } from 'vitest';
import { server } from '@/test/msw/server';
import { renderWithQuery } from '@/test/render';
import { SecurityCenter } from './SecurityCenter';
import { SecurityVerificationDialog } from './SecurityVerificationDialog';
import { apiClient } from '@/lib/api';
import { requestSecurityVerification } from '@/lib/security-verification';
import { AUTHORIZATION_REFRESH } from '@/lib/authorization-events';

describe('security center', () => {
  it('does not publish recovery codes or clear a session after leaving the security center', async () => {
    const user = userEvent.setup();
    let finish!: () => void;
    const pending = new Promise<void>(resolve => { finish = resolve; });
    const started = vi.fn();
    const post = vi.spyOn(apiClient, 'post');
    localStorage.setItem('token', 'enrolled-session');
    server.use(
      http.get('/api/user/security', () => HttpResponse.json({ configured: true, enabled: true })),
      http.post('/api/user/security/recovery', async () => {
        started();
        await pending;
        return HttpResponse.json({ recovery_codes: ['late-recovery-secret'] });
      }),
    );
    const view = renderWithQuery(<SecurityCenter />);
    await user.click(await screen.findByRole('button', { name: '重新生成恢复码' }));
    await waitFor(() => expect(started).toHaveBeenCalledOnce());
    view.unmount();
    renderWithQuery(<SecurityVerificationDialog />);
    await act(async () => { finish(); await post.mock.results[0].value; });
    expect(localStorage.getItem('token')).toBe('enrolled-session');
    expect(screen.queryByText('late-recovery-secret')).not.toBeInTheDocument();
  });

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
    expect(screen.queryByRole('img', { name: '验证器绑定二维码' })).not.toBeInTheDocument();
    await user.click(await screen.findByRole('button', { name: '设置双重认证' }));
    await user.type(await screen.findByLabelText('当前密码'), 'current-password');
    await user.click(screen.getByRole('button', { name: '验证并继续' }));
    expect(await screen.findByText('AUTHENTICATORSECRET')).toBeInTheDocument();
    const qr = screen.getByRole('img', { name: '验证器绑定二维码' });
    expect(qr.tagName.toLowerCase()).toBe('svg');
    expect(qr.querySelector('path')).not.toBeNull();
    expect(qr.querySelector('image')).toBeNull();
    expect(screen.getByRole('link', { name: '在验证器应用中打开' })).toHaveAttribute('href', 'otpauth://totp/test?secret=AUTHENTICATORSECRET');
    expect(screen.queryByText('one-time-recovery-code')).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: '取消' }));
    expect(screen.queryByRole('img', { name: '验证器绑定二维码' })).not.toBeInTheDocument();
    expect(screen.queryByText('AUTHENTICATORSECRET')).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: '设置双重认证' }));
    expect(await screen.findByRole('img', { name: '验证器绑定二维码' })).toBeInTheDocument();
    await user.type(screen.getByLabelText('验证器验证码'), '123456');
    await user.click(screen.getByRole('button', { name: '验证并启用' }));
    expect(await screen.findByText('one-time-recovery-code')).toBeInTheDocument();
    expect(localStorage.getItem('token')).toBeNull();
    expect(localStorage.getItem('recovery_codes')).toBeNull();
    expect(screen.queryByText('AUTHENTICATORSECRET')).not.toBeInTheDocument();
    expect(screen.queryByRole('img', { name: '验证器绑定二维码' })).not.toBeInTheDocument();
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


it('never submits an old dialog factor with the replacement session', async () => {
  const user = userEvent.setup();
  const verify = vi.fn();
  localStorage.setItem('token', 'old-session');
  server.use(
    http.get('/api/user/security', () => HttpResponse.json({ enabled: false })),
    http.post('/api/user/security/verify', ({ request }) => {
      verify(request.headers.get('Authorization'));
      return HttpResponse.json({});
    }),
  );
  renderWithQuery(<SecurityVerificationDialog />);
  let result!: Promise<unknown>;
  await act(async () => { result = requestSecurityVerification().catch(error => error); });
  await user.type(await screen.findByLabelText('当前密码'), 'old-session-password');
  localStorage.setItem('token', 'replacement-session');
  await user.click(screen.getByRole('button', { name: '验证并继续' }));
  expect(await result).toBeInstanceOf(Error);
  expect(verify).not.toHaveBeenCalled();
});

it('starts a separate verification when the session changes while a dialog is pending', async () => {
  const user = userEvent.setup();
  localStorage.setItem('token', 'old-session');
  server.use(
    http.get('/api/user/security', ({ request }) => HttpResponse.json({ enabled: request.headers.get('Authorization') === 'Bearer old-session' })),
    http.post('/api/user/security/verify', () => HttpResponse.json({})),
  );
  renderWithQuery(<SecurityVerificationDialog />);
  let first!: Promise<unknown>;
  let next!: Promise<unknown>;
  await act(async () => { first = requestSecurityVerification().catch(error => error); });
  await screen.findByLabelText('验证码或恢复码');
  localStorage.setItem('token', 'replacement-session');
  await act(async () => { next = requestSecurityVerification().catch(error => error); });
  await user.type(await screen.findByLabelText('当前密码'), 'replacement-password');
  await user.click(screen.getByRole('button', { name: '验证并继续' }));
  expect(await first).toBeInstanceOf(Error);
  expect(await next).toBeUndefined();
});

it.each(['storage', 'refresh'])('clears published recovery codes when a replacement session arrives by %s', async event => {
  const user = userEvent.setup();
  localStorage.setItem('token', 'enrolled-session');
  server.use(
    http.get('/api/user/security', () => HttpResponse.json({ configured: true, enabled: true })),
    http.post('/api/user/security/recovery', () => HttpResponse.json({ recovery_codes: ['original-account-recovery-secret'] })),
  );
  renderWithQuery(<><SecurityCenter /><SecurityVerificationDialog /></>);
  await user.click(await screen.findByRole('button', { name: '重新生成恢复码' }));
  expect(await screen.findByText('original-account-recovery-secret')).toBeInTheDocument();
  expect(localStorage.getItem('token')).toBeNull();
  await act(async () => { window.dispatchEvent(new StorageEvent('storage', { key: 'token', newValue: null })); });
  expect(screen.getByText('original-account-recovery-secret')).toBeInTheDocument();
  await act(async () => {
    localStorage.setItem('token', 'replacement-session');
    window.dispatchEvent(event === 'storage'
      ? new StorageEvent('storage', { key: 'token', newValue: 'replacement-session' })
      : new Event(AUTHORIZATION_REFRESH));
  });
  expect(screen.queryByText('original-account-recovery-secret')).not.toBeInTheDocument();
  expect(localStorage.getItem('token')).toBe('replacement-session');
});

it('discards an old verification factor when another tab replaces the session', async () => {
  const user = userEvent.setup();
  localStorage.setItem('token', 'old-session');
  server.use(http.get('/api/user/security', () => HttpResponse.json({ enabled: true })));
  renderWithQuery(<SecurityVerificationDialog />);
  let oldResult!: Promise<unknown>;
  await act(async () => { oldResult = requestSecurityVerification().catch(error => error); });
  await user.type(await screen.findByLabelText('验证码或恢复码'), 'private-old-recovery-code');
  await act(async () => {
    localStorage.setItem('token', 'replacement-session');
    window.dispatchEvent(new StorageEvent('storage', { key: 'token', newValue: 'replacement-session' }));
  });
  expect(screen.queryByLabelText('验证码或恢复码')).not.toBeInTheDocument();
  expect(await oldResult).toBeInstanceOf(Error);
  let newResult!: Promise<unknown>;
  await act(async () => { newResult = requestSecurityVerification().catch(error => error); });
  expect(await screen.findByLabelText('验证码或恢复码')).toHaveValue('');
  await user.click(screen.getByRole('button', { name: '取消' }));
  expect(await newResult).toBeInstanceOf(Error);
});

describe('security center passkeys', () => {
  class FakePublicKeyCredential {
    id = 'cred-9';
    rawId = new Uint8Array([9]);
    type = 'public-key';
    response = {
      clientDataJSON: new Uint8Array([4]),
      attestationObject: new Uint8Array([5]),
      getTransports: () => ['usb'],
    };
    getClientExtensionResults() { return {}; }
  }

  it('enrolls and removes a passkey with step-up re-verification', async () => {
    const user = userEvent.setup();
    Object.defineProperty(navigator, 'credentials', { value: { create: vi.fn(async () => new FakePublicKeyCredential()), get: vi.fn() }, configurable: true });
    Object.defineProperty(window, 'isSecureContext', { value: true, configurable: true });
    vi.stubGlobal('PublicKeyCredential', FakePublicKeyCredential);
    localStorage.setItem('token', 'passkey-session');
    const list = vi.fn(() => HttpResponse.json({ passkey_enabled: true, passkeys: [{ credential_id: 'cred-9', name: '工作手机', created_at: '0', last_used_at: '' }] }));
    const bodies: Record<string, unknown> = {};
    let stepUpPassed = false;
    let completeAttempts = 0;
    server.use(
      http.get('/api/user/security', () => HttpResponse.json({ configured: true, enabled: false })),
      http.post('/api/user/security/passkeys', list),
      http.post('/api/user/security/verify', async ({ request }) => {
        bodies.verify = await request.json();
        stepUpPassed = true;
        return HttpResponse.json({});
      }),
      http.post('/api/user/security/passkey/enroll/begin', async ({ request }) => {
        bodies.begin = await request.json();
        return HttpResponse.json({ options: JSON.stringify({ challenge: 'Y2hhbGxlbmdl', user: { id: 'dXNlcg', name: 'bob', displayName: 'Bob' } }), ceremony: 'enroll-ceremony' });
      }),
      http.post('/api/user/security/passkey/enroll/complete', async ({ request }) => {
        bodies.enrollComplete = await request.json();
        completeAttempts++;
        if (!stepUpPassed && completeAttempts === 1) return HttpResponse.json({ reason: 'STEP_UP_REQUIRED' }, { status: 403 });
        return HttpResponse.json({});
      }),
      http.post('/api/user/security/passkey/delete', async ({ request }) => {
        bodies.delete = await request.json();
        return HttpResponse.json({});
      }),
    );
    renderWithQuery(<><SecurityCenter /><SecurityVerificationDialog /></>);

    await user.type(await screen.findByPlaceholderText('通行密钥名称（可选）'), '工作手机');
    await user.click(screen.getByRole('button', { name: '添加通行密钥' }));
    // Enrollment requires step-up: dialog appears, then the action retries.
    await user.type(await screen.findByLabelText('当前密码'), 'current-password');
    await user.click(screen.getByRole('button', { name: '验证并继续' }));
    await screen.findByText('工作手机');
    expect(bodies.begin).toEqual({ username: '工作手机' });
    const enrolled = bodies.enrollComplete as { challenge?: string; attestation?: string };
    expect(enrolled.challenge).toBe('enroll-ceremony');
    expect(JSON.parse(enrolled.attestation ?? '{}')).toMatchObject({ id: 'cred-9', type: 'public-key' });

    vi.spyOn(window, 'confirm').mockReturnValue(true);
    await user.click(screen.getByRole('button', { name: '删除' }));
    await waitFor(() => expect(bodies.delete).toEqual({ credential_id: 'cred-9' }));
  });
});
