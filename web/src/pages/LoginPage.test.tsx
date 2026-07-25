import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { describe, expect, it, vi } from 'vitest';
import { LoginPage } from './LoginPage';
import { renderWithQuery } from '@/test/render';
import { redirectToApiPath } from '@/lib/oauth';

vi.mock('@/lib/oauth', async () => {
  const actual = await vi.importActual<typeof import('@/lib/oauth')>('@/lib/oauth');
  return {
    ...actual,
    redirectToApiPath: vi.fn(),
  };
});

describe('LoginPage', () => {
  it('starts OAuth login from provider buttons', async () => {
    const user = userEvent.setup();

    renderWithQuery(
      <MemoryRouter initialEntries={['/login']}>
        <LoginPage />
      </MemoryRouter>,
    );

    await user.click(screen.getByRole('button', { name: 'GitHub' }));

    expect(redirectToApiPath).toHaveBeenCalledWith('/oauth/github');
  });
});
