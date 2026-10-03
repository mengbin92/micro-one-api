import { useEffect } from 'react';
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
      suspendProtectedQueries(client);
      const response = await apiClient.get<IAMReply>('/user/authorization', { params: { 'context.context_type': platformContext.context_type, 'context.context_key': platformContext.context_key }, signal });
      if (!['legacy', 'iam'].includes(response.data.authorization_mode ?? '')) throw new Error('授权状态不可用');
      return response.data;
    },
    retry: false,
    staleTime: 15_000,
    refetchInterval: 30_000,
    refetchOnWindowFocus: true,
    meta: { suppressErrorToast: true },
  });
  const snapshot = !query.isError && !query.isFetching ? query.data : undefined;
  const generation = [identity, ...Object.values(snapshot?.versions ?? {})].join(':');
  return { ...query, snapshot, generation, contextKey: 'platform', can: (op: string) => can(snapshot, op), canAll: (ops: readonly string[]) => canAll(snapshot, ops), canAny: (ops: readonly string[]) => canAny(snapshot, ops), refresh: async () => { suspendProtectedQueries(client); await query.refetch(); } };
}

// Mount once for the authenticated shell. Expiry and 403 synchronously remove
// the old generation; a failed authorization request never recursively retries.
export function useAuthorizationLifecycle() {
  const client = useQueryClient();
  const auth = useAuthorization();
  useEffect(() => {
    setWritePreparer(config => prepareManagedWrite(config, auth.snapshot, client));
    return () => setWritePreparer(undefined);
  }, [client, auth.snapshot]);
  useEffect(() => {
    const refresh = () => {
      suspendProtectedQueries(client);
      void client.resetQueries({ queryKey: authorizationKey });
    };
    window.addEventListener(AUTHORIZATION_REFRESH, refresh);
    window.addEventListener('storage', refresh);
    return () => { window.removeEventListener(AUTHORIZATION_REFRESH, refresh); window.removeEventListener('storage', refresh); };
  }, [client]);
  useEffect(() => {
    if (!auth.snapshot?.valid_until) return;
    const delay = Math.max(0, Date.parse(auth.snapshot.valid_until) - Date.now());
    const timer = window.setTimeout(() => {
      suspendProtectedQueries(client);
      void client.invalidateQueries({ queryKey: authorizationKey });
    }, Math.min(delay, 2_147_483_647));
    return () => window.clearTimeout(timer);
  }, [client, auth.snapshot?.valid_until]);
  return auth;
}

export function useAuthorizedQuery<T>(options: UseQueryOptions<T> & { permission: string | readonly string[] }) {
  const auth = useAuthorization();
  const client = useQueryClient();
  const configuredRetry = options.retry ?? client.getDefaultOptions().queries?.retry ?? 1;
  const { permission, ...rest } = options;
  const allowed = typeof permission === 'string' ? auth.can(permission) : auth.canAll(permission);
  const result = useQuery({ ...rest, queryKey: [...options.queryKey, 'authorization', auth.contextKey, auth.generation], enabled: allowed && options.enabled !== false, retry: (count, error) => { if ((error as { response?: { status?: number } }).response?.status === 403) return false; return typeof configuredRetry === 'function' ? configuredRetry(count, error) : configuredRetry === true || (typeof configuredRetry === 'number' && count < configuredRetry); }, meta: { ...options.meta, protected: true }, queryFn: typeof options.queryFn === 'function' ? (ctx) => {
    if (!allowed) throw new Error('没有执行此查询的权限');
    return (options.queryFn as (context: Parameters<Exclude<typeof options.queryFn, undefined | symbol>>[0]) => T | Promise<T>)(ctx);
  } : options.queryFn });
  return { ...result, isLoading: result.isLoading || auth.isPending || auth.isFetching, isRestricted: !allowed && !auth.isPending && !auth.isFetching, isPending: (allowed && result.isPending) || auth.isPending || auth.isFetching };
}
