import { useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';
import { useQuery } from '@tanstack/react-query';
import { Fingerprint, ShieldCheck, KeyRound } from 'lucide-react';
import { QRCodeSVG } from 'qrcode.react';
import { apiClient, clearUserSession } from '@/lib/api';
import { requestSecurityVerification, showSecurityCompletion } from '@/lib/security-verification';
import { getApiErrorMessage } from '@/lib/api-error';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import type { components } from '@/types/api';
import { t } from '@/lib/i18n';
import { parsePasskeyOptions, passkeyUnavailable, prepareCreationOptions, serializeAttestation } from '@/lib/passkey';

type SecurityReply = components['schemas']['api.identity.v1.SecurityReply'];
type PasskeyCredential = components['schemas']['api.identity.v1.PasskeyCredential'];

export function SecurityCenter() {
  const status = useQuery({ queryKey: ['user-security'], meta: { protected: true }, queryFn: async ({ signal }) => (await apiClient.get<SecurityReply>('/user/security', { signal })).data });
  const passkeys = useQuery({ queryKey: ['user-passkeys'], meta: { protected: true }, retry: false, enabled: !passkeyUnavailable(), queryFn: async ({ signal }) => (await apiClient.post<SecurityReply>('/user/security/passkeys', {}, { signal })).data });
  const [passkeyName, setPasskeyName] = useState('');
  const [passkeyError, setPasskeyError] = useState('');
  const [passkeyBusy, setPasskeyBusy] = useState(false);
  const [setup, setSetup] = useState<{ secret: string; otpauth_uri: string }>();
  const [code, setCode] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const lock = useRef(false);
  const active = useRef(false);
  useEffect(() => { active.current = true; return () => { active.current = false; }; }, []);

  const manage = async (action: 'setup' | 'confirm' | 'disable' | 'recovery') => {
    if (lock.current) return;
    const originalToken = localStorage.getItem('token');
    const headers = { Authorization: `Bearer ${originalToken}` };
    const isCurrent = () => active.current && localStorage.getItem('token') === originalToken;
    lock.current = true; setBusy(true); setError('');
    try {
      const response = await apiClient.post<SecurityReply>(`/user/security/${action}`, { code }, { headers }).catch(async (error: unknown) => {
        const reason = (error as { response?: { data?: { reason?: string } } }).response?.data?.reason;
        if (!isCurrent() || action === 'confirm' || reason !== 'STEP_UP_REQUIRED') throw error;
        await requestSecurityVerification();
        if (!isCurrent()) throw new Error(t('会话已变化，请重试'), { cause: error });
        return apiClient.post<SecurityReply>(`/user/security/${action}`, { code }, { headers });
      });
      if (!isCurrent()) return;
      setCode('');
      if (action === 'setup') {
        if (!response.data.secret || !response.data.otpauth_uri) throw new Error(t('安全设置失败'));
        setSetup({secret:response.data.secret,otpauth_uri:response.data.otpauth_uri});
      }
      else {
        setSetup(undefined);
        // Keep one-time codes visible until the user explicitly finishes.
        showSecurityCompletion({ codes: response.data.recovery_codes ?? [], disabled: action === 'disable' });
        clearUserSession();
      }
    } catch (error) { setError(getApiErrorMessage(error, t('安全设置失败'))); }
    finally { lock.current = false; setBusy(false); }
  };

  const passkeyRequest = async <T,>(fn: (headers: { Authorization: string }) => Promise<T>): Promise<T> => {
    const originalToken = localStorage.getItem('token');
    const headers = { Authorization: `Bearer ${originalToken}` };
    try {
      return await fn(headers);
    } catch (error: unknown) {
      const reason = (error as { response?: { data?: { reason?: string } } }).response?.data?.reason;
      if (reason !== 'STEP_UP_REQUIRED') throw error;
      await requestSecurityVerification();
      if (!active.current || localStorage.getItem('token') !== originalToken) throw new Error(t('会话已变化，请重试'), { cause: error });
      return fn(headers);
    }
  };

  const addPasskey = async () => {
    if (lock.current || passkeyUnavailable()) return;
    const originalToken = localStorage.getItem('token');
    const isCurrent = () => active.current && localStorage.getItem('token') === originalToken;
    lock.current = true; setPasskeyBusy(true); setPasskeyError('');
    try {
      const began = await passkeyRequest(headers => apiClient.post<SecurityReply>('/user/security/passkey/enroll/begin', { username: passkeyName.trim() }, { headers }));
      if (!isCurrent()) return;
      const options = parsePasskeyOptions<PublicKeyCredentialCreationOptions>(began.data.options);
      const credential = await navigator.credentials.create({ publicKey: prepareCreationOptions(options) }).catch((error: unknown) => {
        if (error instanceof DOMException && (error.name === 'NotAllowedError' || error.name === 'AbortError')) throw new Error(t('未选择通行密钥或操作已取消'), { cause: error });
        throw error;
      });
      if (!(credential instanceof PublicKeyCredential)) throw new Error(t('通行密钥创建失败'));
      if (!isCurrent()) return;
      await passkeyRequest(headers => apiClient.post<SecurityReply>('/user/security/passkey/enroll/complete', { challenge: began.data.ceremony, attestation: serializeAttestation(credential) }, { headers }));
      if (!isCurrent()) return;
      setPasskeyName('');
      toast.success(t('通行密钥已添加'));
      await passkeys.refetch().catch(() => undefined);
    } catch (error) { setPasskeyError(getApiErrorMessage(error, t('通行密钥设置失败'))); }
    finally { lock.current = false; setPasskeyBusy(false); }
  };

  const removePasskey = async (credential: PasskeyCredential) => {
    if (lock.current || !credential.credential_id) return;
    if (!confirm(t(`确认删除通行密钥「${credential.name || t('未命名通行密钥')}」？此操作不可撤销。`))) return;
    const originalToken = localStorage.getItem('token');
    const isCurrent = () => active.current && localStorage.getItem('token') === originalToken;
    lock.current = true; setPasskeyBusy(true); setPasskeyError('');
    try {
      await passkeyRequest(headers => apiClient.post<SecurityReply>('/user/security/passkey/delete', { credential_id: credential.credential_id }, { headers }));
      if (!isCurrent()) return;
      toast.success(t('通行密钥已删除'));
      await passkeys.refetch().catch(() => undefined);
    } catch (error) { setPasskeyError(getApiErrorMessage(error, t('通行密钥删除失败'))); }
    finally { lock.current = false; setPasskeyBusy(false); }
  };

  return <Card id="security-center" className="overflow-hidden">
    <CardHeader className="border-b bg-muted/30"><CardTitle className="flex items-center gap-2"><ShieldCheck className="size-5 text-emerald-600" />{t('安全中心')}</CardTitle></CardHeader>
    <CardContent className="space-y-5 pt-6">
      <div className="flex flex-wrap items-center justify-between gap-4"><div><p className="font-semibold">{t('双重认证（TOTP）')}</p><p className="mt-1 text-sm text-muted-foreground">{status.data?.enabled ? t('已启用：密码和第三方登录均需第二因子。') : t('通过验证器应用保护账号，丢失设备时可使用恢复码。')}</p></div><span className="rounded-full bg-muted px-3 py-1 text-xs font-medium">{status.data?.enabled ? t('已启用') : t('未启用')}</span></div>
      {status.isPending && <p role="status">{t('读取安全状态…')}</p>}
      {status.isError && <div className="flex items-center gap-3"><p role="alert" className="text-sm text-destructive">{t('安全验证暂不可用')}</p><Button variant="outline" onClick={() => void status.refetch()}>{t('重试')}</Button></div>}
      {status.data && !status.data.configured && <p className="text-sm text-muted-foreground">{t('管理员尚未配置认证密钥。')}</p>}
      {status.data?.enabled && <p className="flex items-center gap-2 text-sm text-muted-foreground"><KeyRound className="size-4" />{t('剩余恢复码：{count}', { count: status.data.recovery_codes_remaining ?? 0 })}</p>}
      {!setup && status.data?.configured && <div className="flex flex-wrap gap-2">{status.data.enabled ? <><Button variant="outline" disabled={busy} onClick={() => void manage('recovery')}>{t('重新生成恢复码')}</Button><Button variant="destructive" disabled={busy} onClick={() => void manage('disable')}>{t('禁用双重认证')}</Button></> : <Button disabled={busy} onClick={() => void manage('setup')}>{t('设置双重认证')}</Button>}</div>}
      {setup && <form className="space-y-4 rounded-xl border bg-muted/20 p-4" onSubmit={event => { event.preventDefault(); void manage('confirm'); }}>
        <p className="text-sm text-muted-foreground">{t('使用验证器扫描二维码，或手动添加以下密钥，再输入六位验证码。')}</p>
        <div className="mx-auto w-fit max-w-full rounded-lg bg-white p-3">
          <QRCodeSVG value={setup.otpauth_uri} size={220} marginSize={4} bgColor="#ffffff" fgColor="#000000" title={t('验证器绑定二维码')} role="img" aria-label={t('验证器绑定二维码')} className="h-auto max-w-full" />
        </div>
        <code className="block break-all rounded-lg bg-background p-3 font-mono text-sm tracking-wide select-all">{setup.secret}</code>
        <a className="inline-block text-sm text-primary underline" href={setup.otpauth_uri}>{t('在验证器应用中打开')}</a>
        <div className="space-y-2"><Label htmlFor="enrollment-code">{t('验证器验证码')}</Label><Input id="enrollment-code" value={code} onChange={event => setCode(event.target.value)} inputMode="numeric" pattern="[0-9]{6}" maxLength={6} autoComplete="one-time-code" required /></div>
        <div className="flex gap-2"><Button type="submit" disabled={busy}>{t('验证并启用')}</Button><Button type="button" variant="outline" disabled={busy} onClick={() => { setSetup(undefined); setCode(''); }}>{t('取消')}</Button></div>
      </form>}
      {error && <p role="alert" className="text-sm text-destructive">{error}</p>}

      <div className="border-t pt-5">
        <div className="flex flex-wrap items-center justify-between gap-4"><div><p className="font-semibold">{t('通行密钥（Passkey）')}</p><p className="mt-1 text-sm text-muted-foreground">{t('使用设备指纹或安全密钥免密码登录，登录时仍需通过第二因子验证。')}</p></div></div>
        {passkeyUnavailable() && <p className="mt-3 text-sm text-muted-foreground">{t('当前浏览器不支持通行密钥。')}</p>}
        {!passkeyUnavailable() && passkeys.isError && <p role="alert" className="mt-3 text-sm text-destructive">{t('通行密钥暂不可用')}</p>}
        {!passkeyUnavailable() && passkeys.data && <div className="mt-3 space-y-3">
          {passkeys.data.passkeys?.length ? <ul className="space-y-2">
            {(passkeys.data.passkeys ?? []).map(credential => <li key={credential.credential_id} className="flex flex-wrap items-center justify-between gap-3 rounded-lg border px-3 py-2">
              <div className="flex min-w-0 items-center gap-2"><Fingerprint className="size-4 shrink-0 text-muted-foreground" /><div className="min-w-0"><p className="truncate text-sm font-medium">{credential.name || t('未命名通行密钥')}</p><p className="text-xs text-muted-foreground">{t('最近使用：{time}', { time: Number(credential.last_used_at) > 0 ? new Date(Number(credential.last_used_at)).toLocaleString() : t('从未') })}</p></div></div>
              <Button variant="outline" size="sm" disabled={passkeyBusy} onClick={() => void removePasskey(credential)}>{t('删除')}</Button>
            </li>)}
          </ul> : <p className="text-sm text-muted-foreground">{t('尚未添加通行密钥。')}</p>}
          {passkeys.data.passkey_enabled === false
            ? <p className="text-sm text-muted-foreground">{t('管理员尚未启用通行密钥。')}</p>
            : <form className="flex flex-wrap items-center gap-2" onSubmit={event => { event.preventDefault(); void addPasskey(); }}>
              <Input value={passkeyName} onChange={event => setPasskeyName(event.target.value)} placeholder={t('通行密钥名称（可选）')} maxLength={128} className="w-56" />
              <Button type="submit" variant="outline" disabled={passkeyBusy}>{t('添加通行密钥')}</Button>
            </form>}
        </div>}
        {passkeyError && <p role="alert" className="mt-3 text-sm text-destructive">{passkeyError}</p>}
      </div>
    </CardContent>
  </Card>;
}
