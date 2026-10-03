// Page admission and endpoint queries are separate permissions.
export const adminPages: Record<string, string> = {
  '/admin': 'admin.overview.read',
  '/admin/users': 'identity.user.list',
  '/admin/channels': 'channel.channel.list',
  '/admin/models': 'channel.model.list',
  '/admin/subscription-accounts': 'channel.account.list',
  '/admin/routing-groups': 'channel.routing_group.list',
  '/admin/subscription-groups': 'subscription.quota_policy.list',
  '/admin/subscription-plans': 'subscription.plan.list',
  '/admin/subscriptions': 'subscription.user_subscription.list',
  '/admin/channel-health': 'monitor.health.channel.read',
  '/admin/model-health': 'monitor.health.model.read',
  '/admin/pricing': 'billing.pricing.read',
  '/admin/upstream-costs': 'billing.upstream_cost.read',
  '/admin/logs': 'log.request.list',
  '/admin/payment-orders': 'billing.payment.list',
  '/admin/redemptions': 'billing.redemption.list',
  '/admin/options': 'system.option.read',
  '/admin/reconciliation': 'billing.reconciliation.read',
  '/admin/cost-analysis': 'billing.account.ledger.read',
  '/admin/routing-ops': 'monitor.health.selector.read',
  '/admin/iam/permissions': 'iam.permission.list',
  '/admin/iam/roles': 'iam.role.list',
  '/admin/iam/assignments': 'identity.user_role.read',
  '/admin/iam/delegations': 'iam.delegation.read',
  '/admin/iam/constraints': 'iam.constraint.read',
  '/admin/iam/menus': 'iam.menu.list',
  '/admin/iam/explain': 'iam.authorization.explain',
  '/admin/iam/audits': 'iam.audit.read',
};
export const menuRoutes: Record<string, string> = { overview: '/admin', users: '/admin/users', channels: '/admin/channels', models: '/admin/models', 'routing-groups': '/admin/routing-groups', pricing: '/admin/pricing', payments: '/admin/payment-orders', subscriptions: '/admin/subscriptions', logs: '/admin/logs', settings: '/admin/options', iam: '/admin/iam/roles' };
export const menuIcons = ['home', 'users', 'server', 'box', 'route', 'coins', 'credit-card', 'calendar', 'file-text', 'settings', 'shield'];
export function firstAdminPage(can: (op: string) => boolean) { return Object.entries(adminPages).find(([, op]) => can(op))?.[0] ?? '/session-roles'; }

// UI mirrors the config owner's fixed key classification. The owner decides again.
export function configWritePermission(key: string) {
  const content: Record<string, string> = { notice: 'notice', about: 'about', home_page_content: 'home' };
  if (content[key]) return `system.content.${content[key]}.update`;
  if (['SystemName', 'system_name', 'Logo', 'logo', 'Footer', 'footer', 'theme', 'Theme'].includes(key)) return 'system.option.update';
  if (/alipay|stripe|payment|epay|topup/i.test(key)) return 'system.option.payment.update';
  if (/ratio|price|quota_per_unit/i.test(key)) return 'system.option.pricing.update';
  return 'system.option.security.update';
}
