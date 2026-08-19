import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { NotificationPanel } from '@/components/NotificationPanel';

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));

vi.mock('@/lib/api', () => ({
  adminApiClient: { get: getMock },
}));

describe('NotificationPanel', () => {
  beforeEach(() => {
    getMock.mockReset();
  });

  it('keeps cached notifications when a background refresh fails', async () => {
    let listCalls = 0;
    getMock.mockImplementation(async (url: string) => {
      if (url.includes('status=pending')) return { data: { items: [], total: 0 } };
      listCalls += 1;
      if (listCalls > 1) throw new Error('offline');
      return {
        data: {
          items: [{ id: 1, type: 'event', subject: 'Test', content: 'hello', status: 'sent' }],
          total: 1,
        },
      };
    });
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const user = userEvent.setup();

    render(
      <QueryClientProvider client={queryClient}>
        <NotificationPanel open onOpenChange={() => undefined} />
      </QueryClientProvider>,
    );

    expect(await screen.findByText('共 1 条通知')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: '刷新通知' }));
    await waitFor(() => expect(listCalls).toBe(2));

    expect(screen.getByText('共 1 条通知')).toBeInTheDocument();
    expect(screen.queryByText('通知加载失败')).not.toBeInTheDocument();
  });
});
