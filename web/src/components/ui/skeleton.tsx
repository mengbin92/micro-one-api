import { cn } from '@/lib/utils';

function Skeleton({ className, ...props }: React.ComponentProps<'div'>) {
  return <div data-slot="skeleton" className={cn('relative overflow-hidden rounded-xl bg-muted', className)} {...props} />;
}

export { Skeleton };
