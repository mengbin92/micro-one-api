/* eslint-disable react-refresh/only-export-components */
import { lazy, Suspense } from 'react';
import { createBrowserRouter, Navigate } from 'react-router';
import { ProtectedRoute } from '@/components/ProtectedRoute';
import { AdminRoute } from '@/components/AdminRoute';
import { PageLoading } from '@/components/PageLoading';
import { RouteErrorFallback } from '@/components/RouteErrorFallback';
import { routeLoaders } from '@/route-loaders';

const LoginPage = lazy(() => routeLoaders['/login']().then((m) => ({ default: m.LoginPage })));
const UserAgreementPage = lazy(() => routeLoaders['/terms']().then((m) => ({ default: m.UserAgreementPage })));
const PrivacyPolicyPage = lazy(() => routeLoaders['/privacy']().then((m) => ({ default: m.PrivacyPolicyPage })));
const DashboardPage = lazy(() => routeLoaders['/dashboard']().then((m) => ({ default: m.DashboardPage })));
const TokensPage = lazy(() => routeLoaders['/tokens']().then((m) => ({ default: m.TokensPage })));
const UsagePage = lazy(() => routeLoaders['/usage']().then((m) => ({ default: m.UsagePage })));
const PlaygroundPage = lazy(() => routeLoaders['/playground']().then((m) => ({ default: m.PlaygroundPage })));
const ApiGuidePage = lazy(() => routeLoaders['/api-guide']().then((m) => ({ default: m.ApiGuidePage })));
const PricingPage = lazy(() => routeLoaders['/pricing']().then((m) => ({ default: m.PricingPage })));
const OrdersPage = lazy(() => routeLoaders['/orders']().then((m) => ({ default: m.OrdersPage })));
const RechargePage = lazy(() => routeLoaders['/recharge']().then((m) => ({ default: m.RechargePage })));
const RedeemPage = lazy(() => routeLoaders['/redeem']().then((m) => ({ default: m.RedeemPage })));
const ProfilePage = lazy(() => routeLoaders['/profile']().then((m) => ({ default: m.ProfilePage })));
const SubscriptionsPage = lazy(() =>
  routeLoaders['/subscriptions']().then((m) => ({ default: m.SubscriptionsPage }))
);
const AdminOverviewPage = lazy(() =>
  routeLoaders['/admin']().then((m) => ({ default: m.AdminOverviewPage }))
);
const AdminUsersPage = lazy(() =>
  routeLoaders['/admin/users']().then((m) => ({ default: m.AdminUsersPage }))
);
const AdminChannelsPage = lazy(() =>
  routeLoaders['/admin/channels']().then((m) => ({ default: m.AdminChannelsPage }))
);
const AdminModelsPage = lazy(() =>
  routeLoaders['/admin/models']().then((m) => ({ default: m.AdminModelsPage }))
);
const AdminSubscriptionAccountsPage = lazy(() =>
  routeLoaders['/admin/subscription-accounts']().then((m) => ({ default: m.AdminSubscriptionAccountsPage }))
);
const AdminSubscriptionGroupsPage = lazy(() =>
  routeLoaders['/admin/subscription-groups']().then((m) => ({ default: m.AdminSubscriptionGroupsPage }))
);
const AdminSubscriptionPlansPage = lazy(() =>
  routeLoaders['/admin/subscription-plans']().then((m) => ({ default: m.AdminSubscriptionPlansPage }))
);
const AdminSubscriptionsPage = lazy(() =>
  routeLoaders['/admin/subscriptions']().then((m) => ({ default: m.AdminSubscriptionsPage }))
);
const AdminPricingPage = lazy(() =>
  routeLoaders['/admin/pricing']().then((m) => ({ default: m.AdminPricingPage }))
);
const AdminLogsPage = lazy(() =>
  routeLoaders['/admin/logs']().then((m) => ({ default: m.AdminLogsPage }))
);
const AdminPaymentOrdersPage = lazy(() =>
  routeLoaders['/admin/payment-orders']().then((m) => ({ default: m.AdminPaymentOrdersPage }))
);
const AdminRedemptionsPage = lazy(() =>
  routeLoaders['/admin/redemptions']().then((m) => ({ default: m.AdminRedemptionsPage }))
);
const AdminOptionsPage = lazy(() =>
  routeLoaders['/admin/options']().then((m) => ({ default: m.AdminOptionsPage }))
);
const AdminReconciliationPage = lazy(() =>
  routeLoaders['/admin/reconciliation']().then((m) => ({ default: m.AdminReconciliationPage }))
);
const AdminChannelHealthPage = lazy(() =>
  routeLoaders['/admin/channel-health']().then((m) => ({ default: m.ChannelHealthPage }))
);
const AdminCostAnalysisPage = lazy(() =>
  routeLoaders['/admin/cost-analysis']().then((m) => ({ default: m.CostAnalysisPage }))
);
const AdminRoutingOpsPage = lazy(() =>
  routeLoaders['/admin/routing-ops']().then((m) => ({ default: m.RoutingOpsPage }))
);
const AdminUpstreamCostsPage = lazy(() =>
  routeLoaders['/admin/upstream-costs']().then((m) => ({ default: m.AdminUpstreamCostsPage }))
);

function withSuspense(element: React.ReactNode) {
  return <Suspense fallback={<PageLoading />}>{element}</Suspense>;
}

export const router = createBrowserRouter([
  {
    path: '/login',
    element: withSuspense(<LoginPage />),
    errorElement: <RouteErrorFallback />,
  },
  {
    path: '/register',
    element: withSuspense(<LoginPage />),
    errorElement: <RouteErrorFallback />,
  },
  {
    path: '/terms',
    element: withSuspense(<UserAgreementPage />),
    errorElement: <RouteErrorFallback />,
  },
  {
    path: '/privacy',
    element: withSuspense(<PrivacyPolicyPage />),
    errorElement: <RouteErrorFallback />,
  },
  {
    path: '/',
    element: <ProtectedRoute />,
    errorElement: <RouteErrorFallback />,
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
        path: 'playground',
        element: withSuspense(<PlaygroundPage />),
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
            path: 'upstream-costs',
            element: withSuspense(<AdminUpstreamCostsPage />),
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
          {
            path: 'routing-ops',
            element: withSuspense(<AdminRoutingOpsPage />),
          },
        ],
      },
    ],
  },
]);
