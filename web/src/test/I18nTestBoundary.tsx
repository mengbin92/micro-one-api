import type { ReactElement } from 'react';
import { useI18n } from '@/hooks/useI18n';

export function I18nTestBoundary({ ui }: { ui: ReactElement }) {
  const { language } = useI18n();
  return <div key={language}>{ui}</div>;
}
