import type { ComponentProps, ReactNode } from 'react';
import { Button } from '@/components/ui/button';
import { useAuthorization } from '@/lib/authorization';
export function PermissionButton({ permission, any = false, object, ...props }: ComponentProps<typeof Button> & { permission?: string | readonly string[]; any?: boolean; object?: { permitted_actions?: string[]; permittedActions?: string[] } }) {
  const auth = useAuthorization();
  const operations = typeof permission === 'string' ? [permission] : permission;
  // Keep the verified layout during polling; only execution waits for the
  // fresh summary. Removing controls here also unmounts nested local drafts.
  if (operations && !(any ? operations.some(auth.displayCan) : auth.displayCanAll(operations))) return null;
  const objectActions = object?.permitted_actions ?? object?.permittedActions;
  if (auth.displaySnapshot?.authorization_mode === 'iam' && operations && object && (!objectActions || !(any ? operations.some(op => objectActions.includes(op)) : operations.every(op => objectActions.includes(op))))) return null;
  const awaitingAuthorization = !!operations && !(any ? auth.canAny(operations) : auth.canAll(operations));
  return <Button {...props} disabled={props.disabled || awaitingAuthorization} />;
}
export function Permission({ operation, children }: { operation: string | readonly string[]; children: ReactNode }) {
  const auth = useAuthorization();
  return (typeof operation === 'string' ? auth.displayCan(operation) : auth.displayCanAll(operation)) ? children : null;
}
