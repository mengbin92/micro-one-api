export type VerificationRequest = { token: string | null; resolve: () => void; reject: (error: Error) => void };
let listener: ((request: VerificationRequest) => void) | undefined;
let pending: Promise<void> | undefined;
let pendingRequest: VerificationRequest | undefined;

export function subscribeSecurityVerification(next: (request: VerificationRequest) => void) {
  listener = next;
  return () => { listener = undefined; };
}

export function requestSecurityVerification(): Promise<void> {
  const token = localStorage.getItem('token');
  if (pending && pendingRequest?.token === token) return pending;
  if (!listener) return Promise.reject(new Error('Recent security verification required'));
  pendingRequest?.reject(new Error('Session changed during verification'));
  let request!: VerificationRequest;
  const result = new Promise<void>((resolve, reject) => { request = { token, resolve, reject }; });
  pendingRequest = request;
  pending = result.finally(() => {
    if (pendingRequest === request) { pending = undefined; pendingRequest = undefined; }
  });
  listener(request);
  return pending;
}

export type SecurityCompletion = { codes: string[]; disabled: boolean };
let completionListener: ((notice: SecurityCompletion) => void) | undefined;
export function subscribeSecurityCompletion(next: (notice: SecurityCompletion) => void) {
  completionListener = next;
  return () => { completionListener = undefined; };
}
export function showSecurityCompletion(notice: SecurityCompletion) { completionListener?.(notice); }
