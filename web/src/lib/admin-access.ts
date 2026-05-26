export const ROLE_GUEST = 0;
export const ROLE_COMMON = 1;
export const ROLE_ADMIN = 10;
export const ROLE_ROOT = 100;

export interface AdminAccessSnapshot {
  admin?: boolean;
  role?: number;
}

export function isAdminRole(role?: number | null) {
  return typeof role === 'number' && role >= ROLE_ADMIN;
}

export function canAccessAdmin({
  role,
  snapshot,
}: {
  role?: number | null;
  snapshot?: AdminAccessSnapshot | null;
}) {
  if (isAdminRole(role)) {
    return true;
  }
  if (isAdminRole(snapshot?.role)) {
    return true;
  }
  return Boolean(snapshot?.admin);
}
