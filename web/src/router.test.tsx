import { screen, waitFor } from '@testing-library/react';
import { createMemoryRouter, matchRoutes, RouterProvider } from 'react-router';
import { http, HttpResponse } from 'msw';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';
import { router } from './router';

vi.mock('@/components/AppNavigation', () => ({ AppNavigation: () => null }));

describe('IAM entry route', () => {
  beforeEach(() => localStorage.setItem('token', 'iam-route-fixture'));
  it.each(['/admin/iam', '/admin/iam/'])('matches %s instead of returning a route 404', path => {
    expect(matchRoutes(router.routes, path)).not.toBeNull();
  });

  it.each([
    ['permissions', 'iam.permission.list', '/permissions', '权限目录'],
    ['roles', 'iam.role.list', '/roles', '角色与继承'],
    ['assignments', 'identity.user_role.read', '', '用户角色'],
    ['delegations', 'iam.delegation.read', '/delegations', '授权委派'],
    ['constraints', 'iam.constraint.read', '/constraints', '职责分离'],
    ['menus', 'iam.menu.list', '/menus', '菜单管理'],
    ['explain', 'iam.authorization.explain', '', '有效权限与模拟'],
    ['audits', 'iam.audit.read', '/audit-events', '授权审计'],
  ])('opens %s with a trailing slash under the matching page permission', async (section, operation, endpoint, title) => {
    server.use(http.get('/api/user/authorization', () => HttpResponse.json({ authorization_mode: 'iam', session: { activation_state: 'active' }, permitted_operations: ['admin.console.enter', operation] })));
    if (endpoint) server.use(http.get(`/api/v1/admin/iam${endpoint}`, () => HttpResponse.json({ total: '0' })));
    const entryRouter = createMemoryRouter(router.routes, { initialEntries: [`/admin/iam/${section}/`] });
    const view = renderWithQuery(<RouterProvider router={entryRouter} />);
    try {
      expect(await screen.findByRole('heading', { name: title, level: 2 })).toBeVisible();
    } finally { view.unmount(); entryRouter.dispose(); }
  });

  it.each([
    ['/admin/iam', 'iam.permission.list', 'permissions', '权限目录'],
    ['/admin/iam/', 'iam.role.list', 'roles', '角色与继承'],
    ['/admin/iam', 'iam.audit.read', 'audit-events', '授权审计'],
  ])('opens the permitted IAM page from %s with %s', async (path, operation, endpoint, title) => {
    server.use(
      http.get('/api/user/authorization', () => HttpResponse.json({ authorization_mode: 'iam', session: { activation_state: 'active' }, permitted_operations: ['admin.console.enter', operation] })),
      http.get(`/api/v1/admin/iam/${endpoint}`, () => HttpResponse.json({ total: '0' })),
    );
    const entryRouter = createMemoryRouter(router.routes, { initialEntries: [path] });
    const view = renderWithQuery(<RouterProvider router={entryRouter} />);
    try {
      expect(await screen.findByRole('heading', { name: title, level: 2 })).toBeVisible();
      await waitFor(() => expect(entryRouter.state.location.pathname).toBe(`/admin/iam/${endpoint === 'audit-events' ? 'audits' : endpoint}`));
      expect(entryRouter.state.historyAction).toBe('REPLACE');
    } finally { view.unmount(); entryRouter.dispose(); }
  });

  it.each([
    { operations: ['admin.console.enter', 'admin.overview.read'] },
    { operations: ['iam.permission.list'] },
  ])('denies IAM entry without console admission or an IAM page permission: $operations', async ({ operations }) => {
    server.use(http.get('/api/user/authorization', () => HttpResponse.json({ authorization_mode: 'iam', session: { activation_state: 'active' }, permitted_operations: operations })));
    const entryRouter = createMemoryRouter(router.routes, { initialEntries: ['/admin/iam'] });
    const view = renderWithQuery(<RouterProvider router={entryRouter} />);
    try {
      expect(await screen.findByText('需要管理员权限')).toBeVisible();
      expect(entryRouter.state.location.pathname).toBe('/admin/iam');
      expect(screen.queryByRole('heading', { name: '权限目录' })).not.toBeInTheDocument();
    } finally { view.unmount(); entryRouter.dispose(); }
  });
});
