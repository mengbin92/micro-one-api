import { act, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useEffect, useState } from 'react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { beforeEach, describe, expect, it } from 'vitest';
import { http, HttpResponse } from 'msw';
import { AdminRoute } from '@/components/AdminRoute';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';
import { authorizationKey, useAuthorizationLifecycle } from '@/lib/authorization';

function renderAdminRoute() {
  return renderWithQuery(
    <MemoryRouter initialEntries={['/admin']}>
      <Routes>
        <Route path="/admin" element={<AdminRoute />}>
          <Route index element={<div>admin content</div>} />
          <Route path="channels" element={<div>allowed channel content</div>} />
        </Route>
        <Route path="/session-roles" element={<div>select session roles</div>} />
      </Routes>
    </MemoryRouter>,
  );
}

function mockSelfRole(role: number) {
  server.use(
    http.get('/api/user/authorization', () => HttpResponse.json({ authorization_mode: 'legacy', legacy_admin: role >= 10 })),
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
  it('routes an IAM user without overview permission to their first permitted page', async () => {
    server.use(http.get('/api/user/authorization', () => HttpResponse.json({ authorization_mode: 'iam', session: { activation_state: 'active' }, permitted_operations: ['admin.console.enter', 'channel.channel.list'] })));
    renderAdminRoute();
    expect(await screen.findByText('allowed channel content')).toBeVisible();
    expect(screen.queryByText('admin content')).not.toBeInTheDocument();
  });
  it('requires role selection when the refreshed session is not active', async () => {
    server.use(http.get('/api/user/authorization', () => HttpResponse.json({ authorization_mode: 'iam', session: { activation_state: 'selection_required' }, permitted_operations: ['admin.console.enter'] })));
    renderAdminRoute();
    expect(await screen.findByText('select session roles')).toBeVisible();
    expect(screen.queryByText('admin content')).not.toBeInTheDocument();
  });

  it('preserves the page on unchanged checks but clears local copies when the authority version changes', async () => {
    let calls = 0;
    let release: (() => void) | undefined;
    let mounts = 0;
    const snapshot = { authorization_mode: 'iam', session: { activation_state: 'active', revision: '1' }, versions: { policy_revision: '1' }, valid_until: new Date(Date.now() + 60_000).toISOString(), permitted_operations: ['admin.console.enter', 'admin.overview.read'] };
    server.use(http.get('/api/user/authorization', async () => {
      calls++;
      if (calls > 1) await new Promise<void>(resolve => { release = resolve; });
      return HttpResponse.json(snapshot);
    }));
    function Shell() { useAuthorizationLifecycle(); return <AdminRoute />; }
    function Editor() {
      const [draft, setDraft] = useState('');
      useEffect(() => { mounts++; }, []);
      return <input aria-label="draft" value={draft} onChange={event => setDraft(event.target.value)} />;
    }
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const view = render(<QueryClientProvider client={client}><MemoryRouter initialEntries={['/admin']}><Routes><Route path="/admin" element={<Shell />}><Route index element={<Editor />} /></Route></Routes></MemoryRouter></QueryClientProvider>);
    let refreshing: Promise<void> | undefined;
    try {
      const input = await screen.findByLabelText('draft');
      await userEvent.setup().type(input, 'unsaved text');
      act(() => { refreshing = client.refetchQueries({ queryKey: authorizationKey }); });
      await waitFor(() => expect(calls).toBe(2));
      expect(screen.getByLabelText('draft')).toBe(input);
      expect(input).toHaveValue('unsaved text');
      await act(async () => { release?.(); await refreshing; });
      expect(screen.getByLabelText('draft')).toHaveValue('unsaved text');
      expect(mounts).toBe(1);
      snapshot.versions.policy_revision = '2';
      act(() => { refreshing = client.refetchQueries({ queryKey: authorizationKey }); });
      await waitFor(() => expect(calls).toBe(3));
      await act(async () => { release?.(); await refreshing; });
      await waitFor(() => expect(screen.getByLabelText('draft')).toHaveValue(''));
      expect(mounts).toBe(2);
    } finally {
      await act(async () => { release?.(); await refreshing; });
      view.unmount(); client.clear();
    }
  });
});
