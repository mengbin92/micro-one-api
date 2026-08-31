import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { beforeEach, describe, expect, it } from 'vitest';
import { http, HttpResponse } from 'msw';
import { AppNavigation } from './AppNavigation';
import { server } from '@/test/msw/server';
import { renderWithQuery } from '@/test/render';

function renderNavigation(initialPath = '/dashboard') {
  return renderWithQuery(
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
    http.get('/api/admin/notifications', () => HttpResponse.json({ items: [], total: 0 })),
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

    expect(screen.getByRole('link', { name: '仪表盘' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'API 密钥' })).toBeInTheDocument();
  });

  it('shows admin links when the current user has admin role', async () => {
    window.localStorage.setItem('userRole', '10');
    mockSelf(10);

    renderNavigation();

    expect(await screen.findByRole('link', { name: '用户' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: '订单' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: '设置' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: '进入管理' })).toHaveAttribute('href', '/admin');
  });

  it('only highlights admin overview on the exact admin route', async () => {
    window.localStorage.setItem('userRole', '10');
    mockSelf(10);

    renderNavigation('/admin/users');

    const overviewLink = await screen.findByRole('link', { name: '总览' });
    const usersLink = screen.getByRole('link', { name: '用户' });
    expect(overviewLink).not.toHaveAttribute('aria-current');
    expect(usersLink).toHaveAttribute('aria-current', 'page');
  });

  it('hides admin nav and entry link for non-admin users', async () => {
    window.localStorage.setItem('userRole', '1');
    mockSelf(1);

    renderNavigation();

    expect(await screen.findByRole('heading', { name: '仪表盘' })).toBeInTheDocument();
    expect(screen.queryByText('管理后台')).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: '用户' })).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: '进入管理' })).not.toBeInTheDocument();
  });

  it('persists role and user id after fetching self', async () => {
    mockSelf(10, 42);

    renderNavigation();

    expect(await screen.findByRole('link', { name: '用户' })).toBeInTheDocument();
    expect(window.localStorage.getItem('userRole')).toBe('10');
    expect(window.localStorage.getItem('userId')).toBe('42');
  });

  it('opens and closes the mobile menu', async () => {
    mockSelfAndDashboardEmpty();
    const user = userEvent.setup();
    renderNavigation();

    await user.click(screen.getByRole('button', { name: '打开导航' }));
    expect(screen.getByRole('dialog')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: '关闭' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('switches language and persists the preference', async () => {
    mockSelfAndDashboardEmpty();
    const user = userEvent.setup();
    renderNavigation();

    expect(screen.getByRole('heading', { name: '仪表盘' })).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: '切换至英文' }));

    expect(await screen.findByRole('heading', { name: 'Dashboard' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Dashboard' })).toBeInTheDocument();
    expect(window.localStorage.getItem('web:language')).toBe(JSON.stringify('en-US'));
    expect(document.documentElement.lang).toBe('en-US');
  });

  it('logout clears session state and redirects to login', async () => {
    mockSelfAndDashboardEmpty();
    const user = userEvent.setup();
    window.localStorage.setItem('token', 'user-token');
    window.localStorage.setItem('userId', '42');
    window.localStorage.setItem('userRole', '10');
    renderNavigation();

    await user.click(screen.getByRole('button', { name: '退出登录' }));

    expect(window.localStorage.getItem('token')).toBeNull();
    expect(window.localStorage.getItem('userId')).toBeNull();
    expect(window.localStorage.getItem('userRole')).toBeNull();
  });
});
