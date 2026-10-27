import { useEffect, useRef, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { CheckCircle2, Network, ShieldCheck } from 'lucide-react';
import { Link, useLocation, useNavigate } from 'react-router';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { LanguageToggle } from '@/components/LanguageToggle';
import { apiClient, clearUserSession } from '@/lib/api';
import { getAuthErrorMessage } from '@/lib/auth-error';
import { ensureApiSuccess, unwrapApiData } from '@/lib/api-response';
import { oauthProviders, redirectToApiPath } from '@/lib/oauth';
import { t } from '@/lib/i18n';
import { useI18n } from '@/hooks/useI18n';
import { useRegistrationPolicy } from '@/hooks/useRegistrationPolicy';
import { TurnstileChallenge } from '@/components/TurnstileChallenge';
import { useResendCooldown } from '@/hooks/useResendCooldown';

export function LoginPage() {
  const location = useLocation();
  const mode = location.pathname === '/register' ? 'register' : 'login';
  const [username, setUsername] = useState('');
  const [mfaChallenge, setMfaChallenge] = useState(() => new URLSearchParams(location.hash.slice(1)).get('mfa') ?? '');
  const [mfaCode, setMfaCode] = useState('');
  const [password, setPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [email, setEmail] = useState('');
  const [affCode, setAffCode] = useState(() => {
    const params = new URLSearchParams(location.search);
    return params.get('aff') ?? params.get('aff_code') ?? '';
  });
  const [verificationCode, setVerificationCode] = useState('');
  const [turnstileToken, setTurnstileToken] = useState('');
  const [turnstileAttempt, setTurnstileAttempt] = useState(0);
  const [sending, setSending] = useState(false);
  const cooldown = useResendCooldown();
  const [deliveryMessage, setDeliveryMessage] = useState('');
  const [acceptedLegal, setAcceptedLegal] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const loginTabRef = useRef<HTMLButtonElement>(null);
  const registerTabRef = useRef<HTMLButtonElement>(null);
  const emailRef = useRef<HTMLInputElement>(null);
  const submitLock = useRef(false);
  const sendLock = useRef(false);
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { language } = useI18n();
  const policyQuery = useRegistrationPolicy(mode === 'register');
  const policy = policyQuery.data;
  const invitationVerification = !!affCode.trim();
  const verificationRequired = policy?.email_verification || invitationVerification;
  const registrationBlocked = !policy || policyQuery.isError || !policy.registration_enabled
    || (verificationRequired && !policy.email_delivery_configured)
    || (policy.turnstile_check && !policy.turnstile_site_key);
  const emailRequired = verificationRequired || policy?.email_domain_restriction_enabled;
  useEffect(() => {
    if (location.hash === '#oauth=1') {
      navigate('/login', { replace: true });
      apiClient.post('/user/security/oauth', {}).then(response => {
        const data = unwrapApiData<{ token: string }>(response.data, t('登录失败'));
        if (!data.token) throw new Error(t('登录失败'));
        clearUserSession(); queryClient.clear(); localStorage.setItem('token', data.token);
        navigate('/dashboard', { replace: true });
      }).catch(error => setError(getAuthErrorMessage(error, t('登录失败'))));
    } else if (location.hash.startsWith('#mfa=')) navigate('/login', { replace: true });
  }, [location.hash, navigate, queryClient]);
  const userAgreementLabel = language === 'en-US' ? t("用户协议") : `《${t("用户协议")}》`;
  const privacyPolicyLabel = language === 'en-US' ? t("隐私政策") : `《${t("隐私政策")}》`;

  const selectMode = (nextMode: 'login' | 'register') => {
    if (nextMode !== mode) {
      navigate(nextMode === 'register' ? '/register' : '/login', { replace: true });
    }
    setError('');
    setMfaChallenge(''); setMfaCode('');
    setConfirmPassword('');
    setAcceptedLegal(false);
    setTurnstileToken('');
  };

  const sendVerification = async () => {
    if (sendLock.current || cooldown.seconds > 0 || !policy?.email_delivery_configured) return;
    if (!email.trim() || !emailRef.current?.reportValidity()) {
      setError(t('请输入邮箱'));
      return;
    }
    sendLock.current = true;
    setSending(true);
    setError('');
    setDeliveryMessage('');
    try {
      const response = await apiClient.get('/verification', { params: { email: email.trim() } });
      ensureApiSuccess(response.data, t('发送验证码失败'));
      setVerificationCode('');
      setDeliveryMessage(t('验证码已发送，请查收邮件。'));
      cooldown.start();
    } catch (err) {
      setError(getAuthErrorMessage(err, t('发送验证码失败')));
    } finally {
      sendLock.current = false;
      setSending(false);
    }
  };

  const signIn = async (nextUsername: string) => {
    const response = await apiClient.post('/user/login', {
      username: nextUsername,
      password,
    });

    const data = unwrapApiData<string | { token?: string; mfa_required?: boolean; challenge?: string }>(response.data, t("登录失败"));
    if (typeof data !== 'string' && data.mfa_required && data.challenge) { setMfaChallenge(data.challenge); setPassword(''); return false; }
    const token = typeof data === 'string' ? data : data?.token;
    if (!token) {
      throw new Error(t("登录失败"));
    }

    clearUserSession();
    queryClient.clear();
    localStorage.setItem('token', token);
    return true;
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (submitLock.current) return;
    setError('');
    const nextUsername = username.trim();

    if (!mfaChallenge && !nextUsername) {
      setError(t("请输入用户名"));
      return;
    }
    if (mode === 'register' && password !== confirmPassword) {
      setError(t("两次输入的密码不一致"));
      return;
    }
    if (mode === 'register' && !acceptedLegal) {
      setError(t("请先阅读并同意用户协议和隐私政策"));
      return;
    }
    if (mode === 'register') {
      if (registrationBlocked) return;
      if (emailRequired && !email.trim()) { setError(t('请输入邮箱')); return; }
      if (verificationRequired && !verificationCode.trim()) { setError(t('请输入邮箱验证码')); return; }
      if (policy?.turnstile_check && !turnstileToken) { setError(t('请完成安全验证')); return; }
    }

    submitLock.current = true;
    setLoading(true);

    try {
      if (mfaChallenge) {
        const response = await apiClient.post('/user/security/login', { challenge: mfaChallenge, code: mfaCode });
        if (!response.data.token) throw new Error(t('登录失败'));
        clearUserSession(); queryClient.clear(); localStorage.setItem('token', response.data.token);
        setMfaChallenge(''); setMfaCode('');
        toast.success(t('登录成功')); navigate('/dashboard');
      } else if (mode === 'register') {
        const response = await apiClient.post('/user/register', {
          username: nextUsername,
          password,
          email: email.trim(),
          aff_code: affCode.trim(),
          verification_code: verificationCode.trim(),
          turnstile_token: turnstileToken,
        });
        ensureApiSuccess(response.data, t("注册失败"));
        setPassword('');
        setConfirmPassword('');
        setVerificationCode('');
        toast.success(t('注册成功，请使用新账号登录。'));
        navigate('/login', { replace: true });
      } else {
        if (!await signIn(nextUsername)) return;
        toast.success(t("登录成功"));
        navigate('/dashboard');
      }
    } catch (err: unknown) {
      const message = getAuthErrorMessage(err, t("网络错误"));
      setError(message);
      toast.error(message);
    } finally {
      submitLock.current = false;
      setLoading(false);
      setTurnstileToken('');
      setTurnstileAttempt(attempt => attempt + 1);
    }
  };

  return (
    <main className="grid min-h-screen bg-background lg:grid-cols-[minmax(0,1.05fr)_minmax(420px,0.95fr)]">
      <div className="absolute right-4 top-4 z-10 sm:right-6 sm:top-6">
        <LanguageToggle className="gap-2 bg-background/90 backdrop-blur" />
      </div>
      <section
        aria-hidden="true"
        className="relative hidden overflow-hidden bg-slate-950 px-12 py-14 text-white lg:flex lg:flex-col lg:justify-between"
      >
        <div className="absolute -left-32 top-1/4 size-96 rounded-full bg-blue-500/20 blur-3xl" />
        <div className="absolute -right-24 bottom-0 size-80 rounded-full bg-emerald-400/15 blur-3xl" />
        <div className="relative flex items-center gap-4">
          <img src="/logo-icon.svg" alt="" className="size-14 rounded-2xl" />
          <div>
            <p className="text-xl font-bold">Micro-One API</p>
            <p className="text-sm text-white/60">{t('网关控制台')}</p>
          </div>
        </div>

        <div className="relative max-w-xl space-y-8">
          <div className="space-y-4">
            <p className="text-sm font-semibold tracking-wide text-blue-300">{t("统一 AI 网关控制台")}</p>
            <h1 className="text-5xl font-bold leading-tight tracking-tight">{t("一个入口，管理模型、路由与成本。")}</h1>
            <p className="max-w-lg text-lg leading-8 text-white/65">{t("在清晰一致的工作台中管理 API 密钥、调用用量、订阅账号和上游渠道。")}</p>
          </div>
          <div className="grid gap-4 text-sm text-white/75 sm:grid-cols-3">
            <div className="rounded-2xl border border-white/10 bg-white/5 p-4">
              <Network className="mb-3 size-5 text-blue-300" />{t("统一路由")}</div>
            <div className="rounded-2xl border border-white/10 bg-white/5 p-4">
              <ShieldCheck className="mb-3 size-5 text-emerald-300" />{t("安全访问")}</div>
            <div className="rounded-2xl border border-white/10 bg-white/5 p-4">
              <CheckCircle2 className="mb-3 size-5 text-amber-300" />{t("用量可追踪")}</div>
          </div>
        </div>

        <p className="relative text-xs text-white/45">Micro-One API · {t('AI 网关运营平台')}</p>
      </section>

      <section className="flex min-h-screen items-center justify-center px-4 py-8 sm:px-8 lg:px-12">
        <div className="w-full max-w-md">
          <div className="mb-6 flex items-center gap-3 lg:hidden">
            <img src="/logo-icon.svg" alt="" className="size-11 rounded-xl" />
            <div>
              <p className="font-bold text-foreground">Micro-One API</p>
              <p className="text-xs text-muted-foreground">{t('网关控制台')}</p>
            </div>
          </div>

          <Card className="shadow-surface-md">
            <CardHeader className="space-y-5">
              <div>
                <CardTitle className="text-2xl font-bold">
                  {mode === 'login' ? t("欢迎回来") : t("创建账号")}
                </CardTitle>
                <CardDescription className="mt-2">
                  {mode === 'login' ? t("登录后继续管理您的 API 服务。") : t('填写账号信息，完成验证后开始使用控制台。')}
                </CardDescription>
              </div>

              <div role="tablist" aria-label={t("账号入口")} className="grid grid-cols-2 rounded-xl bg-muted p-1">
                {(['login', 'register'] as const).map((item) => {
                  const selected = mode === item;
                  const label = item === 'login' ? t("登录") : t("注册");
                  return (
                    <button
                      key={item}
                      ref={item === 'login' ? loginTabRef : registerTabRef}
                      id={`auth-tab-${item}`}
                      type="button"
                      role="tab"
                      aria-controls="auth-panel"
                      aria-selected={selected}
                      tabIndex={selected ? 0 : -1}
                      className={selected
                        ? 'rounded-lg bg-card px-3 py-2 text-sm font-semibold text-foreground shadow-sm'
                        : 'rounded-lg px-3 py-2 text-sm font-medium text-muted-foreground hover:text-foreground'}
                      onClick={() => selectMode(item)}
                      onKeyDown={(event) => {
                        if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') {
                          event.preventDefault();
                          const nextMode = item === 'login' ? 'register' : 'login';
                          selectMode(nextMode);
                          (nextMode === 'login' ? loginTabRef : registerTabRef).current?.focus();
                        }
                      }}
                    >
                      {label}
                    </button>
                  );
                })}
              </div>
            </CardHeader>

            <CardContent id="auth-panel" role="tabpanel" aria-labelledby={`auth-tab-${mode}`}>
              <form onSubmit={handleSubmit} className="space-y-4" aria-describedby={error ? 'auth-error' : undefined}>
                {mode === 'register' && <>
                  {policyQuery.isPending && <p role="status" className="text-sm text-muted-foreground">{t('正在读取注册策略…')}</p>}
                  {policyQuery.isError && <div className="space-y-2"><p role="alert" className="text-sm text-destructive">{t('注册策略暂不可用，请重试。')}</p><Button type="button" variant="outline" onClick={() => void policyQuery.refetch()}>{t('重试')}</Button></div>}
                  {policy && !policy.registration_enabled && <p role="status" className="text-sm text-muted-foreground">{t('当前暂不开放注册。')}</p>}
                  {policy?.email_verification && !policy.email_delivery_configured && <p role="alert" className="text-sm text-destructive">{t('邮件服务暂不可用，请联系管理员。')}</p>}
                  {policy && invitationVerification && !policy.email_verification && !policy.email_delivery_configured && <p role="alert" className="text-sm text-destructive">{t('邀请奖励需要验证邮箱；邮件服务不可用，请移除邀请码后注册。')}</p>}
                </>}
                {!mfaChallenge && <><div className="space-y-2">
                  <Label htmlFor="username">{t("用户名")}</Label>
                  <Input
                    id="username"
                    type="text"
                    value={username}
                    onChange={(e) => setUsername(e.target.value)}
                    required
                    autoFocus
                    autoComplete="username"
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="password">{t("密码")}</Label>
                  <Input
                    id="password"
                    type="password"
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    required
                    minLength={mode === 'register' ? 8 : undefined}
                    autoComplete={mode === 'login' ? 'current-password' : 'new-password'}
                  />
                </div>
                </>}
                {mfaChallenge && <div className="space-y-3"><p className="text-sm text-muted-foreground">{t('请输入验证器的六位验证码，或使用一个恢复码。')}</p><Label htmlFor="mfa-login-code">{t('验证码或恢复码')}</Label><Input id="mfa-login-code" value={mfaCode} onChange={event => setMfaCode(event.target.value)} autoComplete="one-time-code" maxLength={32} required autoFocus /><Button type="button" variant="ghost" onClick={() => { setMfaChallenge(''); setMfaCode(''); setError(''); }}>{t('返回登录')}</Button></div>}
                {mode === 'register' && (
                  <div className="space-y-2">
                    <Label htmlFor="confirm-password">{t("确认密码")}</Label>
                    <Input
                      id="confirm-password"
                      type="password"
                      value={confirmPassword}
                      onChange={(e) => setConfirmPassword(e.target.value)}
                      required
                      minLength={8}
                      autoComplete="new-password"
                    />
                  </div>
                )}
                {mode === 'register' && <>
                  <div className="space-y-2">
                    <Label htmlFor="registration-email">{emailRequired ? t('邮箱') : t('邮箱（可选）')}</Label>
                    <Input id="registration-email" ref={emailRef} type="email" autoComplete="email" value={email} required={!!emailRequired} disabled={sending || loading} onChange={event => { setEmail(event.target.value); setVerificationCode(''); setDeliveryMessage(''); }} />
                    {policy?.email_domain_restriction_enabled && <p className="text-xs text-muted-foreground">{t('允许的邮箱域名：{domains}', { domains: policy.email_domain_whitelist.join(', ') })}</p>}
                  </div>
                  <div className="space-y-2">
                    <Label htmlFor="aff-code">{t('邀请码（可选）')}</Label>
                    <Input id="aff-code" value={affCode} onChange={event => setAffCode(event.target.value)} autoComplete="off" />
                  </div>
                  {policy && verificationRequired && <div className="space-y-2">
                    <Label htmlFor="verification-code">{t('邮箱验证码')}</Label>
                    <div className="flex gap-2">
                      <Input id="verification-code" value={verificationCode} onChange={event => setVerificationCode(event.target.value)} required autoComplete="one-time-code" />
                      <Button type="button" variant="outline" disabled={sending || loading || !email.trim() || !policy.email_delivery_configured || cooldown.seconds > 0} onClick={() => void sendVerification()}>{sending ? t('发送中…') : cooldown.seconds > 0 ? t('{seconds} 秒后可重发', { seconds: cooldown.seconds }) : t('发送验证码')}</Button>
                    </div>
                    {deliveryMessage && <p role="status" className="text-sm text-muted-foreground">{deliveryMessage}</p>}
                  </div>}
                  {policy?.turnstile_check && (policy.turnstile_site_key
                    ? <TurnstileChallenge key={turnstileAttempt} siteKey={policy.turnstile_site_key} onToken={setTurnstileToken} />
                    : <p role="alert" className="text-sm text-destructive">{t('安全验证尚未配置，请联系管理员。')}</p>)}
                </>}
                {error && (
                  <p id="auth-error" role="alert" className="text-sm font-medium text-destructive">
                    {error}
                  </p>
                )}
                {mode === 'register' && (
                  <div className="flex items-start gap-2.5 rounded-xl bg-muted/60 p-3 text-xs leading-5 text-muted-foreground">
                    <input
                      id="accepted-legal"
                      type="checkbox"
                      checked={acceptedLegal}
                      onChange={(event) => setAcceptedLegal(event.target.checked)}
                      aria-required="true"
                      className="mt-0.5 size-4 shrink-0 accent-primary"
                    />
                    <label htmlFor="accepted-legal">
                      {t("我已阅读并同意")} <Link to="/terms" target="_blank" className="font-medium text-primary hover:underline">{userAgreementLabel}</Link> {language === 'en-US' ? 'and' : '和'} <Link to="/privacy" target="_blank" className="font-medium text-primary hover:underline">{privacyPolicyLabel}</Link>
                    </label>
                  </div>
                )}
                <Button type="submit" className="w-full" disabled={loading || (mode === 'register' && registrationBlocked)}>
                  {loading ? (mode === 'login' ? t("登录中…") : t("创建中…")) : mode === 'login' ? t("登录") : t("注册账号")}
                </Button>
                {mode === 'login' && !mfaChallenge && (
                  <>
                    <div className="text-right"><Link to="/forgot-password" className="text-sm text-primary hover:underline">{t('忘记密码？')}</Link></div>
                    <div className="flex items-center gap-3 py-1">
                      <div className="h-px flex-1 bg-border" />
                      <span className="text-xs font-semibold text-muted-foreground">{t("或使用第三方账号")}</span>
                      <div className="h-px flex-1 bg-border" />
                    </div>
                    <div className="grid grid-cols-2 gap-2">
                      {oauthProviders.map((provider) => (
                        <Button
                          key={provider.id}
                          type="button"
                          variant="outline"
                          disabled={loading}
                          onClick={() => redirectToApiPath(provider.loginPath)}
                        >
                          {t(provider.label)}
                        </Button>
                      ))}
                    </div>
                  </>
                )}
              </form>
            </CardContent>
          </Card>
          <div className="mt-5 flex justify-center gap-4 text-xs text-muted-foreground">
            <Link to="/terms" className="hover:text-foreground hover:underline">{t("用户协议")}</Link>
            <Link to="/privacy" className="hover:text-foreground hover:underline">{t("隐私政策")}</Link>
          </div>
        </div>
      </section>
    </main>
  );
}
