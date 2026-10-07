import { useEffect, useMemo } from 'react';
import { useQuery, useQueryClient, type QueryClient, type UseQueryOptions } from '@tanstack/react-query';
import { prepareManagedWrite } from '@/lib/admin-write';
import { apiClient } from '@/lib/api';
import type { IAMReply } from '@/lib/iam-types';
import { platformContext } from '@/lib/iam-types';
import { AUTHORIZATION_REFRESH, stopProtectedRequests, setWritePreparer } from '@/lib/authorization-events';

export const authorizationKey = ['authorization', 'platform'] as const;
// A credential change produces a new cache identity without putting a JWT in devtools.
let lastCredential: string | null = null;
let identityGeneration = 0;
function identityKey() {
  const credential = localStorage.getItem('token');
  if (credential !== lastCredential) { lastCredential = credential; identityGeneration++; }
  return identityGeneration;
}
export function can(snapshot: IAMReply | undefined, operation: string) {
  if (!snapshot || !snapshot.authorization_mode || (snapshot.valid_until && Date.parse(snapshot.valid_until) <= Date.now())) return false;
  if (snapshot.authorization_mode === 'legacy') return !!snapshot.legacy_admin && !operation.startsWith('iam.');
  return snapshot.authorization_mode === 'iam' && snapshot.session?.activation_state === 'active' && !!snapshot.permitted_operations?.includes(operation);
}
export function canAll(snapshot: IAMReply | undefined, operations: readonly string[]) { return operations.every(op => can(snapshot, op)); }
export function canAny(snapshot: IAMReply | undefined, operations: readonly string[]) { return operations.some(op => can(snapshot, op)); }
function currentSummary(summary: IAMReply | undefined) {
  if (!summary || !['legacy', 'iam'].includes(summary.authorization_mode ?? '')) return undefined;
  if (summary.valid_until && (!Number.isFinite(Date.parse(summary.valid_until)) || Date.parse(summary.valid_until) <= Date.now())) return undefined;
  return summary;
}
// Ignore renewed deadlines and ordering of role/operation sets, but include
// every version, identity and activation boundary. Menus remain live display
// data; changing their order alone must not invalidate resource reads.
// A changed grant or session must invalidate data even if versions are equal.
function summaryIdentity(summary: IAMReply | undefined) {
  if (!summary) return '';
  return JSON.stringify([
    summary.authorization_mode, summary.legacy_admin,
    Object.entries(summary.versions ?? {}).sort(([a], [b]) => a.localeCompare(b)),
    summary.session?.session_id, summary.session?.user_id, summary.session?.revision, summary.session?.activation_state,
    summary.session?.context?.context_type, summary.session?.context?.context_key, summary.session?.context?.organization_id,
    [...summary.permitted_operations ?? []].sort(),
    [...summary.authorized_role_ids ?? []].sort(), [...summary.active_role_ids ?? []].sort(),
  ]);
}
export function suspendProtectedQueries(client: QueryClient) {
  stopProtectedRequests();
  const predicate = (q: { meta?: Record<string, unknown> }) => q.meta?.protected === true;
  void client.cancelQueries({ predicate });
  client.removeQueries({ predicate });
}

export function useAuthorization() {
  const client = useQueryClient();
  const identity = identityKey();
  const query = useQuery({
    queryKey: [...authorizationKey, identity],
    queryFn: async ({ signal }) => {
      const previous = currentSummary(client.getQueryData<IAMReply>([...authorizationKey, identity]));
      if (!previous) suspendProtectedQueries(client);
      try {
        const response = await apiClient.get<IAMReply>('/user/authorization', { params: { 'context.context_type': platformContext.context_type, 'context.context_key': platformContext.context_key }, signal });
        if (!['legacy', 'iam'].includes(response.data.authorization_mode ?? '')) throw new Error('授权状态不可用');
        if (!currentSummary(response.data) || summaryIdentity(previous) !== summaryIdentity(response.data)) suspendProtectedQueries(client);
        return response.data;
      } catch (error) {
        // A canceled older request must not clear a newer identity's data.
        if (!signal.aborted) suspendProtectedQueries(client);
        throw error;
      }
    },
    retry: false,
    staleTime: 15_000,
    refetchInterval: 30_000,
    refetchOnWindowFocus: true,
    meta: { suppressErrorToast: true },
  });
  // Background refresh retains already verified presentation and read-cache
  // identity. Mutation/preview gates still use the fresh, non-fetching snapshot.
  const displaySnapshot = !query.isError ? currentSummary(query.data) : undefined;
  const snapshot = !query.isFetching ? displaySnapshot : undefined;
  const generation = useMemo(() => `${identity}:${summaryIdentity(displaySnapshot)}`, [identity, displaySnapshot]);
  return { ...query, identity, snapshot, displaySnapshot, generation, contextKey: 'platform', can: (op: string) => can(snapshot, op), canAll: (ops: readonly string[]) => canAll(snapshot, ops), canAny: (ops: readonly string[]) => canAny(snapshot, ops), displayCan: (op: string) => can(displaySnapshot, op), displayCanAll: (ops: readonly string[]) => canAll(displaySnapshot, ops), refresh: async () => { suspendProtectedQueries(client); await client.resetQueries({ queryKey: [...authorizationKey, identity], exact: true }); } };
}

