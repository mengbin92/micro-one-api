import { useEffect, useRef, useState } from 'react';
import { apiClient } from '@/lib/api';
import { subscribeSecurityCompletion, subscribeSecurityVerification, type SecurityCompletion, type VerificationRequest } from '@/lib/security-verification';
import { getApiErrorMessage } from '@/lib/api-error';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Button } from '@/components/ui/button';
import { t } from '@/lib/i18n';

export function SecurityVerificationDialog() {
  const [request, setRequest] = useState<VerificationRequest>();
  const [completion, setCompletion] = useState<SecurityCompletion>();
  const activeRequest = useRef<VerificationRequest | undefined>(undefined);
  const [enabled, setEnabled] = useState<boolean>();
  const [passwordAvailable, setPasswordAvailable] = useState(true);
  const [value, setValue] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const lock = useRef(false);

  useEffect(() => subscribeSecurityVerification(next => {
    activeRequest.current = next;
    setValue(''); setError(''); setEnabled(undefined); setRequest(next);
  }), []);
  useEffect(() => subscribeSecurityCompletion(setCompletion), []);
  useEffect(() => () => activeRequest.current?.reject(new Error('Security dialog closed')), []);
  useEffect(() => {
    if (!request) return;
    const controller = new AbortController();
    apiClient.get('/user/security', { signal: controller.signal, headers: { Authorization: `Bearer ${request.token}` } }).then(response => { if (controller.signal.aborted) return; setEnabled(response.data.enabled === true); setPasswordAvailable(response.data.password_available !== false); })
      .catch(error => { if (!controller.signal.aborted) setError(getApiErrorMessage(error, t('安全验证暂不可用'))); });
    return () => controller.abort();
  }, [request]);

  const finish = (current: VerificationRequest, error?: Error) => {
    if (error) current.reject(error); else current.resolve();
    if (activeRequest.current !== current) return;
    activeRequest.current = undefined; setRequest(undefined); setValue('');
  };
  const cancel = () => { request?.reject(new Error(t('已取消安全验证'))); activeRequest.current = undefined; setRequest(undefined); };
  return <><Dialog open={!!request} onOpenChange={open => { if (!open && !busy) cancel(); }}>
    <DialogContent showCloseButton={!busy}>
      <DialogHeader><DialogTitle>{t('安全验证')}</DialogTitle><DialogDescription>{t('验证仅对当前会话生效，有效期五分钟。')}</DialogDescription></DialogHeader>
      <form className="space-y-4" onSubmit={async event => {
        event.preventDefault(); if (lock.current || enabled === undefined || !request) return;
        if (request.token !== localStorage.getItem('token')) { finish(request, new Error(t('会话已变化，请重试'))); return; }
        lock.current = true; setBusy(true); setError('');
        try {
          await apiClient.post('/user/security/verify', enabled ? { code: value } : { password: value }, { headers: { Authorization: `Bearer ${request.token}` } });
          finish(request, request.token === localStorage.getItem('token') ? undefined : new Error(t('会话已变化，请重试')));
        } catch (error) { if (activeRequest.current === request) setError(getApiErrorMessage(error, t('验证失败，请重试'))); }
        finally { lock.current = false; setBusy(false); }
      }}>
        {enabled === false && !passwordAvailable && <p role="alert" className="text-sm text-muted-foreground">{t('请重新使用第三方账号登录，并在五分钟内完成此操作。')}</p>}
        <Label htmlFor="step-up-code">{enabled ? t('验证码或恢复码') : t('当前密码')}</Label>
        <Input id="step-up-code" type={enabled ? 'text' : 'password'} autoComplete={enabled ? 'one-time-code' : 'current-password'} value={value} onChange={event => setValue(event.target.value)} required disabled={busy || enabled === undefined || (enabled === false && !passwordAvailable)} maxLength={enabled ? 32 : 72} autoFocus />
        {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
        <div className="flex justify-end gap-2"><Button type="button" variant="outline" disabled={busy} onClick={cancel}>{t('取消')}</Button><Button type="submit" disabled={busy || enabled === undefined || (enabled === false && !passwordAvailable)}>{busy ? t('验证中…') : t('验证并继续')}</Button></div>
      </form>
    </DialogContent>
  </Dialog>
    <Dialog open={!!completion}>
      <DialogContent showCloseButton={false} className="sm:max-w-lg"><DialogHeader><DialogTitle>{completion?.disabled ? t('双重认证已禁用') : t('保存恢复码')}</DialogTitle><DialogDescription>{completion?.disabled ? t('所有旧会话已失效，请重新登录。') : t('恢复码仅展示一次，每个只能使用一次。请离线保存，完成后重新登录。')}</DialogDescription></DialogHeader>
        {!!completion?.codes.length && <pre className="overflow-x-auto rounded-xl bg-muted p-4 text-sm leading-7 select-all">{completion.codes.join('\n')}</pre>}
        <Button onClick={() => { setCompletion(undefined); window.location.assign('/login'); }}>{completion?.disabled ? t('重新登录') : t('已保存，重新登录')}</Button>
      </DialogContent>
    </Dialog></>;
}
