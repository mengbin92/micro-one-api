import { NavLink, useNavigate } from 'react-router-dom';
import { useState } from 'react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { ThemeToggle } from '@/components/ThemeToggle';
import { MobileNav } from '@/components/MobileNav';
import { useMediaQuery } from '@/hooks/useMediaQuery';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog';
import { cn } from '@/lib/utils';

const userLinks = [
  { to: '/dashboard', label: 'Dashboard' },
  { to: '/tokens', label: 'Tokens' },
];

const adminLinks = [
  { to: '/admin/users', label: 'Users' },
  { to: '/admin/channels', label: 'Channels' },
  { to: '/admin/logs', label: 'Logs' },
  { to: '/admin/redemptions', label: 'Redemptions' },
  { to: '/admin/options', label: 'Options' },
];

function NavigationLinks({
  isAdmin,
  onNavigate,
  stacked = false,
}: {
  isAdmin: boolean;
  onNavigate?: () => void;
  stacked?: boolean;
}) {
  const navLinkClass = ({ isActive }: { isActive: boolean }) =>
    cn(
      'rounded-md text-sm transition-colors',
      stacked ? 'px-2 py-2' : 'px-0 py-1',
      isActive ? 'text-foreground' : 'text-muted-foreground hover:text-foreground'
    );

  return (
    <div className={cn(stacked ? 'flex flex-col gap-1' : 'flex items-center gap-6')}>
      {userLinks.map((link) => (
        <NavLink key={link.to} to={link.to} className={navLinkClass} onClick={onNavigate}>
          {link.label}
        </NavLink>
      ))}
      {isAdmin && (
        <>
          {!stacked && <span className="text-muted-foreground">|</span>}
          {adminLinks.map((link) => (
            <NavLink key={link.to} to={link.to} className={navLinkClass} onClick={onNavigate}>
              {link.label}
            </NavLink>
          ))}
        </>
      )}
    </div>
  );
}

export function AppNavigation() {
  const navigate = useNavigate();
  const [adminToken, setAdminToken] = useState(localStorage.getItem('adminToken') || '');
  const [adminDialogOpen, setAdminDialogOpen] = useState(false);
  const [mobileOpen, setMobileOpen] = useState(false);
  const [adminInput, setAdminInput] = useState('');
  const isWide = useMediaQuery('(min-width: 768px)');
  const isAdmin = !!adminToken;
  const effectiveMobileOpen = !isWide && mobileOpen;

  const handleSetAdminToken = () => {
    localStorage.setItem('adminToken', adminInput);
    setAdminToken(adminInput);
    setAdminInput('');
    setAdminDialogOpen(false);
    setMobileOpen(false);
    toast.success('Admin access enabled');
  };

  const handleClearAdminToken = () => {
    localStorage.removeItem('adminToken');
    setAdminToken('');
    setMobileOpen(false);
    toast.success('Admin access disabled');
  };

  const handleLogout = () => {
    localStorage.removeItem('token');
    localStorage.removeItem('adminToken');
    navigate('/login', { replace: true });
  };

  const adminControl = isAdmin ? (
    <Button variant="outline" size="sm" onClick={handleClearAdminToken}>
      Exit Admin
    </Button>
  ) : (
    <Dialog open={adminDialogOpen} onOpenChange={setAdminDialogOpen}>
      <DialogTrigger render={<Button variant="outline" size="sm" />}>
        Admin
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Admin Access</DialogTitle>
          <DialogDescription>Enter your admin token to access management features.</DialogDescription>
        </DialogHeader>
        <div className="space-y-4 pt-4">
          <Input
            type="password"
            placeholder="Admin Token"
            value={adminInput}
            onChange={(event) => setAdminInput(event.target.value)}
          />
          <Button onClick={handleSetAdminToken} disabled={!adminInput.trim()} className="w-full">
            Confirm
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );

  return (
    <nav className="border-b">
      <div className="container mx-auto flex items-center gap-4 px-4 py-3">
        <h1 className="text-lg font-semibold">One API</h1>
        <div className="hidden md:block">
          <NavigationLinks isAdmin={isAdmin} />
        </div>
        <div className="ml-auto hidden items-center gap-3 md:flex">
          <ThemeToggle />
          {adminControl}
          <Button type="button" variant="ghost" size="sm" onClick={handleLogout}>
            Logout
          </Button>
        </div>
        <div className="ml-auto flex items-center gap-2 md:hidden">
          <ThemeToggle />
          <MobileNav open={effectiveMobileOpen} onOpenChange={setMobileOpen}>
            <NavigationLinks isAdmin={isAdmin} stacked onNavigate={() => setMobileOpen(false)} />
            <div className="mt-2 flex flex-col gap-2 border-t pt-3">
              {adminControl}
              <Button type="button" variant="ghost" size="sm" onClick={handleLogout}>
                Logout
              </Button>
            </div>
          </MobileNav>
        </div>
      </div>
    </nav>
  );
}
