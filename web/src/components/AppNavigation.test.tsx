import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it } from 'vitest';
import { http, HttpResponse } from 'msw';
import { AppNavigation } from './AppNavigation';
import { server } from '@/test/msw/server';

function renderNavigation(initialPath = '/dashboard') {
  return render(
    <MemoryRouter initialEntries={[initialPath]}>
      <AppNavigation />
    </MemoryRouter>
  );
}

function mockSelf(role: number, id = 7) {
  server.use(
    http.get('/api/user/self', () =>
      HttpResponse.json({ success: true, data: { id, username: 'alice', display_name: 'Alice', role } }),
    ),
    http.get('/api/user/dashboard', () => HttpResponse.json({ success: true, data: null })),
  );
}

function mockSelfAndDashboardEmpty() {
  server.use(
    http.get('/api/user/self', () => HttpResponse.json({ success: true, data: null })),
    http.get('/api/user/dashboard', () => HttpResponse.json({ success: true, data: null })),
  );
}

describe('AppNavigation', () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  it('renders user navigation links', () => {
    mockSelfAndDashboardEmpty();
    renderNavigation();

    expect(screen.getByRole('link', { name: 'Dashboard' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Tokens' })).toBeInTheDocument();
  });

  it('shows admin links when the current user has admin role', async () => {
    window.localStorage.setItem('userRole', '10');
    mockSelf(10);

    renderNavigation();

    expect(await screen.findByRole('link', { name: 'Users' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Payment Orders' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Options' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: '进入管理' })).toHaveAttribute('href', '/admin');
  });

  it('only highlights admin overview on the exact admin route', async () => {
    window.localStorage.setItem('userRole', '10');
    mockSelf(10);

    renderNavigation('/admin/users');

    const overviewLink = await screen.findByRole('link', { name: 'Admin Overview' });
    const usersLink = screen.getByRole('link', { name: 'Users' });
    expect(overviewLink).not.toHaveClass('text-blue-600');
    expect(usersLink).toHaveClass('text-blue-600');
  });

  it('hides admin nav and entry link for non-admin users', async () => {
    window.localStorage.setItem('userRole', '1');
    mockSelf(1);

    renderNavigation();

    expect(await screen.findByRole('heading', { name: '仪表盘' })).toBeInTheDocument();
    expect(screen.queryByText('管理后台')).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'Users' })).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: '进入管理' })).not.toBeInTheDocument();
  });

  it('persists role and user id after fetching self', async () => {
    mockSelf(10, 42);

    renderNavigation();

    expect(await screen.findByRole('link', { name: 'Users' })).toBeInTheDocument();
    expect(window.localStorage.getItem('userRole')).toBe('10');
    expect(window.localStorage.getItem('userId')).toBe('42');
  });

  it('opens and closes the mobile menu', async () => {
    mockSelfAndDashboardEmpty();
    const user = userEvent.setup();
    renderNavigation();

    await user.click(screen.getByRole('button', { name: /open navigation/i }));
    expect(screen.getByRole('dialog')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: /close/i }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('logout clears session state and redirects to login', async () => {
    mockSelfAndDashboardEmpty();
    const user = userEvent.setup();
    window.localStorage.setItem('token', 'user-token');
    window.localStorage.setItem('userId', '42');
    window.localStorage.setItem('userRole', '10');
    renderNavigation();

    await user.click(screen.getByRole('button', { name: 'Logout' }));

    expect(window.localStorage.getItem('token')).toBeNull();
    expect(window.localStorage.getItem('userId')).toBeNull();
    expect(window.localStorage.getItem('userRole')).toBeNull();
  });
});
