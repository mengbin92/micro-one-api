import { screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';
import { beforeEach, describe, expect, it } from 'vitest';
import { http, HttpResponse } from 'msw';
import { AdminRoute } from '@/components/AdminRoute';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';

function renderAdminRoute() {
  return renderWithQuery(
    <MemoryRouter initialEntries={['/admin']}>
      <Routes>
        <Route path="/admin" element={<AdminRoute />}>
          <Route index element={<div>admin content</div>} />
        </Route>
      </Routes>
    </MemoryRouter>,
  );
}

function mockSelfRole(role: number) {
  server.use(
    http.get('/api/user/self', () =>
      HttpResponse.json({
        success: true,
        data: { id: 7, username: 'alice', display_name: 'Alice', email: '', group: 'default', status: 1, role },
      }),
    ),
  );
}

describe('AdminRoute', () => {
  beforeEach(() => window.localStorage.clear());

  it('does not trust a stale admin role from local storage', async () => {
    window.localStorage.setItem('userRole', '10');
    mockSelfRole(1);

    renderAdminRoute();

    expect(await screen.findByText('需要管理员权限')).toBeInTheDocument();
    expect(screen.queryByText('admin content')).not.toBeInTheDocument();
  });

  it('renders admin content after the current session role is verified', async () => {
    mockSelfRole(10);

    renderAdminRoute();

    expect(await screen.findByText('admin content')).toBeInTheDocument();
  });
});
