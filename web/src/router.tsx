import { createBrowserRouter, Navigate } from 'react-router-dom';
import { LoginPage } from '@/pages/LoginPage';
import { DashboardPage } from '@/pages/DashboardPage';
import { TokensPage } from '@/pages/TokensPage';
import { AdminUsersPage } from '@/pages/admin/UsersPage';
import { AdminChannelsPage } from '@/pages/admin/ChannelsPage';
import { AdminLogsPage } from '@/pages/admin/LogsPage';
import { AdminRedemptionsPage } from '@/pages/admin/RedemptionsPage';
import { ProtectedRoute } from '@/components/ProtectedRoute';

export const router = createBrowserRouter([
  {
    path: '/login',
    element: <LoginPage />,
  },
  {
    path: '/',
    element: <ProtectedRoute />,
    children: [
      {
        index: true,
        element: <Navigate to="/dashboard" replace />,
      },
      {
        path: 'dashboard',
        element: <DashboardPage />,
      },
      {
        path: 'tokens',
        element: <TokensPage />,
      },
      {
        path: 'admin/users',
        element: <AdminUsersPage />,
      },
      {
        path: 'admin/channels',
        element: <AdminChannelsPage />,
      },
      {
        path: 'admin/logs',
        element: <AdminLogsPage />,
      },
      {
        path: 'admin/redemptions',
        element: <AdminRedemptionsPage />,
      },
    ],
  },
]);
