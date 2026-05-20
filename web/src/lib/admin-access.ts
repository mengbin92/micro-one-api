export interface AdminAccessSnapshot {
  admin?: boolean;
}

export function canAccessAdmin({
  adminToken,
  snapshot,
}: {
  adminToken?: string | null;
  snapshot?: AdminAccessSnapshot | null;
}) {
  return Boolean(adminToken || snapshot?.admin);
}
