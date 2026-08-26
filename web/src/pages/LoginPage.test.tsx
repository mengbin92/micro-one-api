import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { beforeEach, describe, expect, it, vi } from 'vitest';
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
  beforeEach(() => {
    vi.mocked(redirectToApiPath).mockClear();
  });

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

  it('switches between login and registration with accessible tabs', async () => {
    const user = userEvent.setup();

    renderWithQuery(
      <MemoryRouter initialEntries={['/login']}>
        <LoginPage />
      </MemoryRouter>,
    );

    const loginTab = screen.getByRole('tab', { name: '登录' });
    const registerTab = screen.getByRole('tab', { name: '注册' });
    expect(loginTab).toHaveAttribute('aria-selected', 'true');
    expect(screen.queryByLabelText('确认密码')).not.toBeInTheDocument();

    await user.click(registerTab);

    expect(screen.getByRole('tab', { name: '注册' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByLabelText('确认密码')).toHaveAttribute('autocomplete', 'new-password');

    await user.keyboard('{ArrowLeft}');

    expect(screen.getByRole('tab', { name: '登录' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('tab', { name: '登录' })).toHaveFocus();
  });

  it('associates registration validation errors with the form panel', async () => {
    const user = userEvent.setup();

    renderWithQuery(
      <MemoryRouter initialEntries={['/register']}>
        <LoginPage />
      </MemoryRouter>,
    );

    await user.type(screen.getByLabelText('用户名'), 'alice');
    await user.type(screen.getByLabelText('密码'), 'password-1');
    await user.type(screen.getByLabelText('确认密码'), 'password-2');
    await user.click(screen.getByRole('button', { name: '注册账号' }));

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('两次输入的密码不一致');
    expect(screen.getByRole('tabpanel')).toHaveAttribute('aria-labelledby', 'auth-tab-register');
    expect(screen.getByRole('tabpanel').querySelector('form')).toHaveAttribute('aria-describedby', 'auth-error');
  });
});
