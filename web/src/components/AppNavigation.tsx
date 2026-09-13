import { Link, NavLink, useLocation, useNavigate } from 'react-router';
import { useEffect, useMemo, useState } from 'react';
import {
  Activity,
  ArrowLeft,
  BadgeCheck,
  BarChart3,
  Boxes,
  BookOpen,
  ChevronDown,
  CreditCard,
  Database,
  FlaskConical,
  IdCard,
  Gift,
  KeyRound,
  Layers,
  LayoutDashboard,
  LogOut,
  MonitorCog,
  Package,
  ReceiptText,
  Scale,
  ScrollText,
  Settings2,
  Ticket,
  TrendingUp,
  UserCircle,
  Users,
  WalletCards,
  Route,
} from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import { Button, buttonVariants } from '@/components/ui/button';
import { ThemeToggle } from '@/components/ThemeToggle';
import { MobileNav } from '@/components/MobileNav';
import { NotificationPanel } from '@/components/NotificationPanel';
import { LanguageToggle } from '@/components/LanguageToggle';
import { useMediaQuery } from '@/hooks/useMediaQuery';
import { canAccessAdmin } from '@/lib/admin-access';
import { formatUSD } from '@/lib/amount';
import { cn } from '@/lib/utils';
import { t } from '@/lib/i18n';
import { preloadRoute } from '@/route-loaders';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { accountDashboardQueryOptions, userSelfQueryOptions } from '@/lib/account-queries';

interface NavItem {
  to: string;
  label: string;
  icon: LucideIcon;
}

interface SecondaryNavItem {
  label: string;
  icon: LucideIcon;
  to?: string;
}

interface AdminNavGroup {
  label: string;
  items: NavItem[];
}

const userLinks: NavItem[] = [
  { to: '/dashboard', label: '仪表盘', icon: LayoutDashboard },
  { to: '/tokens', label: 'API 密钥', icon: KeyRound },
  { to: '/playground', label: '在线调试', icon: FlaskConical },
  { to: '/usage', label: '使用记录', icon: BarChart3 },
  { to: '/api-guide', label: 'API 使用指南', icon: BookOpen },
];

const secondaryUserLinks: SecondaryNavItem[] = [
  { label: '个人资料', icon: UserCircle, to: '/profile' },
  { label: '模型价格', icon: Database, to: '/pricing' },
  { label: '我的订阅', icon: ScrollText, to: '/subscriptions' },
  { label: '充值 / 订阅', icon: CreditCard, to: '/recharge' },
  { label: '我的订单', icon: Ticket, to: '/orders' },
  { label: '兑换码', icon: Gift, to: '/redeem' },
];

const adminOverviewLink: NavItem[] = [
  { to: '/admin', label: '运营总览', icon: MonitorCog },
];

const adminSystemLink: NavItem[] = [
  { to: '/admin/options', label: '系统设置', icon: Settings2 },
];

const adminNavGroups: AdminNavGroup[] = [
  {
    label: '资源与路由',
    items: [
      { to: '/admin/channels', label: '渠道管理', icon: Database },
      { to: '/admin/models', label: '模型目录', icon: Boxes },
      { to: '/admin/subscription-accounts', label: '订阅账号', icon: IdCard },
      { to: '/admin/routing-groups', label: '分组', icon: Layers },
      { to: '/admin/subscription-groups', label: '订阅额度策略', icon: Layers },
      { to: '/admin/routing-ops', label: '路由策略', icon: Route },
    ],
  },
  {
    label: '监控与分析',
    items: [
      { to: '/admin/channel-health', label: '渠道健康', icon: Activity },
      { to: '/admin/model-health', label: '模型健康', icon: Activity },
      { to: '/admin/logs', label: '调用日志', icon: ScrollText },
      { to: '/admin/cost-analysis', label: '经营分析', icon: TrendingUp },
    ],
  },
  {
    label: '用户与产品',
    items: [
      { to: '/admin/users', label: '用户管理', icon: Users },
      { to: '/admin/subscription-plans', label: '订阅套餐', icon: Package },
      { to: '/admin/subscriptions', label: '用户订阅', icon: BadgeCheck },
      { to: '/admin/redemptions', label: '兑换码', icon: Ticket },
    ],
  },
  {
    label: '计费与财务',
    items: [
      { to: '/admin/pricing', label: '销售定价', icon: ReceiptText },
      { to: '/admin/upstream-costs', label: '上游成本', icon: WalletCards },
      { to: '/admin/payment-orders', label: '支付订单', icon: CreditCard },
      { to: '/admin/reconciliation', label: '账务对账', icon: Scale },
    ],
  },
];

