export interface ApiEnvelope<T = unknown> {
  success?: boolean;
  message?: string;
  data?: T;
}

export function ensureApiSuccess(response: unknown, fallback = 'Request failed') {
  if (response === null || typeof response !== 'object' || Array.isArray(response)) {
    throw new Error('Unexpected API response format');
  }
  const envelope = response as ApiEnvelope;
  // Protobuf bool fields serialise with json:"omitempty", so encoding/json
  // omits success:false entirely. The backend now wraps failed operation
  // responses in an explicit {success:false} envelope, but stay defensive:
  // if the field is present at all and is not true, treat it as a failure.
  if (envelope.success !== undefined && envelope.success !== true) {
    throw new Error(envelope.message || fallback);
  }
}

export function unwrapApiData<T>(response: unknown, fallback = 'Request failed'): T {
  ensureApiSuccess(response, fallback);
  // Payload reads require the envelope's own data field. A raw response or
  // acknowledgement must not silently become undefined in a query/mutation.
  // Explicit null, false, zero and empty payloads are valid.
  if (!Object.hasOwn(response as object, 'data') || (response as ApiEnvelope<T>).data === undefined) {
    throw new Error('Unexpected API response format');
  }
  return (response as ApiEnvelope<T>).data as T;
}
