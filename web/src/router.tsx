/* eslint-disable react-refresh/only-export-components */
import { lazy, Suspense } from 'react';
import { createBrowserRouter, Navigate } from 'react-router-dom';
import { ProtectedRoute } from '@/components/ProtectedRoute';
import { AdminRoute } from '@/components/AdminRoute';
import { PageLoading } from '@/components/PageLoading';

const LoginPage = lazy(() => import('@/pages/LoginPage').then((m) => ({ default: m.LoginPage })));
const DashboardPage = lazy(() => import('@/pages/DashboardPage').then((m) => ({ default: m.DashboardPage })));
const TokensPage = lazy(() => import('@/pages/TokensPage').then((m) => ({ default: m.TokensPage })));
const UsagePage = lazy(() => import('@/pages/UsagePage').then((m) => ({ default: m.UsagePage })));
const ApiGuidePage = lazy(() => import('@/pages/ApiGuidePage').then((m) => ({ default: m.ApiGuidePage })));
const PricingPage = lazy(() => import('@/pages/PricingPage').then((m) => ({ default: m.PricingPage })));
const OrdersPage = lazy(() => import('@/pages/OrdersPage').then((m) => ({ default: m.OrdersPage })));
const RechargePage = lazy(() => import('@/pages/RechargePage').then((m) => ({ default: m.RechargePage })));
const RedeemPage = lazy(() => import('@/pages/RedeemPage').then((m) => ({ default: m.RedeemPage })));
const ProfilePage = lazy(() => import('@/pages/ProfilePage').then((m) => ({ default: m.ProfilePage })));
const SubscriptionsPage = lazy(() =>
  import('@/pages/SubscriptionsPage').then((m) => ({ default: m.SubscriptionsPage }))
);
const AdminOverviewPage = lazy(() =>
  import('@/pages/admin/OverviewPage').then((m) => ({ default: m.AdminOverviewPage }))
);
const AdminUsersPage = lazy(() => import('@/pages/admin/UsersPage').then((m) => ({ default: m.AdminUsersPage })));
const AdminChannelsPage = lazy(() =>
  import('@/pages/admin/ChannelsPage').then((m) => ({ default: m.AdminChannelsPage }))
);
const AdminModelsPage = lazy(() =>
  import('@/pages/admin/ModelsPage').then((m) => ({ default: m.AdminModelsPage }))
);
const AdminSubscriptionAccountsPage = lazy(() =>
  import('@/pages/admin/SubscriptionAccountsPage').then((m) => ({ default: m.AdminSubscriptionAccountsPage }))
);
const AdminSubscriptionGroupsPage = lazy(() =>
  import('@/pages/admin/SubscriptionGroupsPage').then((m) => ({ default: m.AdminSubscriptionGroupsPage }))
);
const AdminSubscriptionPlansPage = lazy(() =>
  import('@/pages/admin/SubscriptionPlansPage').then((m) => ({ default: m.AdminSubscriptionPlansPage }))
);
const AdminSubscriptionsPage = lazy(() =>
  import('@/pages/admin/SubscriptionsAdminPage').then((m) => ({ default: m.AdminSubscriptionsPage }))
);
const AdminPricingPage = lazy(() =>
  import('@/pages/admin/PricingPage').then((m) => ({ default: m.AdminPricingPage }))
);
const AdminLogsPage = lazy(() => import('@/pages/admin/LogsPage').then((m) => ({ default: m.AdminLogsPage })));
const AdminPaymentOrdersPage = lazy(() =>
  import('@/pages/admin/PaymentOrdersPage').then((m) => ({ default: m.AdminPaymentOrdersPage }))
);
const AdminRedemptionsPage = lazy(() =>
  import('@/pages/admin/RedemptionsPage').then((m) => ({ default: m.AdminRedemptionsPage }))
);
const AdminOptionsPage = lazy(() =>
  import('@/pages/admin/OptionsPage').then((m) => ({ default: m.AdminOptionsPage }))
);
const AdminReconciliationPage = lazy(() =>
  import('@/pages/admin/ReconciliationPage').then((m) => ({ default: m.AdminReconciliationPage }))
);
const AdminChannelHealthPage = lazy(() =>
  import('@/pages/admin/ChannelHealthPage').then((m) => ({ default: m.ChannelHealthPage }))
);
const AdminCostAnalysisPage = lazy(() =>
  import('@/pages/admin/CostAnalysisPage').then((m) => ({ default: m.CostAnalysisPage }))
);

function withSuspense(element: React.ReactNode) {
  return <Suspense fallback={<PageLoading />}>{element}</Suspense>;
}

export const router = createBrowserRouter([
  {
    path: '/login',
    element: withSuspense(<LoginPage />),
  },
  {
    path: '/register',
    element: withSuspense(<LoginPage />),
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
        element: withSuspense(<DashboardPage />),
      },
      {
        path: 'tokens',
        element: withSuspense(<TokensPage />),
      },
      {
        path: 'usage',
        element: withSuspense(<UsagePage />),
      },
      {
        path: 'api-guide',
        element: withSuspense(<ApiGuidePage />),
      },
      {
        path: 'pricing',
        element: withSuspense(<PricingPage />),
      },
      {
        path: 'recharge',
        element: withSuspense(<RechargePage />),
      },
      {
        path: 'redeem',
        element: withSuspense(<RedeemPage />),
      },
      {
        path: 'orders',
        element: withSuspense(<OrdersPage />),
      },
      {
        path: 'profile',
        element: withSuspense(<ProfilePage />),
      },
      {
        path: 'subscriptions',
        element: withSuspense(<SubscriptionsPage />),
      },
      {
        path: 'admin',
        element: <AdminRoute />,
        children: [
          {
            index: true,
            element: withSuspense(<AdminOverviewPage />),
          },
          {
            path: 'users',
            element: withSuspense(<AdminUsersPage />),
          },
          {
            path: 'channels',
            element: withSuspense(<AdminChannelsPage />),
          },
          {
            path: 'models',
            element: withSuspense(<AdminModelsPage />),
          },
          {
            path: 'subscription-accounts',
            element: withSuspense(<AdminSubscriptionAccountsPage />),
          },
          {
            path: 'subscription-groups',
            element: withSuspense(<AdminSubscriptionGroupsPage />),
          },
          {
            path: 'subscription-plans',
            element: withSuspense(<AdminSubscriptionPlansPage />),
          },
          {
            path: 'subscriptions',
            element: withSuspense(<AdminSubscriptionsPage />),
          },
          {
            path: 'channel-health',
            element: withSuspense(<AdminChannelHealthPage />),
          },
          {
            path: 'pricing',
            element: withSuspense(<AdminPricingPage />),
          },
          {
            path: 'logs',
            element: withSuspense(<AdminLogsPage />),
          },
          {
            path: 'payment-orders',
            element: withSuspense(<AdminPaymentOrdersPage />),
          },
          {
            path: 'redemptions',
            element: withSuspense(<AdminRedemptionsPage />),
          },
          {
            path: 'reconciliation',
            element: withSuspense(<AdminReconciliationPage />),
          },
          {
            path: 'options',
            element: withSuspense(<AdminOptionsPage />),
          },
          {
            path: 'cost-analysis',
            element: withSuspense(<AdminCostAnalysisPage />),
          },
        ],
      },
    ],
  },
]);
