import { useRef, useState } from 'react';
import { CheckCircle2, Network, ShieldCheck } from 'lucide-react';
import { useLocation, useNavigate } from 'react-router';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { LanguageToggle } from '@/components/LanguageToggle';
import { apiClient } from '@/lib/api';
import { getApiErrorMessage } from '@/lib/api-error';
import { unwrapApiData } from '@/lib/api-response';
import { oauthProviders, redirectToApiPath } from '@/lib/oauth';
import { t } from '@/lib/i18n';

export function LoginPage() {
  const location = useLocation();
  const mode = location.pathname === '/register' ? 'register' : 'login';
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const loginTabRef = useRef<HTMLButtonElement>(null);
  const registerTabRef = useRef<HTMLButtonElement>(null);
  const navigate = useNavigate();

  const selectMode = (nextMode: 'login' | 'register') => {
    if (nextMode !== mode) {
      navigate(nextMode === 'register' ? '/register' : '/login', { replace: true });
    }
    setError('');
    setConfirmPassword('');
  };

  const signIn = async (nextUsername: string) => {
    const response = await apiClient.post('/user/login', {
      username: nextUsername,
      password,
    });

    const data = unwrapApiData<string | { token?: string }>(response.data, t("登录失败"));
    const token = typeof data === 'string' ? data : data?.token;
    if (!token) {
      throw new Error(t("登录失败"));
    }

    localStorage.setItem('token', token);
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    const nextUsername = username.trim();

    if (!nextUsername) {
      setError(t("请输入用户名"));
      return;
    }
    if (mode === 'register' && password !== confirmPassword) {
      setError(t("两次输入的密码不一致"));
      return;
    }

    setLoading(true);

    try {
      if (mode === 'register') {
        const response = await apiClient.post('/user/register', {
          username: nextUsername,
          password,
        });
        unwrapApiData(response.data, t("注册失败"));
        await signIn(nextUsername);
        toast.success(t("账号创建成功"));
      } else {
        await signIn(nextUsername);
        toast.success(t("登录成功"));
      }
      navigate('/dashboard');
    } catch (err: unknown) {
      const message = getApiErrorMessage(err, t("网络错误"));
      setError(message);
      toast.error(message);
    } finally {
      setLoading(false);
    }
  };

  return (
    <main className="grid min-h-screen bg-background lg:grid-cols-[minmax(0,1.05fr)_minmax(420px,0.95fr)]">
      <div className="fixed right-4 top-4 z-10 sm:right-6 sm:top-6">
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
            <p className="text-sm text-white/60">Gateway Console</p>
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

        <p className="relative text-xs text-white/45">Micro-One API · AI gateway operations</p>
      </section>

      <section className="flex min-h-screen items-center justify-center px-4 py-8 sm:px-8 lg:px-12">
        <div className="w-full max-w-md">
          <div className="mb-6 flex items-center gap-3 lg:hidden">
            <img src="/logo-icon.svg" alt="" className="size-11 rounded-xl" />
            <div>
              <p className="font-bold text-foreground">Micro-One API</p>
              <p className="text-xs text-muted-foreground">Gateway Console</p>
            </div>
          </div>

          <Card className="shadow-surface-md">
            <CardHeader className="space-y-5">
              <div>
                <CardTitle className="text-2xl font-bold">
                  {mode === 'login' ? t("欢迎回来") : t("创建账号")}
                </CardTitle>
                <CardDescription className="mt-2">
                  {mode === 'login' ? t("登录后继续管理您的 API 服务。") : t("使用用户名和密码注册，开始使用控制台。")}
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
                <div className="space-y-2">
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
                {error && (
                  <p id="auth-error" role="alert" className="text-sm font-medium text-destructive">
                    {error}
                  </p>
                )}
                <Button type="submit" className="w-full" disabled={loading}>
                  {loading ? (mode === 'login' ? t("登录中…") : t("创建中…")) : mode === 'login' ? t("登录") : t("注册账号")}
                </Button>
                {mode === 'login' && (
                  <>
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
        </div>
      </section>
    </main>
  );
}