const routeTitles: Record<string, string> = {
  '/dashboard': '仪表盘',
  '/tokens': 'API 密钥',
  '/playground': '在线调试',
  '/usage': '使用记录',
  '/api-guide': 'API 使用指南',
  '/pricing': '模型价格',
  '/recharge': '充值 / 订阅',
  '/redeem': '兑换码充值',
  '/orders': '我的订单',
  '/profile': '个人资料',
  '/subscriptions': '我的订阅',
  '/admin': '运营总览',
  '/admin/users': '用户管理',
  '/admin/channels': '渠道管理',
  '/admin/models': '模型目录',
  '/admin/subscription-accounts': '订阅账号管理',
  '/admin/routing-groups': '分组',
  '/admin/subscription-groups': '订阅额度策略',
  '/admin/subscriptions': '用户订阅',
  '/admin/subscription-plans': '订阅套餐',
  '/admin/channel-health': '渠道健康',
  '/admin/model-health': '模型健康',
  '/admin/cost-analysis': '经营分析',
  '/admin/routing-ops': '路由策略',
  '/admin/pricing': '销售定价',
  '/admin/upstream-costs': '上游成本',
  '/admin/logs': '调用日志',
  '/admin/payment-orders': '支付订单',
  '/admin/reconciliation': '账务对账',
  '/admin/redemptions': '兑换码',
  '/admin/options': '系统设置',
};

function formatBalance(value?: number) {
  return formatUSD(value);
}

function NavigationLinks({
  items,
  onNavigate,
  compact = false,
}: {
  items: NavItem[];
  onNavigate?: () => void;
  compact?: boolean;
}) {
  return (
    <div className="space-y-2">
      {items.map((link) => {
        const Icon = link.icon;
        return (
          <NavLink
            key={link.to}
            to={link.to}
            end={link.to === '/admin'}
            aria-label={t(link.label)}
            className={({ isActive }) =>
              cn(
                'flex items-center gap-3 text-sm font-medium transition-colors',
                compact ? 'h-10 rounded-xl px-3' : 'h-12 rounded-2xl px-4',
                isActive
                  ? 'bg-accent text-accent-foreground'
                  : 'text-muted-foreground hover:bg-muted hover:text-foreground',
              )
            }
            onClick={onNavigate}
            onMouseEnter={() => preloadRoute(link.to)}
            onFocus={() => preloadRoute(link.to)}
          >
            <Icon className="size-5" />
            <span aria-hidden="true">{t(link.label)}</span>
          </NavLink>
        );
      })}
    </div>
  );
}

function SecondaryLinks({ onNavigate }: { onNavigate?: () => void }) {
  return (
    <div className="space-y-2">
      {secondaryUserLinks.map((item) => {
        const Icon = item.icon;
        const content = (
          <>
            <Icon className="size-5" />
            <span className="min-w-0 flex-1">{t(item.label)}</span>
            {!item.to && (
              <span className="rounded-full bg-muted px-2 py-0.5 text-xs font-medium text-muted-foreground">{t("开发中")}</span>
            )}
          </>
        );

        if (item.to) {
          return (
            <NavLink
              key={item.label}
              to={item.to}
              className={({ isActive }) =>
                cn(
                  'flex h-11 w-full items-center gap-3 rounded-2xl px-4 text-left text-sm font-medium transition-colors',
                  isActive
                    ? 'bg-accent text-accent-foreground'
                    : 'text-muted-foreground hover:bg-muted hover:text-foreground',
                )
              }
              onClick={onNavigate}
              onMouseEnter={() => preloadRoute(item.to!)}
              onFocus={() => preloadRoute(item.to!)}
            >
              {content}
            </NavLink>
          );
        }

        return (
          <button
            key={item.label}
            type="button"
            disabled
            title={t("开发中")}
            className="flex h-11 w-full cursor-not-allowed items-center gap-3 rounded-2xl px-4 text-left text-sm font-medium text-muted-foreground opacity-75"
          >
            {content}
          </button>
        );
      })}
    </div>
  );
}

