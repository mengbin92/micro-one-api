import type { ReactNode } from 'react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { t } from '@/lib/i18n';

interface AdminTableToolbarProps {
  search: string;
  searchPlaceholder: string;
  onSearchChange: (value: string) => void;
  onClear: () => void;
  actions?: ReactNode;
}

export function AdminTableToolbar({
  search,
  searchPlaceholder,
  onSearchChange,
  onClear,
  actions,
}: AdminTableToolbarProps) {
  return (
    <div className="flex flex-wrap items-center gap-3">
      <Input
        placeholder={searchPlaceholder}
        value={search}
        onChange={(event) => onSearchChange(event.target.value)}
        className="w-full min-w-0 sm:max-w-sm"
      />
      <Button variant="outline" onClick={onClear}>
        {t('清除')}
      </Button>
      {actions && <div className="flex flex-wrap items-center gap-2 sm:ml-auto">{actions}</div>}
    </div>
  );
}
