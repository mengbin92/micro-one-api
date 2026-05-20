import { useSearchParams } from 'react-router-dom';
import { getPreference, setPreference } from '@/lib/preferences';

interface UseAdminTableStateOptions {
  storageKey: string;
  defaultPageSize?: number;
}

function readPositiveInt(value: string | null, fallback: number) {
  const parsed = Number.parseInt(value || '', 10);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback;
}

export function useAdminTableState({ defaultPageSize = 20 }: UseAdminTableStateOptions) {
  const [searchParams, setSearchParams] = useSearchParams();
  const preferredPageSize = getPreference('admin-page-size', defaultPageSize);
  const page = readPositiveInt(searchParams.get('page'), 1);
  const pageSize = readPositiveInt(searchParams.get('page_size'), preferredPageSize);
  const search = searchParams.get('search') ?? '';

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
    setPage: (nextPage: number) => updateParams({ page: Math.max(1, nextPage) }),
    setPageSize: (nextPageSize: number) => {
      setPreference('admin-page-size', nextPageSize);
      updateParams({ page: 1, page_size: nextPageSize });
    },
    setSearch: (nextSearch: string) => updateParams({ page: 1, search: nextSearch.trim() }),
    clearSearch: () => updateParams({ page: 1, search: null }),
  };
}
