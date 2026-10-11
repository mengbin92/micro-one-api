import { QueryClient, QueryObserver } from '@tanstack/react-query';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { apiClient } from './api';
import { accountDashboardQueryOptions, subscriptionProgressQueryOptions } from './account-queries';

afterEach(() => vi.useRealTimers());

describe('account query time windows', () => {
  it.each(['daily reset', 'expiry'])('refreshes a mounted subscription at its %s', async (boundary) => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-10-11T01:59:59Z'));
    const next = Date.now() / 1000 + 1;
    const before = {
      id: 7, status: 'active', starts_at: next - 86400,
      expires_at: boundary === 'expiry' ? next : next + 86400,
      daily_used: { used: 38.46, window_start: next - 86400, next_refresh: boundary === 'daily reset' ? next : next + 86400 },
    };
    const after = boundary === 'expiry' ? null : {
      ...before, daily_used: { used: 0, window_start: next, next_refresh: next + 86400 },
    };
    const get = vi.spyOn(apiClient, 'get')
      .mockResolvedValueOnce({ data: { success: true, data: before } })
      .mockResolvedValue({ data: after ? { success: true, data: after } : { success: false, message: 'subscription not found' } });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const observer = new QueryObserver(client, subscriptionProgressQueryOptions('42'));
    const unsubscribe = observer.subscribe(() => {});
    try {
      await vi.advanceTimersByTimeAsync(0);
      expect(observer.getCurrentResult().data).toEqual(before);
      await vi.advanceTimersByTimeAsync(1000);
      expect(get).toHaveBeenCalledTimes(2);
      expect(observer.getCurrentResult().data).toEqual(after);
      if (boundary === 'expiry') {
        await vi.advanceTimersByTimeAsync(60_000);
        expect(get).toHaveBeenCalledTimes(2);
      }
    } finally {
      unsubscribe();
      client.clear();
    }
  });

  it('refreshes a mounted calendar-day dashboard at UTC midnight', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-10-11T23:59:59Z'));
    const get = vi.spyOn(apiClient, 'get')
      .mockResolvedValueOnce({ data: { success: true, data: { today_amount: 10249 } } })
      .mockResolvedValue({ data: { success: true, data: { today_amount: 0 } } });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const observer = new QueryObserver(client, accountDashboardQueryOptions);
    const unsubscribe = observer.subscribe(() => {});
    try {
      await vi.advanceTimersByTimeAsync(0);
      expect(observer.getCurrentResult().data?.today_amount).toBe(10249);
      await vi.advanceTimersByTimeAsync(1000);
      expect(get).toHaveBeenCalledTimes(2);
      expect(observer.getCurrentResult().data?.today_amount).toBe(0);
    } finally {
      unsubscribe();
      client.clear();
    }
  });
});
