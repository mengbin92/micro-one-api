import { useState } from 'react';
import { useLocation, useNavigate } from 'react-router';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { apiClient } from '@/lib/api';
import { getApiErrorMessage } from '@/lib/api-error';
import { unwrapApiData } from '@/lib/api-response';
import { oauthProviders, redirectToApiPath } from '@/lib/oauth';

export function LoginPage() {
  const location = useLocation();
  const [mode, setMode] = useState<'login' | 'register'>(location.pathname === '/register' ? 'register' : 'login');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const navigate = useNavigate();

  const signIn = async (nextUsername: string) => {
    const response = await apiClient.post('/user/login', {
      username: nextUsername,
      password,
    });

    const data = unwrapApiData<string | { token?: string }>(response.data, '登录失败');
    const token = typeof data === 'string' ? data : data?.token;
    if (!token) {
      throw new Error('登录失败');
    }

    localStorage.setItem('token', token);
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    const nextUsername = username.trim();

    if (!nextUsername) {
      setError('请输入用户名');
      return;
    }
    if (mode === 'register' && password !== confirmPassword) {
      setError('两次输入的密码不一致');
      return;
    }

    setLoading(true);

    try {
      if (mode === 'register') {
        const response = await apiClient.post('/user/register', {
          username: nextUsername,
          password,
        });
        unwrapApiData(response.data, '注册失败');
        await signIn(nextUsername);
        toast.success('账号创建成功');
      } else {
        await signIn(nextUsername);
        toast.success('登录成功');
      }
      navigate('/dashboard');
    } catch (err: unknown) {
      const message = getApiErrorMessage(err, '网络错误');
      setError(message);
      toast.error(message);
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="min-h-screen flex items-center justify-center bg-background px-4">
      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle>{mode === 'login' ? '登录' : '注册账号'}</CardTitle>
          <CardDescription>
            {mode === 'login' ? '登录您的 micro-one-api 账号' : '使用用户名和密码注册'}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={handleSubmit} className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="username">用户名</Label>
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
              <Label htmlFor="password">密码</Label>
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
                <Label htmlFor="confirm-password">确认密码</Label>
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
            {error && <p className="text-sm text-destructive">{error}</p>}
            <Button type="submit" className="w-full" disabled={loading}>
              {loading ? (mode === 'login' ? '登录中…' : '创建中…') : mode === 'login' ? '登录' : '注册账号'}
            </Button>
            {mode === 'login' && (
              <>
                <div className="flex items-center gap-3 py-1">
                  <div className="h-px flex-1 bg-border" />
                  <span className="text-xs font-semibold text-muted-foreground">或使用第三方账号</span>
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
                      {provider.label}
                    </Button>
                  ))}
                </div>
              </>
            )}
            <Button
              type="button"
              variant="ghost"
              className="w-full"
              disabled={loading}
              onClick={() => {
                setMode((current) => {
                  const nextMode = current === 'login' ? 'register' : 'login';
                  navigate(nextMode === 'register' ? '/register' : '/login', { replace: true });
                  return nextMode;
                });
                setError('');
                setConfirmPassword('');
              }}
            >
              {mode === 'login' ? '注册新账号' : '返回登录'}
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  );
}
