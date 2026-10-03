import { can } from '@/lib/authorization';
import type { IAMReply } from '@/lib/iam-types';

// Console admission always uses the verified authorization response.
export function canAccessAdmin({ snapshot }: { snapshot?: IAMReply | null }) {
  return can(snapshot ?? undefined, 'admin.console.enter');
}
