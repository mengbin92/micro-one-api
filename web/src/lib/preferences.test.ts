import { describe, expect, it } from 'vitest';
import { getPreference, preferenceKey, setPreference } from './preferences';

describe('preferences', () => {
  it('returns default value when localStorage is empty', () => {
    expect(getPreference('theme', 'light')).toBe('light');
  });

  it('ignores invalid JSON', () => {
    window.localStorage.setItem(preferenceKey('theme'), '{bad');

    expect(getPreference('theme', 'dark')).toBe('dark');
  });

  it('persists valid values', () => {
    setPreference('theme', 'dark');

    expect(getPreference('theme', 'light')).toBe('dark');
  });

  it('supports namespaced keys', () => {
    expect(preferenceKey('admin-visible-columns:users')).toBe('web:admin-visible-columns:users');
  });
});
