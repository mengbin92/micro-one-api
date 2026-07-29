export interface ApiEnvelope<T = unknown> {
  success?: boolean;
  message?: string;
  data?: T;
}

export function ensureApiSuccess(response: ApiEnvelope, fallback = 'Request failed') {
  // Protobuf bool fields serialise with json:"omitempty", so encoding/json
  // omits success:false entirely. The backend now wraps failed operation
  // responses in an explicit {success:false} envelope, but stay defensive:
  // if the field is present at all and is not true, treat it as a failure.
  if (response.success !== undefined && response.success !== true) {
    throw new Error(response.message || fallback);
  }
}

export function unwrapApiData<T>(response: ApiEnvelope<T>, fallback = 'Request failed'): T {
  ensureApiSuccess(response, fallback);
  return response.data as T;
}
