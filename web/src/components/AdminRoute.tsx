import { Navigate, Outlet, useLocation } from 'react-router';
import { PageLoading } from '@/components/PageLoading';
import { useAuthorization } from '@/lib/authorization';
import { adminPages, firstAdminPage } from '@/lib/admin-permissions';
import { t } from '@/lib/i18n';

export function AdminRoute() {
  const auth = useAuthorization();
  const { pathname } = useLocation();
  if (auth.isPending || auth.isFetching) return <PageLoading />;
  if (auth.snapshot?.authorization_mode === 'iam' && auth.snapshot.session?.activation_state !== 'active') return <Navigate to="/session-roles" replace />;
  if (auth.can('admin.console.enter')) {
    if (pathname === '/admin' && !auth.can('admin.overview.read')) return <Navigate to={firstAdminPage(auth.can)} replace />;
    const page = Object.keys(adminPages).sort((a, b) => b.length - a.length).find(path => pathname === path || pathname.startsWith(`${path}/`));
    if (page && auth.can(adminPages[page])) return <Outlet />;
  }
  return <div role="alert" className="mx-auto max-w-md space-y-3 rounded-xl border p-6"><h2>{t('需要管理员权限')}</h2><p>{t('当前账号没有访问此页面的权限。')}</p><button onClick={() => void auth.refresh()}>{t('刷新权限')}</button></div>;
}
