import { useEffect, useMemo, useState, type ReactNode } from 'react';
import { I18nContext, type I18nContextValue } from '@/lib/i18n-context';
import { currentLanguage, type Language } from '@/lib/i18n';
import { setPreference } from '@/lib/preferences';

export function I18nProvider({ children }: { children: ReactNode }) {
  const [language, updateLanguage] = useState<Language>(currentLanguage);

  useEffect(() => {
    document.documentElement.lang = language;
  }, [language]);

  const value = useMemo<I18nContextValue>(() => ({
    language,
    setLanguage(nextLanguage) {
      setPreference('language', nextLanguage);
      updateLanguage(nextLanguage);
    },
    toggleLanguage() {
      const nextLanguage = language === 'zh-CN' ? 'en-US' : 'zh-CN';
      setPreference('language', nextLanguage);
      updateLanguage(nextLanguage);
    },
  }), [language]);

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}
