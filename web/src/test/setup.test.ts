import { describe, expect, it, vi } from 'vitest';

const originalDate = Date;
const originalNow = Date.now;
const originalRandom = Math.random;

// These cases deliberately share globals to exercise setup.ts between tests.
describe('shared test cleanup', { concurrent: false, shuffle: false }, () => {
  it('leaves clock and random mocks for shared cleanup', () => {
    vi.spyOn(Date, 'now').mockReturnValue(0);
    vi.spyOn(Math, 'random').mockReturnValue(0.25);
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2000-01-01T00:00:00Z'));

    expect(vi.isFakeTimers()).toBe(true);
    expect(Date.now()).toBe(946684800000);
    expect(Math.random()).toBe(0.25);
  });

  it('starts the next test with the original clock and random globals', () => {
    expect(vi.isFakeTimers()).toBe(false);
    expect(Date).toBe(originalDate);
    expect(Date.now).toBe(originalNow);
    expect(Math.random).toBe(originalRandom);
  });
});
