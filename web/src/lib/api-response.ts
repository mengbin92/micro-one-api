export interface ApiEnvelope<T = unknown> {
  success?: boolean;
  message?: string;
  data?: T;
}

export function ensureApiSuccess(response: ApiEnvelope, fallback = 'Request failed') {
  if (response.success === false) {
    throw new Error(response.message || fallback);
  }
}

export function unwrapApiData<T>(response: ApiEnvelope<T>, fallback = 'Request failed'): T {
  ensureApiSuccess(response, fallback);
  return response.data as T;
}
