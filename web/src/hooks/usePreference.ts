import { useState } from 'react';
import { getPreference, setPreference, type PreferenceName } from '@/lib/preferences';

export function usePreference<T>(name: PreferenceName, fallback: T) {
  const [value, setValue] = useState(() => getPreference(name, fallback));

  const updateValue = (nextValue: T | ((current: T) => T)) => {
    setValue((current) => {
      const resolved = typeof nextValue === 'function' ? (nextValue as (current: T) => T)(current) : nextValue;
      setPreference(name, resolved);
      return resolved;
    });
  };

  return [value, updateValue] as const;
}
