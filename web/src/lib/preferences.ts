export type PreferenceName =
  | 'theme'
  | 'admin-page-size'
  | `admin-visible-columns:${string}`
  | 'timezone';

export function preferenceKey(name: PreferenceName) {
  return `web:${name}`;
}

export function getPreference<T>(name: PreferenceName, fallback: T): T {
  const stored = window.localStorage.getItem(preferenceKey(name));
  if (!stored) {
    return fallback;
  }

  try {
    return JSON.parse(stored) as T;
  } catch {
    return fallback;
  }
}

export function setPreference<T>(name: PreferenceName, value: T) {
  window.localStorage.setItem(preferenceKey(name), JSON.stringify(value));
}
