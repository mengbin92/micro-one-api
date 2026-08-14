import { act, renderHook } from '@testing-library/react';
import type { ReactNode } from 'react';
import { MemoryRouter } from 'react-router';
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
    expect(window.localStorage.getItem('web:admin-page-size')).toBe('100');
  });

  it('initializes sort and filters from URL params', () => {
    const { result } = renderHook(
      () => useAdminTableState({ storageKey: 'users', filters: ['status', 'group'] }),
      {
        wrapper: wrapper('/admin/users?sort=username&order=desc&status=1&group=default'),
      },
    );

    expect(result.current.sortKey).toBe('username');
    expect(result.current.sortDirection).toBe('desc');
    expect(result.current.filters).toEqual({ status: '1', group: 'default' });
  });

  it('resets page to one when a filter changes', () => {
    const { result } = renderHook(
      () => useAdminTableState({ storageKey: 'users', filters: ['status'] }),
      {
        wrapper: wrapper('/admin/users?page=3&status=0'),
      },
    );

    act(() => result.current.setFilter('status', '1'));

    expect(result.current.page).toBe(1);
    expect(result.current.filters.status).toBe('1');
  });

  it('removes empty filter values from URL', () => {
    const { result } = renderHook(
      () => useAdminTableState({ storageKey: 'users', filters: ['status'] }),
      {
        wrapper: wrapper('/admin/users?page=3&status=1'),
      },
    );

    act(() => result.current.setFilter('status', ''));

    expect(result.current.page).toBe(1);
    expect(result.current.filters.status).toBeUndefined();
  });

  it('keeps earlier filters when an update arrives from a stale render closure', () => {
    // Router navigations commit asynchronously: an event handler captured by
    // an older render must not clobber a filter that was set afterwards.
    const { result } = renderHook(
      () => useAdminTableState({ storageKey: 'orders', filters: ['status', 'channel'] }),
      {
        wrapper: wrapper('/admin/payment-orders'),
      },
    );

    const staleClosure = result.current;
    act(() => result.current.setFilter('status', 'paid'));
    act(() => staleClosure.setFilter('channel', 'alipay'));

    expect(result.current.filters).toEqual({ status: 'paid', channel: 'alipay' });
  });

  it('accumulates a third update on top of two in-flight ones from stale closures', () => {
    // Regression: the payment-orders smoke test sets status, channel and
    // user_id back-to-back. A render lagging behind the router navigation
    // used to resync latestParams to the intermediate URL, dropping the
    // channel filter that was still in flight (seen on slow CI runners).
    const { result } = renderHook(
      () => useAdminTableState({ storageKey: 'orders', filters: ['status', 'channel', 'user_id'] }),
      {
        wrapper: wrapper('/admin/payment-orders'),
      },
    );

    const staleClosure = result.current;
    act(() => result.current.setFilter('status', 'paid'));
    act(() => staleClosure.setFilter('channel', 'alipay'));
    act(() => staleClosure.setFilter('user_id', '42'));

    expect(result.current.filters).toEqual({ status: 'paid', channel: 'alipay', user_id: '42' });
  });
});
