import { act, renderHook } from '@testing-library/react';
import type { ReactNode } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it } from 'vitest';
import { useAdminTableState } from './useAdminTableState';

function wrapper(initialPath: string) {
  return function TestWrapper({ children }: { children: ReactNode }) {
    return <MemoryRouter initialEntries={[initialPath]}>{children}</MemoryRouter>;
  };
}

describe('useAdminTableState', () => {
  it('initializes from URL params', () => {
    const { result } = renderHook(() => useAdminTableState({ storageKey: 'users' }), {
      wrapper: wrapper('/admin/users?page=3&page_size=50&search=alice'),
    });

    expect(result.current.page).toBe(3);
    expect(result.current.pageSize).toBe(50);
    expect(result.current.search).toBe('alice');
  });

  it('resets page to one when search changes', () => {
    const { result } = renderHook(() => useAdminTableState({ storageKey: 'users' }), {
      wrapper: wrapper('/admin/users?page=3&page_size=20'),
    });

    act(() => result.current.setSearch('bob'));

    expect(result.current.page).toBe(1);
    expect(result.current.search).toBe('bob');
  });

  it('persists page size to localStorage', () => {
    const { result } = renderHook(() => useAdminTableState({ storageKey: 'users' }), {
      wrapper: wrapper('/admin/users'),
    });

    act(() => result.current.setPageSize(100));

    expect(result.current.page).toBe(1);
    expect(result.current.pageSize).toBe(100);
    expect(window.localStorage.getItem('admin-table:users:page-size')).toBe('100');
  });
});
