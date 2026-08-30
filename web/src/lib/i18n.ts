import { getPreference } from '@/lib/preferences';
import { EN_US_MESSAGES } from '@/locales/en-US';

export type Language = 'zh-CN' | 'en-US';

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

export function t(message: string): string {
  if (currentLanguage() === 'zh-CN' || !message) return message;

  const normalized = message.replace(/\s+/g, ' ').trim();
  const exact = EN_US_MESSAGES[normalized];
  if (exact) return exact.replace(/^[a-z]/, (letter) => letter.toUpperCase());

  let translated = normalized;
  for (const [source, target] of EN_US_SEGMENTS) {
    if (translated.includes(source)) translated = translated.replaceAll(source, target);
  }
  return translated;
}

export function locale(): Language {
  return currentLanguage();
}
