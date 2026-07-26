import { Navigate, Outlet } from 'react-router';
import { AppNavigation } from '@/components/AppNavigation';

export function ProtectedRoute() {
  const token = localStorage.getItem('token');

  if (!token) {
    return <Navigate to="/login" replace />;
  }

  return (
    <div className="min-h-screen bg-[#f3f7ff] text-slate-950 dark:bg-background dark:text-foreground">
      <AppNavigation />
      <main className="min-h-screen px-4 pb-8 pt-24 sm:px-5 md:ml-72 md:px-8 md:pt-28 xl:px-10">
        <Outlet />
      </main>
    </div>
  );
}
