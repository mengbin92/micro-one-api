// Transport has no dependency on React or the QueryClient singleton.
export const AUTHORIZATION_REFRESH = 'micro-one:authorization-refresh';
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
import type { InternalAxiosRequestConfig } from 'axios';
let writePreparer: ((config: InternalAxiosRequestConfig) => InternalAxiosRequestConfig) | undefined;
export function setWritePreparer(prepare?: typeof writePreparer) { writePreparer = prepare; }
export function prepareAdminRequest(config: InternalAxiosRequestConfig) { return writePreparer?.(config) ?? config; }
