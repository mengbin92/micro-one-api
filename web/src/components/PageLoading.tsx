import { Loader2 } from 'lucide-react';
import { t } from '@/lib/i18n';

export function PageLoading() {
  return (
    <div className="flex min-h-80 items-center justify-center text-muted-foreground">
      <Loader2 className="mr-2 size-4 animate-spin" />{t("加载中…")}</div>
  );
}
