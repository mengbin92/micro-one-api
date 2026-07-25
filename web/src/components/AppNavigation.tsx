import { Link, NavLink, useLocation, useNavigate } from 'react-router-dom';
import { useEffect, useMemo, useState } from 'react';
import {
  Activity,
  BadgeCheck,
  BarChart3,
  Boxes,
  BookOpen,
  ChevronsLeft,
  CreditCard,
  Database,
  IdCard,
  Gift,
  KeyRound,
  Layers,
  LayoutDashboard,
  Languages,
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
} from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import { Button, buttonVariants } from '@/components/ui/button';
import { ThemeToggle } from '@/components/ThemeToggle';
import { MobileNav } from '@/components/MobileNav';
import { NotificationPanel } from '@/components/NotificationPanel';
import { useMediaQuery } from '@/hooks/useMediaQuery';
import { apiClient } from '@/lib/api';
import { canAccessAdmin } from '@/lib/admin-access';
import { unwrapApiData } from '@/lib/api-response';
import { formatUSD } from '@/lib/amount';
import { cn } from '@/lib/utils';

interface NavItem {
  to: string;
  label: string;
  ariaLabel: string;
  icon: LucideIcon;
}

interface SecondaryNavItem {
  label: string;
  icon: LucideIcon;
  to?: string;
}

interface UserSelf {
  id?: number | string;
  username?: string;
  display_name?: string;
  role?: number;
}

interface AccountDashboard {
  balance?: number;
  used_amount?: number;
}

const userLinks: NavItem[] = [
  { to: '/dashboard', label: '仪表盘', ariaLabel: 'Dashboard', icon: LayoutDashboard },
  { to: '/tokens', label: 'API 密钥', ariaLabel: 'Tokens', icon: KeyRound },
  { to: '/usage', label: '使用记录', ariaLabel: 'Usage', icon: BarChart3 },
  { to: '/api-guide', label: 'API 使用指南', ariaLabel: 'API Guide', icon: BookOpen },
];

const secondaryUserLinks: SecondaryNavItem[] = [
  { label: '个人资料', icon: UserCircle, to: '/profile' },
  { label: '模型价格', icon: Database, to: '/pricing' },
  { label: '我的订阅', icon: ScrollText, to: '/subscriptions' },
  { label: '充值 / 订阅', icon: CreditCard, to: '/recharge' },
  { label: '我的订单', icon: Ticket, to: '/orders' },
  { label: '兑换码', icon: Gift, to: '/redeem' },
];

const adminLinks: NavItem[] = [
  { to: '/admin', label: '总览', ariaLabel: 'Admin Overview', icon: MonitorCog },
  { to: '/admin/users', label: '用户', ariaLabel: 'Users', icon: Users },
  { to: '/admin/channels', label: '渠道', ariaLabel: 'Channels', icon: Database },
  { to: '/admin/models', label: '模型', ariaLabel: 'Models', icon: Boxes },
  { to: '/admin/subscription-accounts', label: '订阅账号', ariaLabel: 'Subscription Accounts', icon: IdCard },
  { to: '/admin/subscription-groups', label: '订阅分组', ariaLabel: 'Subscription Groups', icon: Layers },
  { to: '/admin/subscription-plans', label: '订阅套餐', ariaLabel: 'Subscription Plans', icon: Package },
  { to: '/admin/subscriptions', label: '用户订阅', ariaLabel: 'User Subscriptions', icon: BadgeCheck },
  { to: '/admin/channel-health', label: '健康监控', ariaLabel: 'Channel Health', icon: Activity },
  { to: '/admin/cost-analysis', label: '成本分析', ariaLabel: 'Cost Analysis', icon: TrendingUp },
  { to: '/admin/pricing', label: '模型价格', ariaLabel: 'Model Pricing', icon: ReceiptText },
  { to: '/admin/logs', label: '日志', ariaLabel: 'Logs', icon: ScrollText },
  { to: '/admin/payment-orders', label: '订单', ariaLabel: 'Payment Orders', icon: CreditCard },
  { to: '/admin/reconciliation', label: '对账', ariaLabel: 'Reconciliation', icon: Scale },
  { to: '/admin/redemptions', label: '兑换码', ariaLabel: 'Redemptions', icon: Ticket },
  { to: '/admin/options', label: '设置', ariaLabel: 'Options', icon: Settings2 },
];

