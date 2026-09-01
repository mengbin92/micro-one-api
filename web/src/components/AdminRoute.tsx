import { ShieldAlert } from 'lucide-react';
import { useQuery } from '@tanstack/react-query';
import { Outlet } from 'react-router';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { PageLoading } from '@/components/PageLoading';
import { isAdminRole } from '@/lib/admin-access';
import { t } from '@/lib/i18n';
import { userSelfQueryOptions } from '@/lib/account-queries';

export function AdminRoute() {
  const { data: user, isLoading } = useQuery(userSelfQueryOptions);

  if (isLoading) {
    return <PageLoading />;
  }

  if (isAdminRole(user?.role)) {
    return <Outlet />;
  }

  return (
    <div className="mx-auto flex min-h-[60vh] max-w-md items-center justify-center">
      <Card className="w-full">
        <CardHeader>
          <div className="mb-2 grid size-11 place-items-center rounded-lg bg-amber-500 text-white">
            <ShieldAlert className="size-5" />
          </div>
          <CardTitle>{t("需要管理员权限")}</CardTitle>
        </CardHeader>
        <CardContent className="space-y-2 text-sm text-muted-foreground">
          <p>{t("当前账号没有访问管理后台的权限。")}</p>
          <p>{t("请联系超级管理员为您授予管理员角色（role ≥ admin）后再试。")}</p>
        </CardContent>
      </Card>
    </div>
  );
}
