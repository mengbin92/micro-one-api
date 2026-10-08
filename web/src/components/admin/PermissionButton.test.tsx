import { useState } from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import { describe, expect, it, vi } from 'vitest';
import { authorizationKey, useAuthorizationLifecycle } from '@/lib/authorization';
import type { IAMReply } from '@/lib/iam-types';
import { server } from '@/test/msw/server';
import { Permission, PermissionButton } from './PermissionButton';
import { ExportButton } from './ExportButton';

// The affected pages all use these shared controls with their own operations.
const actions = [
  'channel.channel.update', 'identity.user.disable', 'channel.model.create',
  'channel.account.update', 'channel.routing_group.create',
  'subscription.quota_policy.update', 'subscription.plan.create',
  'subscription.user_subscription.extend', 'billing.redemption.create',
  'system.option.security.update', 'billing.pricing.update',
  'billing.upstream_cost.create', 'log.request.delete',
];
const exports = ['channel.channel.export', 'identity.user.export', 'billing.redemption.export', 'log.request.export'];
const snapshot = (): IAMReply => ({ authorization_mode: 'iam', session: { activation_state: 'active', revision: '1' }, versions: { policy_revision: '1' }, permitted_operations: [...actions, ...exports], valid_until: new Date(Date.now() + 600_000).toISOString() });

function Draft() {
  const [value, setValue] = useState('');
  return <input aria-label="local draft" value={value} onChange={event => setValue(event.target.value)} />;
}
function Controls() {
  useAuthorizationLifecycle();
  return <>
    {actions.map(operation => <PermissionButton key={operation} permission={operation}>{operation}</PermissionButton>)}
    {exports.map(operation => <ExportButton key={operation} permission={operation} label={operation} filename="data.csv" href="/test-export" />)}
    <PermissionButton permission={actions[0]} disabled>pending mutation</PermissionButton>
    <PermissionButton permission={[actions[0], 'ungranted']} any object={{ permittedActions: [actions[0]] }}>any granted</PermissionButton>
    <PermissionButton permission={[actions[0], 'ungranted']}>all required</PermissionButton>
    <PermissionButton permission={actions[0]} object={{ permitted_actions: [] }}>object denied</PermissionButton>
    <PermissionButton permission={actions[0]} object={{}}>missing object facts</PermissionButton>
    <PermissionButton permission={[actions[0], actions[1]]} object={{ permitted_actions: [actions[0]] }}>object missing one</PermissionButton>
    <Permission operation={actions[0]}><Draft /></Permission>
  </>;
}

describe('shared management controls during authorization refresh', () => {
  it.each(['iam', 'legacy'] as const)('retains controls and drafts while disabling execution in %s mode', async mode => {
    let calls = 0;
    let release: (() => void) | undefined;
    const request = vi.fn();
    const current = mode === 'iam' ? snapshot() : { ...snapshot(), authorization_mode: 'legacy', legacy_admin: true };
    server.use(http.get('/api/user/authorization', async () => {
      if (++calls > 1) await new Promise<void>(resolve => { release = resolve; });
      return HttpResponse.json(current);
    }), http.get('/api/test-export', () => { request(); return HttpResponse.text('private data'); }));
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const view = render(<QueryClientProvider client={client}><Controls /></QueryClientProvider>);
    let refreshing: Promise<void> | undefined;
    try {
      const draft = await screen.findByLabelText('local draft');
      fireEvent.change(draft, { target: { value: 'unsaved changes' } });
      const controls = [...actions, ...exports, 'any granted'].map(name => screen.getByRole('button', { name }));
      if (mode === 'iam') {
        for (const name of ['all required', 'object denied', 'missing object facts', 'object missing one']) expect(screen.queryByRole('button', { name })).not.toBeInTheDocument();
      }
      act(() => { refreshing = client.refetchQueries({ queryKey: authorizationKey }); });
      await waitFor(() => expect(calls).toBe(2));
      controls.forEach(button => {
        expect(screen.getByRole('button', { name: button.textContent! })).toBe(button);
        expect(button).toBeDisabled();
        fireEvent.click(button);
      });
      expect(request).not.toHaveBeenCalled();
      expect(screen.getByLabelText('local draft')).toBe(draft);
      expect(draft).toHaveValue('unsaved changes');
      expect(screen.getByRole('button', { name: 'pending mutation' })).toBeDisabled();
      if (mode === 'iam') expect(screen.queryByRole('button', { name: 'object denied' })).not.toBeInTheDocument();
      await act(async () => { release?.(); await refreshing; });
      await waitFor(() => controls.forEach(button => expect(button).toBeEnabled()));
      expect(screen.getByRole('button', { name: 'pending mutation' })).toBeDisabled();
      expect(draft).toHaveValue('unsaved changes');
    } finally {
      await act(async () => { release?.(); await refreshing; });
      view.unmount(); client.clear();
    }
  });

  it.each(['revoked', 'failed', 'expired', 'object_revoked'] as const)('removes protected controls when authorization is %s', async change => {
    let current = snapshot();
    let failed = false;
    server.use(http.get('/api/user/authorization', () => failed ? HttpResponse.json({}, { status: 503 }) : HttpResponse.json(current)));
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const content = (objectActions: string[]) => <QueryClientProvider client={client}><PermissionButton permission={actions[0]} object={{ permitted_actions: objectActions }}>edit</PermissionButton><ExportButton permission={exports[0]} href="/test-export" filename="data.csv" /><Permission operation={actions[0]}><Draft /></Permission></QueryClientProvider>;
    const view = render(content([actions[0]]));
    try {
      await screen.findByRole('button', { name: 'edit' });
      if (change === 'object_revoked') {
        view.rerender(content([]));
        expect(screen.queryByRole('button', { name: 'edit' })).not.toBeInTheDocument();
        expect(screen.getByRole('button', { name: /Export CSV/ })).toBeEnabled();
      } else {
        if (change === 'revoked') current = { ...current, permitted_operations: [] };
        if (change === 'expired') current = { ...current, valid_until: new Date(0).toISOString() };
        failed = change === 'failed';
        await act(async () => { await client.refetchQueries({ queryKey: authorizationKey }); });
        await waitFor(() => expect(screen.queryByRole('button', { name: 'edit' })).not.toBeInTheDocument());
        expect(screen.queryByRole('button', { name: /Export CSV/ })).not.toBeInTheDocument();
        expect(screen.queryByLabelText('local draft')).not.toBeInTheDocument();
      }
    } finally { view.unmount(); client.clear(); }
  });
});
