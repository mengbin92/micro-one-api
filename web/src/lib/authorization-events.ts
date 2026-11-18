// Transport has no dependency on React or the QueryClient singleton.
export const AUTHORIZATION_REFRESH = 'micro-one:authorization-refresh';
// Revalidate after a policy write without discarding the verified snapshot first.
export const AUTHORIZATION_INVALIDATE = 'micro-one:authorization-invalidate';
let protectedController = new AbortController();
export function protectedSignal() { return protectedController.signal; }
export function stopProtectedRequests() {
  protectedController.abort();
  protectedController = new AbortController();
}
export function refreshAuthorization() {
  stopProtectedRequests();
  window.dispatchEvent(new Event(AUTHORIZATION_REFRESH));
}
export function invalidateAuthorization() {
  window.dispatchEvent(new Event(AUTHORIZATION_INVALIDATE));
}
import type { InternalAxiosRequestConfig } from 'axios';
type AdminWriteHandlers = {
  prepare: (config: InternalAxiosRequestConfig) => InternalAxiosRequestConfig;
  refreshConflict?: (config: InternalAxiosRequestConfig) => Promise<void>;
};
let writeHandlers: AdminWriteHandlers | undefined;
export function setAdminWriteHandlers(handlers?: AdminWriteHandlers) { writeHandlers = handlers; }
export function prepareAdminRequest(config: InternalAxiosRequestConfig) { return writeHandlers?.prepare(config) ?? config; }
export async function refreshAdminConflict(config: InternalAxiosRequestConfig) { await writeHandlers?.refreshConflict?.(config); }
