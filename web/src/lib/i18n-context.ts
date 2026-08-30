import { createContext } from 'react';
import type { Language } from '@/lib/i18n';

export interface I18nContextValue {
  language: Language;
  setLanguage: (language: Language) => void;
  toggleLanguage: () => void;
}

export const I18nContext = createContext<I18nContextValue | null>(null);
