import { afterEach, describe, expect, it, vi } from 'vitest';
import { toast } from 'sonner';
import { queryClient } from '@/lib/query-client';

vi.mock('sonner', () => ({
  toast: { error: vi.fn() },
}));

describe('queryClient error notifications', () => {
  afterEach(() => {
    queryClient.clear();
    vi.clearAllMocks();
  });

  it('suppresses toast for background polling queries', async () => {
    await expect(
      queryClient.fetchQuery({
        queryKey: ['silent-poll'],
        queryFn: async () => {
          throw new Error('offline');
        },
        retry: false,
        meta: { suppressErrorToast: true },
      }),
    ).rejects.toThrow('offline');

    expect(toast.error).not.toHaveBeenCalled();
  });

  it('keeps the global toast for normal query failures', async () => {
    await expect(
      queryClient.fetchQuery({
        queryKey: ['visible-error'],
        queryFn: async () => {
          throw new Error('failed');
        },
        retry: false,
      }),
    ).rejects.toThrow('failed');

    expect(toast.error).toHaveBeenCalledOnce();
  });
});
