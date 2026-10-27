export type VerificationRequest = { resolve: () => void; reject: (error: Error) => void };
let listener: ((request: VerificationRequest) => void) | undefined;
let pending: Promise<void> | undefined;

export function subscribeSecurityVerification(next: (request: VerificationRequest) => void) {
  listener = next;
  return () => { listener = undefined; };
}

export function requestSecurityVerification(): Promise<void> {
  if (pending) return pending;
  if (!listener) return Promise.reject(new Error('Recent security verification required'));
  pending = new Promise<void>((resolve, reject) => listener!({ resolve, reject }))
    .finally(() => { pending = undefined; });
  return pending;
}

export type SecurityCompletion = { codes: string[]; disabled: boolean };
let completionListener: ((notice: SecurityCompletion) => void) | undefined;
export function subscribeSecurityCompletion(next: (notice: SecurityCompletion) => void) {
  completionListener = next;
  return () => { completionListener = undefined; };
}
export function showSecurityCompletion(notice: SecurityCompletion) { completionListener?.(notice); }
