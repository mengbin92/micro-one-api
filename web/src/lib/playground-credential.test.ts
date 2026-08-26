import { describe, expect, it } from 'vitest';
import {
  clearPlaygroundCredential,
  setPlaygroundCredential,
  takePlaygroundCredential,
} from './playground-credential';

describe('playground credential handoff', () => {
  it('is one-time and trims blank values', () => {
    clearPlaygroundCredential();
    setPlaygroundCredential('  sk-test  ');

    expect(takePlaygroundCredential()).toBe('sk-test');
    expect(takePlaygroundCredential()).toBeNull();

    setPlaygroundCredential('   ');
    expect(takePlaygroundCredential()).toBeNull();
  });
});
