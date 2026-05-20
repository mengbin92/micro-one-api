import type { ReactNode } from 'react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';

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
    <div className="flex flex-wrap items-center gap-4">
      <Input
        placeholder={searchPlaceholder}
        value={search}
        onChange={(event) => onSearchChange(event.target.value)}
        className="max-w-sm"
      />
      <Button variant="outline" onClick={onClear}>
        Clear
      </Button>
      {actions && <div className="ml-auto">{actions}</div>}
    </div>
  );
}
