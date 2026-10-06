import { Navigate, Outlet } from 'react-router';
import { useAuthorizationLifecycle } from '@/lib/authorization';
import { PageLoading } from '@/components/PageLoading';
import { useLocation } from 'react-router';
import { AppNavigation } from '@/components/AppNavigation';

export function ProtectedRoute() {
  const auth = useAuthorizationLifecycle();
  const { pathname } = useLocation();
  const token = localStorage.getItem('token');

  if (!token) {
    return <Navigate to="/login" replace />;
  }

  if (auth.isPending) return <PageLoading />;
  if (auth.snapshot?.authorization_mode === 'iam' && auth.snapshot.session?.activation_state !== 'active' && pathname !== '/session-roles') return <Navigate to="/session-roles" replace />;

  return (
    <div className="min-h-screen bg-background text-foreground">
      <AppNavigation />
      <main className="min-h-screen px-4 pb-8 pt-24 sm:px-5 lg:ml-64 lg:px-8 lg:pt-28 xl:px-10">
        <Outlet />
      </main>
    </div>
  );
}
