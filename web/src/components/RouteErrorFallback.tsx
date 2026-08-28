import { isRouteErrorResponse, useNavigate, useRouteError } from 'react-router';
import { Button } from '@/components/ui/button';
import { t } from '@/lib/i18n';

export function RouteErrorFallback() {
  const error = useRouteError();
  const navigate = useNavigate();
  const message = isRouteErrorResponse(error)
    ? `${error.status} ${error.statusText}`
    : t("页面暂时无法显示");

  return (
    <main className="flex min-h-screen items-center justify-center bg-slate-50 p-6 dark:bg-slate-950">
      <div className="max-w-md space-y-4 text-center">
        <h1 className="text-2xl font-semibold text-slate-900 dark:text-slate-100">{t("页面出错了")}</h1>
        <p className="text-sm text-slate-500 dark:text-slate-400">{message}{t("，请重试或返回首页。")}</p>
        <div className="flex justify-center gap-2">
          <Button type="button" variant="outline" onClick={() => window.location.reload()}>{t("重试")}</Button>
          <Button type="button" onClick={() => void navigate('/dashboard')}>{t("返回首页")}</Button>
        </div>
      </div>
    </main>
  );
}
