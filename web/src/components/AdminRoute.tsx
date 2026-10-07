import { Navigate, Outlet, useLocation } from 'react-router';
import { PageLoading } from '@/components/PageLoading';
import { useAuthorization } from '@/lib/authorization';
import { adminPages, firstAdminPage } from '@/lib/admin-permissions';
import { t } from '@/lib/i18n';

export function AdminRoute() {
  const auth = useAuthorization();
  const { pathname } = useLocation();
  if (auth.isPending || (auth.isFetching && !auth.displaySnapshot)) return <PageLoading />;
  if (auth.displaySnapshot?.authorization_mode === 'iam' && auth.displaySnapshot.session?.activation_state !== 'active') return <Navigate to="/session-roles" replace />;
  if (auth.displayCan('admin.console.enter')) {
    if (pathname === '/admin' && !auth.displayCan('admin.overview.read')) return <Navigate to={firstAdminPage(auth.displayCan)} replace />;
    const page = Object.keys(adminPages).sort((a, b) => b.length - a.length).find(path => pathname === path || pathname.startsWith(`${path}/`));
    // A real authority change must discard local detail/draft copies too,
    // even when the actor can still enter this page after losing field access.
    if (page && auth.displayCan(adminPages[page])) return <Outlet key={auth.generation} />;
  }
  return <div role="alert" className="mx-auto max-w-md space-y-3 rounded-xl border p-6"><h2>{t('需要管理员权限')}</h2><p>{t('当前账号没有访问此页面的权限。')}</p><button onClick={() => void auth.refresh()}>{t('刷新权限')}</button></div>;
}
