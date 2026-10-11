import { describe, expect, it } from 'vitest';
import { deriveAccountStatus } from './AccountStatusBadge';

describe('server-rolled account quota status', () => {
  it('keeps a fixed 25-hour day exhausted after 24 hours', () => {
    expect(deriveAccountStatus({ status: 1, quotaDailyUsedUsd: 10, quotaDailyLimitUsd: 10,
      quotaDailyWindowStart: Date.parse('2026-11-01T04:00:00Z') / 1000 },
    Date.parse('2026-11-02T04:30:00Z') / 1000)).toBe('quota_exceeded');
  });

  it('uses the server-rolled zero when a window has reset', () => {
    expect(deriveAccountStatus({ status: 1, quotaDailyUsedUsd: 0, quotaDailyLimitUsd: 10,
      quotaDailyWindowStart: 1 }, 86402)).toBe('active');
  });
});