// Mount once for the authenticated shell. Expiry and 403 synchronously remove
// the old generation; a failed authorization request never recursively retries.
export function useAuthorizationLifecycle() {
  const client = useQueryClient();
  const auth = useAuthorization();
  useEffect(() => {
    setWritePreparer(config => {
      // Read the live cache state, not the last React render. A refresh or
      // credential change must block writes before effects/buttons update.
      const state = client.getQueryState<IAMReply>([...authorizationKey, identityKey()]);
      const snapshot = state?.status === 'success' && state.fetchStatus === 'idle' ? currentSummary(state.data) : undefined;
      const method = config.method?.toLowerCase() ?? 'get';
      if (!['get', 'head', 'options'].includes(method) && (!snapshot || (snapshot.authorization_mode === 'iam' && snapshot.session?.activation_state !== 'active'))) throw new Error('授权状态不可用');
      return prepareManagedWrite(config, snapshot, client);
    });
    return () => setWritePreparer(undefined);
  }, [client]);
  useEffect(() => {
    const refresh = (event: Event) => {
      if (event instanceof StorageEvent && event.key !== null && event.key !== 'token') return;
      suspendProtectedQueries(client);
      void client.resetQueries({ queryKey: authorizationKey });
    };
    window.addEventListener(AUTHORIZATION_REFRESH, refresh);
    window.addEventListener('storage', refresh);
    return () => { window.removeEventListener(AUTHORIZATION_REFRESH, refresh); window.removeEventListener('storage', refresh); };
  }, [client]);
  useEffect(() => {
    if (!auth.displaySnapshot?.valid_until) return;
    const delay = Math.max(0, Date.parse(auth.displaySnapshot.valid_until) - Date.now());
    const timer = window.setTimeout(() => {
      suspendProtectedQueries(client);
      void client.resetQueries({ queryKey: authorizationKey });
    }, Math.min(delay, 2_147_483_647));
    return () => window.clearTimeout(timer);
  }, [client, auth.displaySnapshot?.valid_until]);
  return auth;
}

export function useAuthorizedQuery<T>(options: UseQueryOptions<T> & { permission: string | readonly string[] }) {
  const auth = useAuthorization();
  const client = useQueryClient();
  const configuredRetry = options.retry ?? client.getDefaultOptions().queries?.retry ?? 1;
  const { permission, ...rest } = options;
  // These are reads: owners reauthorize every request. A still-valid summary
  // can retain their cache during polling; failed/expired/explicitly invalidated
  // summaries never enable reads or preserve sensitive data.
  const allowed = typeof permission === 'string' ? auth.displayCan(permission) : auth.displayCanAll(permission);
  const queryKey = [...options.queryKey, 'authorization', auth.contextKey, auth.generation];
  // A 403 clears protected queries and revalidates the summary. Keep the
  // rejection outside that cache so an unchanged summary cannot recreate the
  // same failing read indefinitely, including after the route remounts.
  const denialKey = ['authorization-denial', ...queryKey];
  const denial = useQuery<Error | null>({ queryKey: denialKey, queryFn: () => null, enabled: false });
  const result = useQuery({
    ...rest,
    queryKey,
    enabled: allowed && options.enabled !== false,
    retry: (count, error) => {
      if ((error as { response?: { status?: number } }).response?.status === 403) return false;
      return typeof configuredRetry === 'function' ? configuredRetry(count, error) : configuredRetry === true || (typeof configuredRetry === 'number' && count < configuredRetry);
    },
    meta: { ...options.meta, protected: true },
    queryFn: typeof options.queryFn === 'function' ? async (ctx) => {
      if (!allowed) throw new Error('没有执行此查询的权限');
      const rejection = client.getQueryData<Error>(denialKey);
      if (rejection) throw rejection;
      try {
        return await (options.queryFn as (context: Parameters<Exclude<typeof options.queryFn, undefined | symbol>>[0]) => T | Promise<T>)(ctx);
      } catch (error) {
        if ((error as { response?: { status?: number } }).response?.status === 403) {
          // Retain only a safe rejection marker, never the Axios request's JWT
          // or response body, across protected-cache invalidation.
          client.setQueryData(denialKey, Object.assign(new Error('没有执行此查询的权限'), { response: { status: 403 } }));
        }
        throw error;
      }
    } : options.queryFn,
  });
  const waitingForSummary = auth.isPending || (auth.isFetching && !auth.displaySnapshot);
  const displayedResult = denial.data ? {
    ...result,
    data: undefined,
    error: denial.data,
    isError: true as const,
    isSuccess: false as const,
    status: 'error' as const,
  } : result;
  return {
    ...displayedResult,
    isLoading: (!denial.data && result.isLoading) || waitingForSummary,
    isRestricted: !allowed && !waitingForSummary,
    isPending: (allowed && !denial.data && result.isPending) || waitingForSummary,
    refetch: (...args: Parameters<typeof result.refetch>) => {
      // Revalidation can change the cache before React repaints the button.
      const identity = identityKey();
      const state = client.getQueryState<IAMReply>([...authorizationKey, identity]);
      const snapshot = state?.status === 'success' && state.fetchStatus === 'idle' ? currentSummary(state.data) : undefined;
      if (`${identity}:${summaryIdentity(snapshot)}` !== auth.generation || (typeof permission === 'string' ? !can(snapshot, permission) : !canAll(snapshot, permission))) return Promise.resolve(result);
      client.setQueryData(denialKey, null);
      return result.refetch(...args);
    },
  };
}
