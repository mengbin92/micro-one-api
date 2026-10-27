import { useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { ShieldCheck, KeyRound } from 'lucide-react';
import { apiClient, clearUserSession } from '@/lib/api';
import { requestSecurityVerification, showSecurityCompletion } from '@/lib/security-verification';
import { getApiErrorMessage } from '@/lib/api-error';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import type { components } from '@/types/api';
import { t } from '@/lib/i18n';

type SecurityReply = components['schemas']['api.identity.v1.SecurityReply'];

export function SecurityCenter() {
  const status = useQuery({ queryKey: ['user-security'], queryFn: async () => (await apiClient.get<SecurityReply>('/user/security')).data });
  const [setup, setSetup] = useState<{ secret: string; otpauth_uri: string }>();
  const [code, setCode] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const lock = useRef(false);

  const manage = async (action: 'setup' | 'confirm' | 'disable' | 'recovery') => {
    if (lock.current) return;
    const originalToken = localStorage.getItem('token');
    lock.current = true; setBusy(true); setError('');
    try {
      const response = await apiClient.post<SecurityReply>(`/user/security/${action}`, { code }).catch(async (error: unknown) => {
        const reason = (error as { response?: { data?: { reason?: string } } }).response?.data?.reason;
        if (action === 'confirm' || reason !== 'STEP_UP_REQUIRED') throw error;
        await requestSecurityVerification();
        if (localStorage.getItem('token') !== originalToken) throw new Error(t('会话已变化，请重试'));
        return apiClient.post<SecurityReply>(`/user/security/${action}`, { code });
      });
      if (localStorage.getItem('token') !== originalToken) throw new Error(t('会话已变化，请重试'));
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

  return <Card id="security-center" className="overflow-hidden">
    <CardHeader className="border-b bg-muted/30"><CardTitle className="flex items-center gap-2"><ShieldCheck className="size-5 text-emerald-600" />{t('安全中心')}</CardTitle></CardHeader>
    <CardContent className="space-y-5 pt-6">
      <div className="flex flex-wrap items-center justify-between gap-4"><div><p className="font-semibold">{t('双重认证（TOTP）')}</p><p className="mt-1 text-sm text-muted-foreground">{status.data?.enabled ? t('已启用：密码和第三方登录均需第二因子。') : t('通过验证器应用保护账号，丢失设备时可使用恢复码。')}</p></div><span className="rounded-full bg-muted px-3 py-1 text-xs font-medium">{status.data?.enabled ? t('已启用') : t('未启用')}</span></div>
      {status.isPending && <p role="status">{t('读取安全状态…')}</p>}
      {status.isError && <div className="flex items-center gap-3"><p role="alert" className="text-sm text-destructive">{t('安全验证暂不可用')}</p><Button variant="outline" onClick={() => void status.refetch()}>{t('重试')}</Button></div>}
      {status.data && !status.data.configured && <p className="text-sm text-muted-foreground">{t('管理员尚未配置认证密钥。')}</p>}
      {status.data?.enabled && <p className="flex items-center gap-2 text-sm text-muted-foreground"><KeyRound className="size-4" />{t('剩余恢复码：{count}', { count: status.data.recovery_codes_remaining ?? 0 })}</p>}
      {!setup && status.data?.configured && <div className="flex flex-wrap gap-2">{status.data.enabled ? <><Button variant="outline" disabled={busy} onClick={() => void manage('recovery')}>{t('重新生成恢复码')}</Button><Button variant="destructive" disabled={busy} onClick={() => void manage('disable')}>{t('禁用双重认证')}</Button></> : <Button disabled={busy} onClick={() => void manage('setup')}>{t('设置双重认证')}</Button>}</div>}
      {setup && <form className="space-y-4 rounded-xl border bg-muted/20 p-4" onSubmit={event => { event.preventDefault(); void manage('confirm'); }}><p className="text-sm text-muted-foreground">{t('在验证器中手动添加以下密钥，再输入六位验证码。')}</p><code className="block break-all rounded-lg bg-background p-3 font-mono text-sm tracking-wide select-all">{setup.secret}</code><a className="inline-block text-sm text-primary underline" href={setup.otpauth_uri}>{t('在验证器应用中打开')}</a><div className="space-y-2"><Label htmlFor="enrollment-code">{t('验证器验证码')}</Label><Input id="enrollment-code" value={code} onChange={event => setCode(event.target.value)} inputMode="numeric" pattern="[0-9]{6}" maxLength={6} autoComplete="one-time-code" required /></div><div className="flex gap-2"><Button type="submit" disabled={busy}>{t('验证并启用')}</Button><Button type="button" variant="outline" disabled={busy} onClick={() => { setSetup(undefined); setCode(''); }}>{t('取消')}</Button></div></form>}
      {error && <p role="alert" className="text-sm text-destructive">{error}</p>}

    </CardContent>
  </Card>;
}
