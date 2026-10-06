import type { ComponentProps, ReactNode } from 'react';
import { Button } from '@/components/ui/button';
import { useAuthorization } from '@/lib/authorization';
export function PermissionButton({ permission, any = false, object, ...props }: ComponentProps<typeof Button> & { permission?: string | readonly string[]; any?: boolean; object?: { permitted_actions?: string[]; permittedActions?: string[] } }) {
  const auth = useAuthorization();
  const operations = typeof permission === 'string' ? [permission] : permission;
  if (operations && !(any ? auth.canAny(operations) : auth.canAll(operations))) return null;
  const objectActions = object?.permitted_actions ?? object?.permittedActions;
  if (auth.snapshot?.authorization_mode === 'iam' && operations && object && (!objectActions || !(any ? operations.some(op => objectActions.includes(op)) : operations.every(op => objectActions.includes(op))))) return null;
  return <Button {...props} />;
}
export function Permission({ operation, children }: { operation: string | readonly string[]; children: ReactNode }) {
  const auth = useAuthorization();
  return (typeof operation === 'string' ? auth.can(operation) : auth.canAll(operation)) ? children : null;
}
