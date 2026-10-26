import { useEffect } from 'react';
import { act, render } from '@testing-library/react';
import { createMemoryRouter } from 'react-router';
import { describe, expect, it, vi } from 'vitest';
import { PasswordRecoveryFlowProvider } from './PasswordRecoveryFlowProvider';
import { usePasswordRecoveryFlow } from '@/hooks/usePasswordRecoveryFlow';
import { EMPTY_RECOVERY_DRAFT, type PasswordRecoveryFlow } from '@/lib/password-recovery-flow';

function Probe({ capture }: { capture: (flow: PasswordRecoveryFlow) => void }) {
  const flow = usePasswordRecoveryFlow();
  useEffect(() => capture(flow), [capture, flow]);
  return null;
}

describe('PasswordRecoveryFlowProvider', () => {
  it('keeps the draft in memory across URL scrubbing and page remounts', async () => {
    const persisted = vi.spyOn(window.localStorage, 'setItem');
    const router = createMemoryRouter([{ path: '*', element: null }], { initialEntries: ['/reset-password?email=alice%40example.com#token=flow-secret'] });
    const capture = vi.fn<(flow: PasswordRecoveryFlow) => void>();
    const view = render(<PasswordRecoveryFlowProvider router={router}><Probe key="zh" capture={capture} /></PasswordRecoveryFlowProvider>);
    const current = () => capture.mock.calls.at(-1)![0];
    act(() => current().update({ password: 'new-password', confirmation: 'new-password' }));
    await act(async () => { await router.navigate('/reset-password', { replace: true }); });
    view.rerender(<PasswordRecoveryFlowProvider router={router}><Probe key="en" capture={capture} /></PasswordRecoveryFlowProvider>);
    expect(current().draft).toEqual({ email: 'alice@example.com', token: 'flow-secret', password: 'new-password', confirmation: 'new-password', complete: false });
    expect(router.state.location.search).toBe('');
    expect(router.state.location.hash).toBe('');
    expect(router.state.location.state).toBeNull();
    expect(persisted).not.toHaveBeenCalled();
  });

  it('clears all secrets when leaving the recovery page', async () => {
    const router = createMemoryRouter([{ path: '*', element: null }], { initialEntries: ['/reset-password?email=alice%40example.com#token=flow-secret'] });
    const capture = vi.fn<(flow: PasswordRecoveryFlow) => void>();
    render(<PasswordRecoveryFlowProvider router={router}><Probe capture={capture} /></PasswordRecoveryFlowProvider>);
    const current = () => capture.mock.calls.at(-1)![0];
    const oldFlow = current();
    act(() => current().update({ password: 'new-password', confirmation: 'new-password' }));
    await act(async () => { await router.navigate('/login'); });
    expect(current().draft).toEqual(EMPTY_RECOVERY_DRAFT);
    await act(async () => { await router.navigate('/reset-password'); });
    expect(current().draft).toEqual(EMPTY_RECOVERY_DRAFT);
    expect(oldFlow.isCurrent()).toBe(false);
    act(() => oldFlow.finish());
    expect(current().draft).toEqual(EMPTY_RECOVERY_DRAFT);
  });

  it('clears completed secrets and initializes a subsequent reset link independently', async () => {
    const router = createMemoryRouter([{ path: '*', element: null }], { initialEntries: ['/reset-password?email=alice%40example.com#token=flow-secret'] });
    const capture = vi.fn<(flow: PasswordRecoveryFlow) => void>();
    render(<PasswordRecoveryFlowProvider router={router}><Probe capture={capture} /></PasswordRecoveryFlowProvider>);
    const current = () => capture.mock.calls.at(-1)![0];
    act(() => current().finish());
    expect(current().draft).toEqual({ ...EMPTY_RECOVERY_DRAFT, complete: true });
    await act(async () => { await router.navigate('/reset-password?email=bob%40example.com#token=second-flow-secret'); });
    expect(current().draft).toEqual({ ...EMPTY_RECOVERY_DRAFT, email: 'bob@example.com', token: 'second-flow-secret' });
    await act(async () => { await router.navigate('/forgot-password'); });
    expect(current().draft).toEqual(EMPTY_RECOVERY_DRAFT);
  });
});
