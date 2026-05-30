import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { useState, useEffect } from 'react';
import { toast } from 'sonner';
import { apiClient } from '@/lib/api';
import { unwrapApiData } from '@/lib/api-response';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Skeleton } from '@/components/ui/skeleton';
import { User, Mail, Shield, Users, Save, X } from 'lucide-react';

interface UserProfile {
  id: number;
  username: string;
  display_name: string;
  email: string;
  group: string;
  status: number;
  role: number;
}

const ROLE_LABELS: Record<number, string> = {
  0: '访客',
  1: '普通用户',
  10: '管理员',
  100: '超级管理员',
};

function formatQuota(q: number) {
  return `US$${(q / 500000).toFixed(2)}`;
}

export function ProfilePage() {
  const queryClient = useQueryClient();
  const [editing, setEditing] = useState(false);
  const [displayName, setDisplayName] = useState('');
  const [password, setPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');

  const { data: user, isLoading } = useQuery({
    queryKey: ['user-self'],
    queryFn: async () => {
      const res = await apiClient.get('/user/self');
      return unwrapApiData<UserProfile>(res.data);
    },
  });

  const { data: dashboard } = useQuery({
    queryKey: ['dashboard-summary'],
    queryFn: async () => {
      const res = await apiClient.get('/user/dashboard');
      return unwrapApiData<{ quota?: number; used_quota?: number }>(res.data);
    },
  });

  useEffect(() => {
    if (user) {
      setDisplayName(user.display_name || '');
    }
  }, [user]);

  const updateMutation = useMutation({
    mutationFn: async () => {
      const payload: Record<string, string> = {};
      if (displayName !== (user?.display_name || '')) {
        payload.display_name = displayName;
      }
      if (password) {
        if (password !== confirmPassword) {
          throw new Error('两次输入的密码不一致');
        }
        if (password.length < 8) {
          throw new Error('密码长度不能少于8位');
        }
        payload.password = password;
      }
      if (Object.keys(payload).length === 0) {
        throw new Error('没有需要更新的内容');
      }
      const res = await apiClient.put('/user/self', payload);
      return unwrapApiData(res.data);
    },
    onSuccess: () => {
      toast.success('个人资料已更新');
      setEditing(false);
      setPassword('');
      setConfirmPassword('');
      queryClient.invalidateQueries({ queryKey: ['user-self'] });
    },
    onError: (error: Error) => {
      toast.error(error.message || '更新失败');
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
      <div className="text-center py-12 text-slate-500">无法加载用户信息</div>
    );
  }

  const roleLabel = ROLE_LABELS[user.role] || `角色 ${user.role}`;

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h2 className="text-2xl font-black tracking-normal text-slate-950 dark:text-white">
          个人资料
        </h2>
        {!editing ? (
          <Button onClick={() => setEditing(true)}>编辑资料</Button>
        ) : (
          <div className="flex gap-2">
            <Button
              variant="outline"
              onClick={() => {
                setEditing(false);
                setDisplayName(user.display_name || '');
                setPassword('');
                setConfirmPassword('');
              }}
            >
              <X className="size-4 mr-1" />
              取消
            </Button>
            <Button
              onClick={() => updateMutation.mutate()}
              disabled={updateMutation.isPending}
            >
              <Save className="size-4 mr-1" />
              {updateMutation.isPending ? '保存中...' : '保存'}
            </Button>
          </div>
        )}
      </div>

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-[1fr_320px]">
        <Card className="rounded-lg border-0 bg-white shadow-sm ring-1 ring-slate-200 dark:bg-card dark:ring-white/10">
          <CardHeader className="border-b border-slate-100 p-6 dark:border-white/10">
            <CardTitle className="text-xl font-black tracking-normal text-slate-950 dark:text-white">
              基本信息
            </CardTitle>
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
                  <Label className="text-xs font-bold text-slate-400">
                    用户名
                  </Label>
                  <div className="text-sm font-semibold text-slate-700 dark:text-slate-200">
                    {user.username}
                  </div>
                </div>
              </div>

              <div className="flex items-center gap-3">
                <User className="size-5 text-slate-400" />
                <div className="flex-1">
                  <Label className="text-xs font-bold text-slate-400">
                    显示名称
                  </Label>
                  {editing ? (
                    <Input
                      value={displayName}
                      onChange={(e) => setDisplayName(e.target.value)}
                      placeholder="输入显示名称"
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
                  <Label className="text-xs font-bold text-slate-400">
                    邮箱
                  </Label>
                  <div className="text-sm font-semibold text-slate-700 dark:text-slate-200">
                    {user.email || '—'}
                  </div>
                </div>
              </div>

              <div className="flex items-center gap-3">
                <Shield className="size-5 text-slate-400" />
                <div className="flex-1">
                  <Label className="text-xs font-bold text-slate-400">
                    角色
                  </Label>
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
                  <Label className="text-xs font-bold text-slate-400">
                    分组
                  </Label>
                  <div className="text-sm font-semibold text-slate-700 dark:text-slate-200">
                    {user.group || 'default'}
                  </div>
                </div>
              </div>
            </div>

            {editing && (
              <div className="pt-4 border-t border-slate-100 dark:border-white/10 space-y-4">
                <h3 className="text-sm font-bold text-slate-500 dark:text-slate-400">
                  修改密码（可选）
                </h3>
                <div>
                  <Label className="text-xs font-bold text-slate-400">
                    新密码
                  </Label>
                  <Input
                    type="password"
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    placeholder="留空则不修改密码"
                    className="mt-1"
                  />
                </div>
                <div>
                  <Label className="text-xs font-bold text-slate-400">
                    确认新密码
                  </Label>
                  <Input
                    type="password"
                    value={confirmPassword}
                    onChange={(e) => setConfirmPassword(e.target.value)}
                    placeholder="再次输入新密码"
                    className="mt-1"
                  />
                </div>
              </div>
            )}
          </CardContent>
        </Card>

        <div className="space-y-6">
          <Card className="rounded-lg border-0 bg-white shadow-sm ring-1 ring-slate-200 dark:bg-card dark:ring-white/10">
            <CardHeader className="border-b border-slate-100 p-6 dark:border-white/10">
              <CardTitle className="text-xl font-black tracking-normal text-slate-950 dark:text-white">
                账户概览
              </CardTitle>
            </CardHeader>
            <CardContent className="p-6 space-y-4">
              <div>
                <div className="text-xs font-bold text-slate-400">剩余额度</div>
                <div className="text-2xl font-black text-emerald-600 dark:text-emerald-400">
                  {formatQuota(dashboard?.quota ?? 0)}
                </div>
              </div>
              <div>
                <div className="text-xs font-bold text-slate-400">已用额度</div>
                <div className="text-lg font-black text-slate-700 dark:text-slate-200">
                  {formatQuota(dashboard?.used_quota ?? 0)}
                </div>
              </div>
              <div>
                <div className="text-xs font-bold text-slate-400">用户 ID</div>
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
