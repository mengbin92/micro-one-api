import { ShieldAlert } from 'lucide-react';
import { useEffect, useState } from 'react';
import { Outlet } from 'react-router-dom';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { PageLoading } from '@/components/PageLoading';
import { apiClient } from '@/lib/api';
import { isAdminRole } from '@/lib/admin-access';

function readStoredRole(): number | null {
  const raw = localStorage.getItem('userRole');
  if (raw == null || raw === '') return null;
  const parsed = Number(raw);
  return Number.isFinite(parsed) ? parsed : null;
}

export function AdminRoute() {
  const [role, setRole] = useState<number | null>(readStoredRole);
  const [loading, setLoading] = useState<boolean>(role === null);

  useEffect(() => {
    if (role !== null) return;
    let cancelled = false;
    apiClient
      .get('/user/self')
      .then((response) => {
        if (cancelled) return;
        const data = response.data?.data as { role?: number } | null;
        const nextRole = typeof data?.role === 'number' ? data.role : 0;
        localStorage.setItem('userRole', String(nextRole));
        setRole(nextRole);
      })
      .catch(() => {
        if (cancelled) return;
        setRole(0);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [role]);

  if (loading) {
    return <PageLoading />;
  }

  if (isAdminRole(role)) {
    return <Outlet />;
  }

  return (
    <div className="mx-auto flex min-h-[60vh] max-w-md items-center justify-center">
      <Card className="w-full rounded-lg border-0 bg-white shadow-sm ring-1 ring-slate-200 dark:bg-card dark:ring-white/10">
        <CardHeader>
          <div className="mb-2 grid size-11 place-items-center rounded-lg bg-amber-500 text-white">
            <ShieldAlert className="size-5" />
          </div>
          <CardTitle>需要管理员权限</CardTitle>
        </CardHeader>
        <CardContent className="space-y-2 text-sm text-slate-600 dark:text-slate-300">
          <p>当前账号没有访问管理后台的权限。</p>
          <p>请联系超级管理员为您授予管理员角色（role ≥ admin）后再试。</p>
        </CardContent>
      </Card>
    </div>
  );
}
