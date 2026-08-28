import { Languages } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { t } from '@/lib/i18n';
import { useI18n } from '@/hooks/useI18n';

export function LanguageToggle({ className }: { className?: string }) {
  const { language, toggleLanguage } = useI18n();
  const label = language === 'zh-CN' ? 'English' : '中文';
  const accessibleLabel = language === 'zh-CN' ? t('切换至英文') : t('切换至中文');

  return (
    <Button
      type="button"
      variant="outline"
      size="sm"
      className={className}
      aria-label={accessibleLabel}
      title={accessibleLabel}
      onClick={toggleLanguage}
    >
      <Languages className="size-4" />
      {label}
    </Button>
  );
}
