import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import type { DataRouter, Location } from 'react-router';
import { EMPTY_RECOVERY_DRAFT, PasswordRecoveryFlowContext } from '@/lib/password-recovery-flow';

function draftFromLocation(location: Location) {
  if (location.pathname !== '/reset-password') return EMPTY_RECOVERY_DRAFT;
  const query = new URLSearchParams(location.search);
  const fragment = new URLSearchParams(location.hash.slice(1));
  return { ...EMPTY_RECOVERY_DRAFT, email: query.get('email') ?? '', token: fragment.get('token') ?? query.get('token') ?? '' };
}

// This provider stays above the locale-keyed RouterProvider. Recovery secrets
// survive a language switch in memory; they never enter browser storage or
// history state, and navigation out of the current recovery page clears them.
export function PasswordRecoveryFlowProvider({ router, children }: { router: Pick<DataRouter, 'state' | 'subscribe'>; children: ReactNode }) {
  const generation = useRef(0);
  const [state, setState] = useState(() => ({ draft: draftFromLocation(router.state.location), generation: 0 }));

  useEffect(() => {
    let previous = router.state.location;
    return router.subscribe(({ location }) => {
      const changedPage = previous.pathname !== location.pathname;
      const newLink = location.key !== previous.key && !!(location.search || location.hash);
      previous = location;
      if (changedPage || newLink) {
        generation.current++;
        setState({ draft: draftFromLocation(location), generation: generation.current });
      }
    });
  }, [router]);

  const value = useMemo(() => ({
    draft: state.draft,
    // Old in-flight requests must not update a later recovery flow or clear
    // a session established after the user navigated away.
    isCurrent: () => generation.current === state.generation,
    update: (patch: Partial<typeof state.draft>) => setState(current => current.generation === state.generation
      ? { ...current, draft: { ...current.draft, ...patch } } : current),
    finish: () => setState(current => current.generation === state.generation
      ? { ...current, draft: { ...EMPTY_RECOVERY_DRAFT, complete: true } } : current),
  }), [state]);
  return <PasswordRecoveryFlowContext.Provider value={value}>{children}</PasswordRecoveryFlowContext.Provider>;
}
