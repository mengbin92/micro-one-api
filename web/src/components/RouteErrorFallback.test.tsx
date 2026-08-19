import { render, screen } from '@testing-library/react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { describe, expect, it, vi } from 'vitest';
import { RouteErrorFallback } from '@/components/RouteErrorFallback';

function BrokenPage(): never {
  throw new Error('render failed');
}

describe('RouteErrorFallback', () => {
  it('shows recovery actions when a route render fails', async () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    const router = createMemoryRouter(
      [{ path: '/', element: <BrokenPage />, errorElement: <RouteErrorFallback /> }],
      { initialEntries: ['/'] },
    );

    render(<RouterProvider router={router} />);

    expect(await screen.findByRole('heading', { name: '页面出错了' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '返回首页' })).toBeInTheDocument();
    consoleError.mockRestore();
  });
});
