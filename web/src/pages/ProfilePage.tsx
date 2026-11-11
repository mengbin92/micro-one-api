import { SecurityCenter } from '@/components/SecurityCenter';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { useEffect, useRef, useState } from 'react';
import { useLocation, useNavigate } from 'react-router';
import { toast } from 'sonner';
import { apiClient } from '@/lib/api';
import { getApiErrorMessage } from '@/lib/api-error';
import { ensureApiSuccess, unwrapApiData } from '@/lib/api-response';
import { oauthProviders, redirectToURL } from '@/lib/oauth';
import { formatUSD } from '@/lib/amount';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Skeleton } from '@/components/ui/skeleton';
import { User, Mail, Shield, Users, Save, X, Check } from 'lucide-react';
import { t } from '@/lib/i18n';
import { accountDashboardQueryOptions, userSelfQueryOptions } from '@/lib/account-queries';

const ROLE_LABELS: Record<number, string> = {
  0: '访客',
  1: '普通用户',
  10: '管理员',
  100: '超级管理员',
};

function formatAmount(q: number) {
  return formatUSD(q);
}

export function ProfilePage() {
  const queryClient = useQueryClient();
  const location = useLocation();
  const navigate = useNavigate();
  const handledBind = useRef<string | undefined>(undefined);
  const [editing, setEditing] = useState(false);
  const [displayName, setDisplayName] = useState('');
  const [password, setPassword] = useState('');
  const [currentPassword, setCurrentPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');

  const { data: user, isLoading } = useQuery(userSelfQueryOptions);

  useEffect(() => {
    const params = new URLSearchParams(location.search);
    const outcome = params.get('oauth_bind');
    if (!outcome || handledBind.current === location.key) return;
    handledBind.current = location.key;
    const label = oauthProviders.find((provider) => provider.id === params.get('provider'))?.label;
    if (outcome === 'success') {
      toast.success(label ? t('{provider} 绑定成功', { provider: t(label) }) : t('绑定成功'));
      queryClient.invalidateQueries({ queryKey: ['user-self'] });
    }
    params.delete('oauth_bind');
    params.delete('provider');
    void navigate({ pathname: location.pathname, search: params.toString(), hash: location.hash }, { replace: true, state: location.state });
  }, [location, navigate, queryClient]);

  const { data: dashboard } = useQuery(accountDashboardQueryOptions);

  const updateMutation = useMutation({
    mutationFn: async () => {
      const payload: Record<string, string> = {};
      if (displayName !== (user?.display_name || '')) {
        payload.display_name = displayName;
      }
      if (password) {
        if (password !== confirmPassword) {
          throw new Error(t("两次输入的密码不一致"));
        }
        if (password.length < 8) {
          throw new Error(t("密码长度不能少于8位"));
        }
        payload.password = password;
        payload.current_password = currentPassword;
      }
      if (Object.keys(payload).length === 0) {
        throw new Error(t("没有需要更新的内容"));
      }
      const res = await apiClient.put('/user/self', payload);
      ensureApiSuccess(res.data);
    },
    onSuccess: () => {
      toast.success(t("个人资料已更新"));
      setEditing(false);
      setPassword('');
      setCurrentPassword('');
      setConfirmPassword('');
      queryClient.invalidateQueries({ queryKey: ['user-self'] });
    },
    onError: (error: Error) => {
      toast.error(error.message || t("更新失败"));
    },
  });

  if (isLoading) {
    return (
      <div className="space-y-6">
        <Skeleton className="h-10 w-48" />
        <Card>
          <CardContent className="p-6 space-y-4">
            <Skeleton className="h-8 w-full" />
            <Skeleton className="h-8 w-full" />
            <Skeleton className="h-8 w-full" />
          </CardContent>
        </Card>
      </div>
    );
  }

  if (!user) {
    return (
      <div className="text-center py-12 text-slate-500">{t("无法加载用户信息")}</div>
    );
  }

  const roleLabel = t(ROLE_LABELS[user.role] || `角色 ${user.role}`);

  const startOAuthBind = async (path: string) => {
    try {
      const res = await apiClient.get(path);
      const data = unwrapApiData<{ auth_url?: string }>(res.data, t("发起绑定失败"));
      if (!data.auth_url) {
        throw new Error(t("发起绑定失败"));
      }
      redirectToURL(data.auth_url);
    } catch (err: unknown) {
      toast.error(getApiErrorMessage(err, t("发起绑定失败")));
    }
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h2 className="text-2xl font-black tracking-normal text-slate-950 dark:text-white">{t("个人资料")}</h2>
        {!editing ? (
          <Button
            onClick={() => {
              setDisplayName(user.display_name || '');
              setEditing(true);
            }}
          >{t("编辑资料")}</Button>
        ) : (
          <div className="flex gap-2">
            <Button
              variant="outline"
              onClick={() => {
                setEditing(false);
                setDisplayName(user.display_name || '');
                setPassword('');
      setCurrentPassword('');
                setConfirmPassword('');
              }}
            >
              <X className="size-4 mr-1" />{t("取消")}</Button>
            <Button
              onClick={() => updateMutation.mutate()}
              disabled={updateMutation.isPending}
            >
              <Save className="size-4 mr-1" />
              {updateMutation.isPending ? t("保存中...") : t("保存")}
            </Button>
          </div>
        )}
      </div>

      <SecurityCenter />
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-[1fr_320px]">
        <Card>
          <CardHeader className="border-b border-slate-100 p-6 dark:border-white/10">
            <CardTitle className="text-xl font-black tracking-normal text-slate-950 dark:text-white">{t("基本信息")}</CardTitle>
          </CardHeader>
          <CardContent className="p-6 space-y-5">
            <div className="flex items-center gap-4">
              <div className="grid size-16 place-items-center rounded-full bg-emerald-500 text-xl font-black text-white">
                {(user.display_name || user.username || 'U')
                  .slice(0, 2)
                  .toUpperCase()}
              </div>
              <div>
                <div className="text-lg font-black text-slate-950 dark:text-white">
                  {user.display_name || user.username}
                </div>
                <div className="text-sm font-semibold text-slate-400">
                  @{user.username}
                </div>
              </div>
            </div>

            <div className="space-y-4 pt-4">
              <div className="flex items-center gap-3">
                <User className="size-5 text-slate-400" />
                <div className="flex-1">
                  <Label className="text-xs font-bold text-slate-400">{t("用户名")}</Label>
                  <div className="text-sm font-semibold text-slate-700 dark:text-slate-200">
                    {user.username}
                  </div>
                </div>
              </div>

              <div className="flex items-center gap-3">
                <User className="size-5 text-slate-400" />
                <div className="flex-1">
                  <Label className="text-xs font-bold text-slate-400">{t("显示名称")}</Label>
                  {editing ? (
                    <Input
                      value={displayName}
                      onChange={(e) => setDisplayName(e.target.value)}
                      placeholder={t("输入显示名称")}
                      className="mt-1"
                    />
                  ) : (
                    <div className="text-sm font-semibold text-slate-700 dark:text-slate-200">
                      {user.display_name || '—'}
                    </div>
                  )}
                </div>
              </div>

              <div className="flex items-center gap-3">
                <Mail className="size-5 text-slate-400" />
                <div className="flex-1">
                  <Label className="text-xs font-bold text-slate-400">{t("邮箱")}</Label>
                  <div className="text-sm font-semibold text-slate-700 dark:text-slate-200">
                    {user.email || '—'}
                  </div>
                </div>
              </div>

              <div className="flex items-center gap-3">
                <Shield className="size-5 text-slate-400" />
                <div className="flex-1">
                  <Label className="text-xs font-bold text-slate-400">{t("角色")}</Label>
                  <div className="mt-1">
                    <span className="inline-flex items-center px-2.5 py-1 rounded-full text-xs font-bold bg-blue-50 text-blue-600 dark:bg-blue-500/10 dark:text-blue-300">
                      {roleLabel}
                    </span>
                  </div>
                </div>
              </div>

              <div className="flex items-center gap-3">
                <Users className="size-5 text-slate-400" />
                <div className="flex-1">
                  <Label className="text-xs font-bold text-slate-400">{t("路由分组")}</Label>
                  <div className="text-sm font-semibold text-slate-700 dark:text-slate-200">
                    {user.group || 'default'}
                  </div>
                </div>
              </div>
            </div>

            {editing && (
              <div className="pt-4 border-t border-slate-100 dark:border-white/10 space-y-4">
                <h3 className="text-sm font-bold text-slate-500 dark:text-slate-400">{t("修改密码（可选）")}</h3>
                <div className="space-y-2"><Label htmlFor="profile-current-password">{t("当前密码")}</Label><Input id="profile-current-password" type="password" autoComplete="current-password" value={currentPassword} onChange={event => setCurrentPassword(event.target.value)} /></div>
                <div>
                  <Label className="text-xs font-bold text-slate-400">{t("新密码")}</Label>
                  <Input
                    type="password"
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    placeholder={t("留空则不修改密码")}
                    className="mt-1"
                  />
                </div>
                <div>
                  <Label className="text-xs font-bold text-slate-400">{t("确认新密码")}</Label>
                  <Input
                    type="password"
                    value={confirmPassword}
                    onChange={(e) => setConfirmPassword(e.target.value)}
                    placeholder={t("再次输入新密码")}
                    className="mt-1"
                  />
                </div>
              </div>
            )}
          </CardContent>
        </Card>

        <div className="space-y-6">
          <Card>
            <CardHeader className="border-b border-slate-100 p-6 dark:border-white/10">
              <CardTitle className="text-xl font-black tracking-normal text-slate-950 dark:text-white">{t("第三方账号")}</CardTitle>
            </CardHeader>
            <CardContent className="p-6">
              <div className="grid grid-cols-1 gap-2">
                {oauthProviders.filter(provider => provider.bindPath || user?.oauth_providers?.includes(provider.id)).map((provider) => {
                  const bound = user?.oauth_providers?.includes(provider.id) ?? false;
                  return (
                    <Button
                      key={provider.id}
                      type="button"
                      variant="outline"
                      disabled={bound}
                      onClick={() => provider.bindPath && void startOAuthBind(provider.bindPath)}
                    >
                      {bound && <Check className="mr-1 h-4 w-4" />}
                      {t(bound ? '已绑定 {provider}' : '绑定{provider}', { provider: t(provider.label) })}
                    </Button>
                  );
                })}
              </div>
            </CardContent>
          </Card>

          <Card>
            <CardHeader className="border-b border-slate-100 p-6 dark:border-white/10">
              <CardTitle className="text-xl font-black tracking-normal text-slate-950 dark:text-white">{t("账户概览")}</CardTitle>
            </CardHeader>
            <CardContent className="p-6 space-y-4">
              <div>
                <div className="text-xs font-bold text-slate-400">{t("钱包余额")}</div>
                <div className="text-2xl font-black text-emerald-600 dark:text-emerald-400">
                  {formatAmount(dashboard?.balance ?? 0)}
                </div>
              </div>
              <div>
                <div className="text-xs font-bold text-slate-400">{t("已用金额")}</div>
                <div className="text-lg font-black text-slate-700 dark:text-slate-200">
                  {formatAmount(dashboard?.used_amount ?? 0)}
                </div>
              </div>
              <div>
                <div className="text-xs font-bold text-slate-400">{t("用户 ID")}</div>
                <div className="text-sm font-mono font-semibold text-slate-500">
                  {user.id}
                </div>
              </div>
            </CardContent>
          </Card>
        </div>
      </div>
    </div>
  );
}