const routeTitles: Record<string, string> = {
  '/dashboard': '仪表盘',
  '/tokens': 'API 密钥',
  '/usage': '使用记录',
  '/api-guide': 'API 使用指南',
  '/pricing': '模型价格',
  '/recharge': '充值 / 订阅',
  '/redeem': '兑换码充值',
  '/orders': '我的订单',
  '/profile': '个人资料',
  '/subscriptions': '我的订阅',
  '/admin': '管理总览',
  '/admin/users': '用户管理',
  '/admin/channels': '渠道管理',
  '/admin/models': '模型管理',
  '/admin/subscription-accounts': '订阅账号管理',
  '/admin/subscription-groups': '订阅分组',
  '/admin/subscriptions': '用户订阅',
  '/admin/channel-health': '健康监控',
  '/admin/cost-analysis': '成本分析',
  '/admin/pricing': '模型价格',
  '/admin/logs': '系统日志',
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
}: {
  items: NavItem[];
  onNavigate?: () => void;
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
            aria-label={link.ariaLabel}
            className={({ isActive }) =>
              cn(
                'flex h-12 items-center gap-3 rounded-2xl px-4 text-sm font-semibold transition-colors',
                isActive
                  ? 'bg-blue-50 text-blue-600 dark:bg-blue-500/10 dark:text-blue-300'
                  : 'text-slate-500 hover:bg-slate-50 hover:text-slate-950 dark:text-slate-400 dark:hover:bg-white/5 dark:hover:text-white',
              )
            }
            onClick={onNavigate}
          >
            <Icon className="size-5" />
            <span aria-hidden="true">{link.label}</span>
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
            <span className="min-w-0 flex-1">{item.label}</span>
            {!item.to && (
              <span className="rounded-full bg-slate-100 px-2 py-0.5 text-xs font-bold text-slate-400 dark:bg-white/10">
                开发中
              </span>
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
                  'flex h-11 w-full items-center gap-3 rounded-2xl px-4 text-left text-sm font-semibold transition-colors',
                  isActive
                    ? 'bg-blue-50 text-blue-600 dark:bg-blue-500/10 dark:text-blue-300'
                    : 'text-slate-500 hover:bg-slate-50 hover:text-slate-950 dark:text-slate-400 dark:hover:bg-white/5 dark:hover:text-white',
                )
              }
              onClick={onNavigate}
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
            title="开发中"
            className="flex h-11 w-full cursor-not-allowed items-center gap-3 rounded-2xl px-4 text-left text-sm font-semibold text-slate-400 opacity-75 dark:text-slate-500"
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
  const location = useLocation();
  const [mobileOpen, setMobileOpen] = useState(false);
  const [notificationOpen, setNotificationOpen] = useState(false);
  const [role, setRole] = useState<number | null>(() => {
    const stored = localStorage.getItem('userRole');
    return stored != null && stored !== '' ? Number(stored) : null;
  });
  const [user, setUser] = useState<UserSelf | null>(null);
  const [account, setAccount] = useState<AccountDashboard | null>(null);
  const isWide = useMediaQuery('(min-width: 768px)');
  const isAdmin = canAccessAdmin({ role });
  const effectiveMobileOpen = !isWide && mobileOpen;
  const currentTitle = routeTitles[location.pathname] ?? '仪表盘';
  const displayName = user?.display_name || user?.username || '用户';
  const initials = useMemo(() => displayName.slice(0, 2).toUpperCase(), [displayName]);

  useEffect(() => {
    let cancelled = false;

    Promise.allSettled([apiClient.get('/user/self'), apiClient.get('/user/dashboard')]).then((results) => {
      if (cancelled) return;

      const userResult = results[0];
      if (userResult.status === 'fulfilled') {
        const self = unwrapApiData<UserSelf | null>(userResult.value.data);
        setUser(self);
        if (self?.id != null) {
          localStorage.setItem('userId', String(self.id));
        }
        if (typeof self?.role === 'number') {
          localStorage.setItem('userRole', String(self.role));
          setRole(self.role);
        }
      }

      const dashboardResult = results[1];
      if (dashboardResult.status === 'fulfilled') {
        setAccount(unwrapApiData<AccountDashboard | null>(dashboardResult.value.data));
      }
    });

    return () => {
      cancelled = true;
    };
  }, []);

  const handleLogout = () => {
    localStorage.removeItem('token');
    localStorage.removeItem('adminToken');
    localStorage.removeItem('userId');
    localStorage.removeItem('userRole');
    navigate('/login', { replace: true });
  };

  const adminControl = isAdmin ? (
    <Link to="/admin" aria-label="进入管理" className={buttonVariants({ variant: 'outline', size: 'sm' })}>
      <MonitorCog className="size-4" />
      进入管理
    </Link>
  ) : null;

  const sidebar = (
    <div className="flex h-full flex-col bg-white dark:bg-card">
      <div className="flex h-20 items-center border-b border-slate-200 px-6 dark:border-white/10">
        <div className="flex items-center gap-3">
          <img src="/logo-icon.svg" alt="" aria-hidden="true" className="size-10 shrink-0 rounded-xl" />
          <div>
            <div className="text-lg font-black tracking-normal text-slate-950 dark:text-white">Micro-One API</div>
            <div className="text-xs font-semibold text-slate-400">Gateway Console</div>
          </div>
        </div>
      </div>

      <div className="flex-1 overflow-y-auto px-4 py-6">
        <p className="mb-3 px-4 text-xs font-bold text-slate-400">核心功能</p>
        <NavigationLinks items={userLinks} onNavigate={() => setMobileOpen(false)} />

        <p className="mb-3 mt-7 px-4 text-xs font-bold text-slate-400">钱包 & 活动</p>
        <SecondaryLinks onNavigate={() => setMobileOpen(false)} />

        {isAdmin && (
          <>
            <p className="mb-3 mt-7 px-4 text-xs font-bold text-slate-400">管理后台</p>
            <NavigationLinks items={adminLinks} onNavigate={() => setMobileOpen(false)} />
          </>
        )}
      </div>

      <div className="border-t border-slate-200 p-4 dark:border-white/10">
        <button
          type="button"
          className="flex w-full items-center gap-3 rounded-2xl px-4 py-3 text-left text-sm font-semibold text-slate-600 hover:bg-slate-50 dark:text-slate-300 dark:hover:bg-white/5"
        >
          <ChevronsLeft className="size-5" />
          <span>
            <span className="block text-slate-900 dark:text-white">收起侧边栏</span>
            <span className="block text-xs font-medium text-slate-400">为内容保留更多空间</span>
          </span>
        </button>
      </div>
    </div>
  );

  return (
    <>
      <aside className="fixed inset-y-0 left-0 z-30 hidden w-72 border-r border-slate-200 md:block dark:border-white/10">
        {sidebar}
      </aside>

      <header className="fixed left-0 right-0 top-0 z-20 border-b border-slate-200 bg-white/95 backdrop-blur md:left-72 dark:border-white/10 dark:bg-card/95">
        <div className="flex h-20 items-center gap-3 px-4 sm:px-5 md:px-8 xl:px-10">
          <div className="flex items-center gap-3 md:hidden">
            <MobileNav open={effectiveMobileOpen} onOpenChange={setMobileOpen}>
              {sidebar}
            </MobileNav>
          </div>

          <h1 className="min-w-0 text-xl font-black tracking-normal text-slate-950 dark:text-white sm:text-2xl">
            {currentTitle}
          </h1>

          <div className="ml-auto flex min-w-0 items-center gap-2 sm:gap-3">
            <Button type="button" variant="outline" size="sm" className="hidden gap-2 sm:inline-flex">
              <Languages className="size-4" />
              CN ZH
            </Button>
            <div className="hidden h-10 items-center gap-2 rounded-2xl bg-emerald-50 px-4 text-sm font-black text-emerald-600 sm:flex dark:bg-emerald-500/10 dark:text-emerald-300">
              <WalletCards className="size-4" />
              {formatBalance(account?.balance)}
            </div>
            <ThemeToggle />
            {isAdmin && <NotificationPanel open={notificationOpen} onOpenChange={setNotificationOpen} />}
            {adminControl}
            <button
              type="button"
              className="hidden min-w-0 items-center gap-3 rounded-2xl border border-blue-100 bg-white px-3 py-2 shadow-sm sm:flex dark:border-white/10 dark:bg-card"
            >
              <span className="grid size-10 shrink-0 place-items-center rounded-full bg-emerald-500 text-sm font-black text-white">
                {initials}
              </span>
              <span className="min-w-0 text-left">
                <span className="block max-w-36 truncate text-sm font-black text-slate-950 dark:text-white">
                  {displayName}
                </span>
                <span className="block text-xs font-semibold text-slate-400">控制台用户</span>
              </span>
            </button>
            <Button type="button" variant="ghost" size="icon-sm" aria-label="Logout" onClick={handleLogout}>
              <LogOut className="size-4" />
            </Button>
          </div>
        </div>
      </header>
    </>
  );
}
