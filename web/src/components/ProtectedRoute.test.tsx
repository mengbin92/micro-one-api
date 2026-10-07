import { act, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { http, HttpResponse } from 'msw';
import { describe, expect, it, vi } from 'vitest';
import { ProtectedRoute } from './ProtectedRoute';
import { SessionRolesPage } from '@/pages/SessionRolesPage';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';

vi.mock('./AppNavigation', () => ({ AppNavigation: () => null }));

describe('session role initialization route', () => {
  it('clears the previous actor draft and role selection when the credential changes', async () => {
    localStorage.setItem('token', 'first-actor-fixture');
    server.use(http.get('/api/user/authorization', ({ request }) => {
      const second = request.headers.get('authorization') === 'Bearer second-actor-fixture';
      const role = second ? { id: '2', name: 'second actor role' } : { id: '1', name: 'first actor role' };
      return HttpResponse.json({ authorization_mode: 'iam', session: { activation_state: 'selection_required', revision: '1' }, authorized_role_ids: [role.id], roles: [role], permitted_operations: [], versions: { policy_revision: '1' } });
    }));
    renderWithQuery(<MemoryRouter initialEntries={['/session-roles']}><Routes><Route element={<ProtectedRoute />}><Route path="session-roles" element={<SessionRolesPage />} /></Route></Routes></MemoryRouter>);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('checkbox', { name: 'first actor role' }));
    await user.type(screen.getByRole('textbox', { name: '变更原因' }), 'private first actor draft');
    await act(async () => {
      localStorage.setItem('token', 'second-actor-fixture');
      window.dispatchEvent(new StorageEvent('storage', { key: 'token', newValue: 'second-actor-fixture' }));
    });
    expect(await screen.findByRole('checkbox', { name: 'second actor role' })).not.toBeChecked();
    expect(screen.queryByRole('checkbox', { name: 'first actor role' })).not.toBeInTheDocument();
    expect(screen.getByRole('textbox', { name: '变更原因' })).toHaveValue('');
  });
  it('retains the conflict, reason and role selection across a forced authority recheck', async () => {
    localStorage.setItem('token', 'session-role-fixture');
    let authCalls = 0;
    let activationCalls = 0;
    let release: (() => void) | undefined;
    server.use(
      http.get('/api/user/authorization', async () => {
        authCalls++;
        if (authCalls > 1) await new Promise<void>(resolve => { release = resolve; });
        return HttpResponse.json({ authorization_mode: 'iam', session: { activation_state: 'selection_required', revision: '1' }, authorized_role_ids: ['1', '2'], roles: [{ id: '1', name: 'finance_reader' }, { id: '2', name: 'auditor' }], permitted_operations: [], versions: { policy_revision: '1' } });
      }),
      http.put('/api/user/session/roles', async ({ request }) => {
        activationCalls++;
        expect(await request.json()).toMatchObject({ role_ids: ['1', '2'], expected_revision: '1', reason: 'review conflict' });
        return HttpResponse.json({ message: 'constraint conflict' }, { status: 400 });
      }),
    );
    renderWithQuery(<MemoryRouter initialEntries={['/session-roles']}><Routes><Route element={<ProtectedRoute />}><Route path="session-roles" element={<SessionRolesPage />} /></Route></Routes></MemoryRouter>);
    const user = userEvent.setup();
    await user.click(await screen.findByRole('checkbox', { name: 'finance_reader' }));
    await user.click(screen.getByRole('checkbox', { name: 'auditor' }));
    await user.type(screen.getByRole('textbox', { name: '变更原因' }), 'review conflict');
    await user.click(screen.getByRole('button', { name: '激活所选角色' }));
    await waitFor(() => expect(authCalls).toBe(2));
    release?.();
    expect(await screen.findByRole('alert')).toHaveTextContent('constraint conflict');
    expect(screen.getByRole('checkbox', { name: 'finance_reader' })).toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'auditor' })).toBeChecked();
    expect(screen.getByRole('textbox', { name: '变更原因' })).toHaveValue('review conflict');
    expect(activationCalls).toBe(1);
  });
});
