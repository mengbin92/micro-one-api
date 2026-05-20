import type { ReactNode } from 'react';
import { ArrowDown, ArrowUp, ChevronsUpDown } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { TableHead } from '@/components/ui/table';
import { getNextSortDirection, type SortState } from '@/lib/table-utils';

interface SortableHeaderProps<T extends object> {
  columnKey: keyof T;
  sort: SortState<T>;
  onSortChange: (sort: SortState<T>) => void;
  children: ReactNode;
  className?: string;
}

export function SortableHeader<T extends object>({
  columnKey,
  sort,
  onSortChange,
  children,
  className,
}: SortableHeaderProps<T>) {
  const isActive = sort.key === columnKey;
  const direction = isActive ? sort.direction : null;
  const Icon = direction === 'asc' ? ArrowUp : direction === 'desc' ? ArrowDown : ChevronsUpDown;

  return (
    <TableHead className={className}>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        className="-ml-2 h-7 px-2"
        aria-label={`Sort by ${String(children)}`}
        onClick={() => onSortChange({ key: columnKey, direction: getNextSortDirection(direction) })}
      >
        <span>{children}</span>
        <Icon className="size-3.5 text-muted-foreground" />
      </Button>
    </TableHead>
  );
}
