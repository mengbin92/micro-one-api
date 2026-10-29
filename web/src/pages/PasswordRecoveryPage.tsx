import { useEffect, useRef, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { Link, useLocation, useNavigate } from 'react-router';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { LanguageToggle } from '@/components/LanguageToggle';
import { useI18n } from '@/hooks/useI18n';
import { useRegistrationPolicy } from '@/hooks/useRegistrationPolicy';
import { useResendCooldown } from '@/hooks/useResendCooldown';
import { usePasswordRecoveryFlow } from '@/hooks/usePasswordRecoveryFlow';
import { apiClient, clearUserSession } from '@/lib/api';
import { getAuthErrorMessage } from '@/lib/auth-error';
import { ensureApiSuccess } from '@/lib/api-response';
import { t } from '@/lib/i18n';

export function PasswordRecoveryPage() {
  const location = useLocation();
  const navigate = useNavigate();
  const resetting = location.pathname === '/reset-password';
  const flow = usePasswordRecoveryFlow();
  const { email, token, password, confirmation, complete } = flow.draft;
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [sent, setSent] = useState(false);
  const submitLock = useRef(false);
  const queryClient = useQueryClient();
  const cooldown = useResendCooldown();
  const policy = useRegistrationPolicy(!resetting);
  useI18n();

  // Once captured, remove the secret from browser history and later referrers.
  useEffect(() => {
    if (resetting && (location.search || location.hash)) navigate('/reset-password', { replace: true });
  }, [resetting, location.search, location.hash, navigate]);

  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    if (submitLock.current || complete) return;
    setError('');
    if (resetting && password !== confirmation) {
      setError(t('两次输入的密码不一致'));
      return;
    }
    if (!resetting && (!policy.data?.email_delivery_configured || policy.isError || cooldown.seconds > 0)) return;
    submitLock.current = true;
    setBusy(true);
    const originalToken = localStorage.getItem('token');
    try {
      if (resetting) {
        const response = await apiClient.post('/user/reset', { email: email.trim(), token: token.trim(), password });
        ensureApiSuccess(response.data, t('密码重置失败'));
        if (!flow.isCurrent()) return;
        if (localStorage.getItem('token') === originalToken) {
          clearUserSession();
          queryClient.clear();
        }
        flow.finish();
      } else {
        const response = await apiClient.get('/reset_password', { params: { email: email.trim() } });
        ensureApiSuccess(response.data, t('发送重置邮件失败'));
        // The same text applies to known and unknown email addresses.
        setSent(true);
        cooldown.start();
      }
    } catch (err) {
      setError(getAuthErrorMessage(err, t(resetting ? '密码重置失败' : '发送重置邮件失败')));
    } finally {
      submitLock.current = false;
      setBusy(false);
    }
  };

  return <main className="flex min-h-screen items-center justify-center bg-background px-4 py-20">
    <div className="absolute right-4 top-4"><LanguageToggle disabled={busy} /></div>
    <Card className="w-full max-w-md shadow-surface-md">
      <CardHeader>
        <CardTitle role="heading" aria-level={1} className="text-2xl">{t(resetting ? '重置密码' : '忘记密码')}</CardTitle>
        <CardDescription>{t(resetting ? '输入邮件中的重置令牌并设置新密码。' : '输入注册邮箱，获取密码重置邮件。')}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-5">
        {complete ? <p role="status" className="text-sm">{t('密码已重置，原有会话已失效，请重新登录。')}</p> : <form className="space-y-4" onSubmit={submit} aria-describedby={error ? 'recovery-error' : undefined}>
          <div className="space-y-2">
            <Label htmlFor="recovery-email">{t('邮箱')}</Label>
            <Input id="recovery-email" type="email" required autoComplete="email" value={email} disabled={busy} onChange={event => { flow.update({ email: event.target.value }); setSent(false); }} />
          </div>
          {resetting && <>
            <div className="space-y-2">
              <Label htmlFor="reset-token">{t('重置令牌')}</Label>
              <Input id="reset-token" required autoComplete="off" value={token} onChange={event => flow.update({ token: event.target.value })} disabled={busy} />
            </div>
            <div className="space-y-2">
              <Label htmlFor="new-password">{t('新密码')}</Label>
              <Input id="new-password" type="password" required minLength={8} autoComplete="new-password" value={password} onChange={event => flow.update({ password: event.target.value })} disabled={busy} />
            </div>
            <div className="space-y-2">
              <Label htmlFor="confirm-new-password">{t('确认新密码')}</Label>
              <Input id="confirm-new-password" type="password" required minLength={8} autoComplete="new-password" value={confirmation} onChange={event => flow.update({ confirmation: event.target.value })} disabled={busy} />
            </div>
          </>}
          {!resetting && policy.isError && <div className="space-y-2"><p role="alert" className="text-sm text-destructive">{t('注册策略暂不可用，请重试。')}</p><Button type="button" variant="outline" onClick={() => void policy.refetch()}>{t('重试')}</Button></div>}
          {!resetting && policy.data && !policy.data.email_delivery_configured && <p role="alert" className="text-sm text-destructive">{t('邮件服务暂不可用，请联系管理员。')}</p>}
          {sent && <p role="status" className="text-sm text-muted-foreground">{t('如果该邮箱关联了账号，您将收到密码重置邮件。')}</p>}
          {error && <p id="recovery-error" role="alert" className="text-sm text-destructive">{error}</p>}
          <Button className="w-full" type="submit" disabled={busy || (!resetting && (cooldown.seconds > 0 || policy.isError || !policy.data?.email_delivery_configured))}>{busy ? t(resetting ? '重置中…' : '发送中…') : !resetting && cooldown.seconds > 0 ? t('{seconds} 秒后可重发', { seconds: cooldown.seconds }) : t(resetting ? '保存新密码' : '发送重置邮件')}</Button>
        </form>}
        <div className="flex flex-wrap justify-between gap-3 text-sm text-primary">
          <Link to="/login" className="hover:underline">{t('返回登录')}</Link>
          {!complete && <Link to={resetting ? '/forgot-password' : '/reset-password'} className="hover:underline">{t(resetting ? '需要重新发送邮件？' : '已有重置令牌？')}</Link>}
        </div>
      </CardContent>
    </Card>
  </main>;
}
