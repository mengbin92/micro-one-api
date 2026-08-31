import { getPreference } from '@/lib/preferences';
import { EN_US_MESSAGES } from '@/locales/en-US';

export type Language = 'zh-CN' | 'en-US';
export type TranslationValues = Record<string, string | number>;

const DEFAULT_LANGUAGE: Language = 'zh-CN';
const EN_US_SEGMENTS = Object.entries(EN_US_MESSAGES)
  .filter(([source]) => /[\u3400-\u9fff]/.test(source))
  .sort(([left], [right]) => right.length - left.length);

function isLanguage(value: unknown): value is Language {
  return value === 'zh-CN' || value === 'en-US';
}

export function currentLanguage(): Language {
  if (typeof window === 'undefined') return DEFAULT_LANGUAGE;
  const stored = getPreference<unknown>('language', DEFAULT_LANGUAGE);
  return isLanguage(stored) ? stored : DEFAULT_LANGUAGE;
}

function interpolate(message: string, values?: TranslationValues): string {
  if (!values) return message;
  return message.replace(/\{([A-Za-z0-9_]+)\}/g, (placeholder, key: string) => (
    Object.prototype.hasOwnProperty.call(values, key) ? String(values[key]) : placeholder
  ));
}

export function t(message: string, values?: TranslationValues): string {
  if (!message) return message;
  if (currentLanguage() === 'zh-CN') return interpolate(message, values);

  const normalized = message.replace(/\s+/g, ' ').trim();
  const exact = EN_US_MESSAGES[normalized];
  if (exact) return interpolate(exact.replace(/^[a-z]/, (letter) => letter.toUpperCase()), values);

  let translated = normalized;
  for (const [source, target] of EN_US_SEGMENTS) {
    if (translated.includes(source)) translated = translated.replaceAll(source, target);
  }
  return interpolate(translated, values);
}

export function locale(): Language {
  return currentLanguage();
}
