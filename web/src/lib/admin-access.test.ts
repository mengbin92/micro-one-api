import { describe, expect, it } from 'vitest';
import { canAccessAdmin } from './admin-access';

describe('console admission', () => {
  it('requires the verified legacy admin flag', () => {
    expect(canAccessAdmin({})).toBe(false);
    expect(canAccessAdmin({ snapshot: { authorization_mode: 'legacy', legacy_admin: false } })).toBe(false);
    expect(canAccessAdmin({ snapshot: { authorization_mode: 'legacy', legacy_admin: true } })).toBe(true);
  });
  it('uses active IAM operations despite a forged local numeric role', () => {
    localStorage.setItem('userRole', '100');
    expect(canAccessAdmin({ snapshot: { authorization_mode: 'iam', session: { activation_state: 'active' }, permitted_operations: [] } })).toBe(false);
    expect(canAccessAdmin({ snapshot: { authorization_mode: 'iam', session: { activation_state: 'active' }, permitted_operations: ['admin.console.enter'] } })).toBe(true);
    expect(canAccessAdmin({ snapshot: { authorization_mode: 'iam', session: { activation_state: 'selection_required' }, permitted_operations: ['admin.console.enter'] } })).toBe(false);
  });
});
