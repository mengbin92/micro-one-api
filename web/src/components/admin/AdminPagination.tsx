import { Button } from '@/components/ui/button';

interface AdminPaginationProps {
  page: number;
  pageSize: number;
  hasNextPage: boolean;
  onPageChange: (page: number) => void;
  onPageSizeChange: (pageSize: number) => void;
}

export function AdminPagination({
  page,
  pageSize,
  hasNextPage,
  onPageChange,
  onPageSizeChange,
}: AdminPaginationProps) {
  return (
    <div className="flex items-center justify-between gap-4">
      <Button variant="outline" onClick={() => onPageChange(Math.max(1, page - 1))} disabled={page === 1}>
        Previous
      </Button>
      <div className="flex items-center gap-3 text-sm text-muted-foreground">
        <span>Page {page}</span>
        <select
          value={pageSize}
          onChange={(event) => onPageSizeChange(Number.parseInt(event.target.value, 10))}
          className="h-8 rounded-md border bg-background px-2 text-sm text-foreground"
          aria-label="Rows per page"
        >
          {[20, 50, 100].map((size) => (
            <option key={size} value={size}>
              {size} / page
            </option>
          ))}
        </select>
      </div>
      <Button variant="outline" onClick={() => onPageChange(page + 1)} disabled={!hasNextPage}>
        Next
      </Button>
    </div>
  );
}
