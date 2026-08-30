import { Menu } from 'lucide-react';
import type { ReactNode } from 'react';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog';
import { t } from '@/lib/i18n';

interface MobileNavProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  children: ReactNode;
}

export function MobileNav({ open, onOpenChange, children }: MobileNavProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger render={<Button type="button" variant="ghost" size="icon-sm" aria-label={t('打开导航')} />}>
        <Menu className="size-4" />
      </DialogTrigger>
      <DialogContent className="top-0 left-auto right-0 h-full max-w-72 translate-x-0 translate-y-0 rounded-none sm:max-w-80">
        <DialogHeader>
          <DialogTitle>{t('导航')}</DialogTitle>
          <DialogDescription>{t('打开页面或管理访问权限。')}</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-2">{children}</div>
      </DialogContent>
    </Dialog>
  );
}
