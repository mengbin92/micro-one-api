import { useSearchParams } from 'react-router';
import { getPreference, setPreference } from '@/lib/preferences';
import type { SortDirection } from '@/lib/table-utils';

interface UseAdminTableStateOptions {
  storageKey: string;
  defaultPageSize?: number;
  filters?: string[];
}

interface AdminTableSortUpdate {
  key: PropertyKey | null;
  direction: SortDirection;
}

function readPositiveInt(value: string | null, fallback: number) {
  const parsed = Number.parseInt(value || '', 10);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback;
}

function isSortDirection(value: string | null): value is Exclude<SortDirection, null> {
  return value === 'asc' || value === 'desc';
}

export function useAdminTableState({ defaultPageSize = 20, filters: filterKeys = [] }: UseAdminTableStateOptions) {
  const [searchParams, setSearchParams] = useSearchParams();
  const preferredPageSize = getPreference('admin-page-size', defaultPageSize);
  const page = readPositiveInt(searchParams.get('page'), 1);
  const pageSize = readPositiveInt(searchParams.get('page_size'), preferredPageSize);
  const search = searchParams.get('search') ?? '';
  const sortKey = searchParams.get('sort');
  const orderParam = searchParams.get('order');
  const sortDirection = isSortDirection(orderParam) ? orderParam : null;
  const filters = Object.fromEntries(
    filterKeys
      .map((key) => [key, searchParams.get(key)] as const)
      .filter(([, value]) => value !== null && value !== ''),
  );

  const updateParams = (updates: Record<string, string | number | null>) => {
    setSearchParams((current) => {
      const next = new URLSearchParams(current);
      for (const [key, value] of Object.entries(updates)) {
        if (value === null || value === '' || value === 1 || value === defaultPageSize) {
          next.delete(key);
        } else {
          next.set(key, String(value));
        }
      }
      return next;
    });
  };

  return {
    page,
    pageSize,
    search,
    sortKey,
    sortDirection,
    filters,
    setPage: (nextPage: number) => updateParams({ page: Math.max(1, nextPage) }),
    setPageSize: (nextPageSize: number) => {
      setPreference('admin-page-size', nextPageSize);
      updateParams({ page: 1, page_size: nextPageSize });
    },
    setSearch: (nextSearch: string) => updateParams({ page: 1, search: nextSearch.trim() }),
    clearSearch: () => updateParams({ page: 1, search: null }),
    setSort: (nextSort: AdminTableSortUpdate) =>
      updateParams({ page: 1, sort: nextSort.key === null ? null : String(nextSort.key), order: nextSort.direction }),
    setFilter: (key: string, value: string | number | null) => updateParams({ page: 1, [key]: value }),
  };
}
