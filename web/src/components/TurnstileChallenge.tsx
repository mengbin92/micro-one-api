import { useEffect, useRef, useState } from 'react';
import { t } from '@/lib/i18n';

interface TurnstileOptions {
  sitekey: string;
  callback: (token: string) => void;
  'expired-callback': () => void;
  'error-callback': () => boolean;
}

interface TurnstileAPI {
  ready: (callback: () => void) => void;
  render: (container: HTMLElement, options: TurnstileOptions) => string;
  remove: (id: string) => void;
}

declare global {
  interface Window { turnstile?: TurnstileAPI }
}

const scriptURL = 'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit';

export function TurnstileChallenge({ siteKey, onToken }: { siteKey: string; onToken: (token: string) => void }) {
  const container = useRef<HTMLDivElement>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let disposed = false;
    let widgetID: string | undefined;
    const fail = () => {
      if (disposed) return;
      onToken('');
      setFailed(true);
    };
    const render = () => window.turnstile?.ready(() => {
      if (disposed || !container.current || !window.turnstile) return;
      try {
        widgetID = window.turnstile.render(container.current, {
          sitekey: siteKey,
          callback: token => { if (!disposed) { setFailed(false); onToken(token); } },
          'expired-callback': () => { if (!disposed) onToken(''); },
          'error-callback': () => { fail(); return true; },
        });
      } catch { fail(); }
    });
    const existing = document.querySelector<HTMLScriptElement>(`script[src="${scriptURL}"]`);
    const script = existing ?? document.createElement('script');
    if (window.turnstile) render();
    else {
      script.addEventListener('load', render);
      script.addEventListener('error', fail);
      if (!existing) {
        script.src = scriptURL;
        script.async = true;
        document.head.appendChild(script);
      }
    }
    return () => {
      disposed = true;
      script.removeEventListener('load', render);
      script.removeEventListener('error', fail);
      if (widgetID) window.turnstile?.remove(widgetID);
    };
  }, [siteKey, onToken]);

  return <div className="space-y-2">
    <div ref={container} aria-label={t('安全验证')} />
    {failed && <p role="alert" className="text-sm text-destructive">{t('安全验证加载失败，请刷新页面重试。')}</p>}
  </div>;
}