export function AppNavigation() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const location = useLocation();
  const [mobileOpen, setMobileOpen] = useState(false);
  const [notificationOpen, setNotificationOpen] = useState(false);
  const [storedRole] = useState<number | null>(() => {
    const stored = localStorage.getItem('userRole');
    return stored != null && stored !== '' ? Number(stored) : null;
  });
  const { data: user } = useQuery(userSelfQueryOptions);
  const { data: account } = useQuery(accountDashboardQueryOptions);
  const role = typeof user?.role === 'number' ? user.role : storedRole;
  const isWide = useMediaQuery('(min-width: 1024px)');
  const isAdmin = canAccessAdmin({ role });
  const isAdminRoute = location.pathname === '/admin' || location.pathname.startsWith('/admin/');
  const activeAdminGroup = adminNavGroups.find((group) => group.items.some((item) => (
    location.pathname === item.to || location.pathname.startsWith(`${item.to}/`)
  )))?.label;
  // group: '' = explicitly all-collapsed, null = follow the active route's
  // group. The disclosure is keyed to the pathname it was made on and resets
  // when the route changes, so a manual toggle on one route cannot resurrect
  // itself when the user navigates back to an older path.
  const [adminNavDisclosure, setAdminNavDisclosure] = useState<{ path: string; group: string | null }>({
    path: location.pathname,
    group: null,
  });
  if (adminNavDisclosure.path !== location.pathname) {
    setAdminNavDisclosure({ path: location.pathname, group: null });
  }
  const expandedAdminGroup = adminNavDisclosure.path === location.pathname && adminNavDisclosure.group !== null
    ? adminNavDisclosure.group
    : activeAdminGroup;
  const effectiveMobileOpen = !isWide && mobileOpen;
  const currentTitle = t(routeTitles[location.pathname] ?? '仪表盘');
  const displayName = user?.display_name || user?.username || t("用户");
  const initials = useMemo(() => displayName.slice(0, 2).toUpperCase(), [displayName]);

  useEffect(() => {
    if (user?.id != null) {
      localStorage.setItem('userId', String(user.id));
    }
    if (typeof user?.role === 'number') {
      localStorage.setItem('userRole', String(user.role));
    }
  }, [user]);

  const handleLogout = () => {
    localStorage.removeItem('token');
    localStorage.removeItem('adminToken');
    localStorage.removeItem('userId');
    localStorage.removeItem('userRole');
    queryClient.clear();
    navigate('/login', { replace: true });
  };

  const adminControl = isAdmin ? (
    <Link
      to={isAdminRoute ? '/dashboard' : '/admin'}
      aria-label={t(isAdminRoute ? '返回控制台' : '进入管理')}
      className={buttonVariants({ variant: 'outline', size: 'sm' })}
      onMouseEnter={() => preloadRoute(isAdminRoute ? '/dashboard' : '/admin')}
      onFocus={() => preloadRoute(isAdminRoute ? '/dashboard' : '/admin')}
    >
      {isAdminRoute ? <ArrowLeft className="size-4" /> : <MonitorCog className="size-4" />}
      <span className="hidden sm:inline">{t(isAdminRoute ? '返回控制台' : '进入管理')}</span>
    </Link>
  ) : null;

  const sidebar = (
    <div className="flex h-full flex-col bg-sidebar supports-backdrop-filter:bg-sidebar/70 supports-backdrop-filter:backdrop-blur-xl supports-backdrop-filter:backdrop-saturate-[1.8]">
      <div className="flex h-20 shrink-0 items-center border-b border-border px-6">
        <div className="flex items-center gap-3">
          <img src="/logo-icon.svg" alt="" aria-hidden="true" className="size-10 shrink-0 rounded-xl" />
          <div>
            <div className="text-lg font-semibold tracking-normal text-foreground">Micro-One API</div>
            <div className="text-xs font-medium text-muted-foreground">{t(isAdminRoute ? '管理控制台' : '网关控制台')}</div>
          </div>
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto px-4 py-6">
        {isAdminRoute && isAdmin ? (
          <>
            <p className="mb-2 px-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground">{t('管理工作台')}</p>
            <NavigationLinks items={adminOverviewLink} compact onNavigate={() => setMobileOpen(false)} />
            <div className="mt-4 space-y-1">
              {adminNavGroups.map((group) => {
                const expanded = expandedAdminGroup === group.label;
                return (
                  <section key={group.label}>
                    <button
                      type="button"
                      aria-expanded={expanded}
                      onClick={() => setAdminNavDisclosure({ path: location.pathname, group: expanded ? '' : group.label })}
                      className="flex h-10 w-full items-center justify-between rounded-xl px-3 text-sm font-semibold text-muted-foreground hover:bg-muted hover:text-foreground"
                    >
                      <span>{t(group.label)}</span>
                      <ChevronDown className={cn('size-4 transition-transform', expanded && 'rotate-180')} />
                    </button>
                    {expanded && <NavigationLinks items={group.items} compact onNavigate={() => setMobileOpen(false)} />}
                  </section>
                );
              })}
            </div>
            <div className="mt-4 border-t border-border pt-4">
              <NavigationLinks items={adminSystemLink} compact onNavigate={() => setMobileOpen(false)} />
            </div>
          </>
        ) : (
          <>
            <p className="mb-3 px-4 text-xs font-medium text-muted-foreground">{t('核心功能')}</p>
            <NavigationLinks items={userLinks} onNavigate={() => setMobileOpen(false)} />

            <p className="mb-3 mt-7 px-4 text-xs font-medium text-muted-foreground">{t('钱包 & 活动')}</p>
            <SecondaryLinks onNavigate={() => setMobileOpen(false)} />
          </>
        )}
      </div>

      <div className="shrink-0 border-t border-border p-4">
        {isAdminRoute && (
          <Link to="/dashboard" onClick={() => setMobileOpen(false)} className="flex h-11 items-center gap-3 rounded-xl px-3 text-sm font-medium text-muted-foreground hover:bg-muted hover:text-foreground">
            <ArrowLeft className="size-5" />{t('返回用户控制台')}
          </Link>
        )}
        <div className="mt-3 flex items-center justify-center gap-2 lg:hidden">
          <LanguageToggle className="gap-2" />
          <ThemeToggle />
        </div>
        <div className="mt-3 flex justify-center gap-3 text-xs text-muted-foreground">
          <Link to="/terms" onClick={() => setMobileOpen(false)} className="hover:text-foreground hover:underline">{t("用户协议")}</Link>
          <Link to="/privacy" onClick={() => setMobileOpen(false)} className="hover:text-foreground hover:underline">{t("隐私政策")}</Link>
        </div>
      </div>
    </div>
  );

  return (
    <>
      <aside className="fixed inset-y-0 left-0 z-30 hidden w-64 border-r border-border lg:block">
        {sidebar}
      </aside>

      <header className="fixed left-0 right-0 top-0 z-20 border-b border-border bg-background/95 backdrop-blur lg:left-64">
        <div className="flex h-20 items-center gap-3 px-4 sm:px-5 lg:px-8 xl:px-10">
          <div className="flex items-center gap-3 lg:hidden">
            <MobileNav open={effectiveMobileOpen} onOpenChange={setMobileOpen}>
              {sidebar}
            </MobileNav>
          </div>

          <h1 className="min-w-0 truncate text-lg font-bold tracking-normal text-foreground sm:text-2xl">
            {currentTitle}
          </h1>

          <div className="ml-auto flex min-w-0 shrink-0 items-center gap-2 sm:gap-3">
            <div className="hidden md:block">
              <LanguageToggle className="gap-2" />
            </div>
            <div className={cn('h-10 items-center gap-2 rounded-2xl bg-emerald-50 px-4 text-sm font-semibold text-emerald-600 dark:bg-emerald-500/10 dark:text-emerald-300', isAdminRoute ? 'hidden' : 'hidden xl:flex')}>
              <WalletCards className="size-4" />
              {formatBalance(account?.balance)}
            </div>
            <div className="hidden md:block">
              <ThemeToggle />
            </div>
            {isAdmin && <NotificationPanel open={notificationOpen} onOpenChange={setNotificationOpen} />}
            {adminControl}
            <button
              type="button"
              className="hidden min-w-0 items-center gap-3 rounded-2xl border border-border bg-card px-3 py-2 shadow-sm xl:flex"
            >
              <span className="grid size-10 shrink-0 place-items-center rounded-full bg-emerald-500 text-sm font-semibold text-white">
                {initials}
              </span>
              <span className="min-w-0 text-left">
                <span className="block max-w-36 truncate text-sm font-semibold text-foreground">
                  {displayName}
                </span>
                <span className="block text-xs font-medium text-muted-foreground">{t(isAdminRoute ? '管理员' : '控制台用户')}</span>
              </span>
            </button>
            <Button type="button" variant="ghost" size="icon-sm" aria-label={t('退出登录')} onClick={handleLogout}>
              <LogOut className="size-4" />
            </Button>
          </div>
        </div>
      </header>
    </>
  );
}
