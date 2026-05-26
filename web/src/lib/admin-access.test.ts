import { describe, expect, it } from 'vitest';
import { canAccessAdmin, isAdminRole, ROLE_ADMIN, ROLE_ROOT } from './admin-access';

describe('isAdminRole', () => {
  it('treats admin and above as admin', () => {
    expect(isAdminRole(ROLE_ADMIN)).toBe(true);
    expect(isAdminRole(ROLE_ROOT)).toBe(true);
  });

  it('rejects common users and missing role', () => {
    expect(isAdminRole(1)).toBe(false);
    expect(isAdminRole(0)).toBe(false);
    expect(isAdminRole(null)).toBe(false);
    expect(isAdminRole(undefined)).toBe(false);
  });
});

describe('canAccessAdmin', () => {
  it('allows when the current role is admin', () => {
    expect(canAccessAdmin({ role: ROLE_ADMIN })).toBe(true);
  });

  it('rejects when the current role is below admin', () => {
    expect(canAccessAdmin({ role: 1 })).toBe(false);
  });

  it('allows when a backend snapshot reports an admin role', () => {
    expect(canAccessAdmin({ snapshot: { role: ROLE_ROOT } })).toBe(true);
  });

  it('allows backend capability snapshot fallback', () => {
    expect(canAccessAdmin({ snapshot: { admin: true } })).toBe(true);
  });
});
