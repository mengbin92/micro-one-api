import { describe, expect, it } from 'vitest';
import { canAccessAdmin } from './admin-access';

describe('canAccessAdmin', () => {
  it('allows admin token compatibility', () => {
    expect(canAccessAdmin({ adminToken: 'token' })).toBe(true);
  });

  it('allows backend capability snapshot', () => {
    expect(canAccessAdmin({ snapshot: { admin: true } })).toBe(true);
  });
});
