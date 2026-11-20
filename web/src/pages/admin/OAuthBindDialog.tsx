import { useMutation } from '@tanstack/react-query';
import { isAxiosError } from 'axios';
import { Copy, ExternalLink, ShieldCheck } from 'lucide-react';
import { useRef, useState } from 'react';
import { toast } from 'sonner';
import { adminApiClient } from '@/lib/api';
import { PermissionButton as Button } from '@/components/admin/PermissionButton';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog';
import { parseOAuthCallbackInput } from './oauthCallbackInput';
import { t } from '@/lib/i18n';

const PLATFORM_OPTIONS: Array<{ value: string; label: string }> = [
  { value: 'claude', label: 'Claude (Claude Code OAuth)' },
  { value: 'codex', label: 'Codex (ChatGPT OAuth)' },
  { value: 'kimi', label: 'Kimi (Kimi Code OAuth)' },
];
const BIND_PERMISSIONS = ['channel.account.oauth.bind', 'channel.account.create'] as const;

interface AuthURLResult {
  auth_url: string;
  session_id: string;
  state: string;
  expires_at: number;
  user_code?: string;
}

interface ExchangeResult {
  pending?: boolean;
  success?: boolean;
  error?: string;
  message?: string;
}

interface OAuthBindDialogProps {
  onBound: () => void;
}

export function OAuthBindDialog({ onBound }: OAuthBindDialogProps) {
  const [open, setOpen] = useState(false);
  const [platform, setPlatform] = useState('claude');
  const [session, setSession] = useState<AuthURLResult | null>(null);
  const [codeInput, setCodeInput] = useState('');
  const [name, setName] = useState('');
  const [group, setGroup] = useState('default');
  const [models, setModels] = useState('');
  const [priority, setPriority] = useState('0');
  const [baseUrl, setBaseUrl] = useState('');
  const isDeviceFlow = platform === 'kimi';
  const flowRevision = useRef(0);

  const reset = () => {
    flowRevision.current++;
    setSession(null);
    setCodeInput('');
    setName('');
    setGroup('default');
    setModels('');
    setPriority('0');
    setBaseUrl('');
  };

  const authUrlMutation = useMutation({
    mutationFn: async (request: { platform: string; group: string; revision: number }) => {
      const res = await adminApiClient.post<AuthURLResult>(
        `/v1/admin/accounts/subscription/oauth/${request.platform}/auth-url`,
        { group: request.group }
      );
      return res.data;
    },
    onSuccess: (data, request) => {
      if (request.revision !== flowRevision.current) return;
      if (!data?.auth_url) {
        toast.error(t("生成授权链接失败：返回为空"));
        return;
      }
      setSession(data);
      setCodeInput('');
    },
    onError: (error, request) => {
      if (request.revision !== flowRevision.current) return;
      if (isAxiosError(error) && error.response?.data?.reason === 'KIMI_DEVICE_OAUTH_UNAVAILABLE') {
        toast.error(t("Kimi 设备码授权需要上游批准的客户端注册。"));
        return;
      }
      toast.error(t("生成授权链接失败"));
    },
  });

  const requestAuthorization = () => authUrlMutation.mutate({ platform, group: group.trim(), revision: flowRevision.current });

  const exchangeMutation = useMutation({
    mutationFn: async (revision: number) => {
      if (!session || revision !== flowRevision.current) throw new Error(t("请先生成授权链接"));
      const parsed = parseOAuthCallbackInput(isDeviceFlow ? '' : codeInput);
      if (!isDeviceFlow && !parsed.code) throw new Error(t("请填写授权码或回调 URL"));
      if (parsed.state && parsed.state !== session.state) {
        throw new Error(t("回调 URL 的 state 与当前授权会话不一致，请重新生成授权链接"));
      }
      const res = await adminApiClient.post<ExchangeResult>(
        `/v1/admin/accounts/subscription/oauth/${platform}/exchange`,
        {
          session_id: session.session_id,
          state: session.state,
          code: parsed.code,
          name: name.trim(),
          group: group.trim(),
          models: models.trim(),
          priority: parseInt(priority || '0', 10),
          base_url: isDeviceFlow ? '' : baseUrl.trim(),
        }
      );
      if (res.data?.error) throw new Error(res.data.error);
      if (res.data?.success !== true && !(isDeviceFlow && res.data?.pending === true)) {
        throw new Error(res.data?.message || t("授权码兑换失败"));
      }
      return res.data;
    },
    onSuccess: (data, revision) => {
      if (data.pending === true) {
        if (revision === flowRevision.current) toast.info(t("设备授权尚未完成，请在授权页确认后重试。"));
        return;
      }
      onBound();
      if (revision !== flowRevision.current) return;
      toast.success(t("订阅账号已通过 OAuth 绑定"));
      reset();
      setOpen(false);
    },
    onError: (error: Error, revision) => {
      if (revision !== flowRevision.current) return;
      if (isAxiosError(error) && error.response?.data?.reason === 'KIMI_DEVICE_OAUTH_RETRYABLE') {
        toast.error(t("设备授权服务暂不可用，请稍后重试。"));
        return;
      }
      toast.error(error.message || t("授权码兑换失败"));
    },
  });

  const copyAuthUrl = async () => {
    if (!session?.auth_url) return;
    try {
      await navigator.clipboard.writeText(session.auth_url);
      toast.success(t("授权链接已复制"));
    } catch {
      toast.error(t("复制失败，请手动复制授权链接。"));
    }
  };

  const isPending = authUrlMutation.isPending || exchangeMutation.isPending;

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) reset();
      }}
    >
      <DialogTrigger render={<Button variant="outline" permission={BIND_PERMISSIONS} />}>
        <ShieldCheck className="size-4" />{t("OAuth 授权绑定")}</DialogTrigger>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t("OAuth 授权绑定")}</DialogTitle>
          <DialogDescription>{t("通过 OAuth 绑定 Claude、Codex 或 Kimi 订阅账号。授权会话最多有效 5 分钟。")}</DialogDescription>
        </DialogHeader>

        <div className="grid gap-4 pt-2">
          <div className="space-y-2">
            <Label htmlFor="oauth-platform">{t("平台")}</Label>
            <select
              id="oauth-platform"
              value={platform}
              disabled={!!session || isPending}
              onChange={(e) => setPlatform(e.target.value)}
              className="h-8 w-full rounded-lg border border-input bg-background px-2.5 text-sm disabled:opacity-60"
            >
              {PLATFORM_OPTIONS.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-2">
            <Label htmlFor="oauth-group">{t("路由分组")}</Label>
            <Input id="oauth-group" value={group} disabled={!!session || isPending} onChange={(e) => setGroup(e.target.value)} />
          </div>

          {!session ? (
            <Button permission={BIND_PERMISSIONS} onClick={requestAuthorization} disabled={isPending}>
              {authUrlMutation.isPending ? t("生成中...") : t("生成授权链接")}
            </Button>
          ) : (
            <>
              <div className="space-y-2">
                <Label htmlFor="oauth-auth-url">{t("授权链接")}</Label>
                <div className="flex items-center gap-2">
                  <Input id="oauth-auth-url" readOnly value={session.auth_url} className="font-mono text-xs" />
                  <Button type="button" variant="outline" size="icon-sm" onClick={copyAuthUrl} aria-label={t("复制授权链接")}>
                    <Copy className="size-4" />
                  </Button>
                </div>
                <a href={session.auth_url} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1 text-sm underline">
                  {t("打开授权链接")}<ExternalLink className="size-3" aria-hidden="true" />
                </a>
                {isDeviceFlow ? (
                  <div className="space-y-1">
                    <Label htmlFor="oauth-user-code">{t("设备码")}</Label>
                    <Input id="oauth-user-code" readOnly value={session.user_code ?? ''} className="font-mono" />
                    <p className="text-xs text-muted-foreground">{t("在授权页确认设备码并授权，然后返回这里完成绑定。")}</p>
                  </div>
                ) : (
                  <p className="text-xs text-muted-foreground">{t("在浏览器完成授权后，把回调地址(含")}<code>?code=...</code>{t(")或授权码粘贴到下方。")}</p>
                )}
                {platform === 'codex' ? (
                  <p className="rounded-md bg-amber-50 px-2.5 py-2 text-xs text-amber-700 dark:bg-amber-950/40 dark:text-amber-400">{t("⚠️ Codex 授权完成后浏览器会跳转到")}<code>http://localhost:1455/auth/callback?...</code>{t("， 该页面")}<strong>{t("无法打开(显示“无法访问/连接被拒绝”)属于正常现象")}</strong>{t("—— 这个地址是 Codex CLI 的本地回调，本系统并不在该端口监听。请直接")}<strong>{t("从浏览器地址栏复制整段 URL")}</strong>{t("(包含")}<code>code=</code>{t(")粘贴到下方即可。")}</p>
                ) : null}
              </div>

              {!isDeviceFlow && <div className="space-y-2">
                <Label htmlFor="oauth-code">{t("授权码 / 回调 URL")}</Label>
                <Input
                  id="oauth-code"
                  value={codeInput}
                  onChange={(e) => setCodeInput(e.target.value)}
                  placeholder={t("粘贴 code 或 http://.../callback?code=...&state=...")}
                />
              </div>}

              <div className="grid gap-4 sm:grid-cols-2">
                <div className="space-y-2">
                  <Label htmlFor="oauth-name">{t("账号名称")}</Label>
                  <Input id="oauth-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="claude-pro-1" />
                </div>
                <div className="space-y-2 sm:col-span-2">
                  <Label htmlFor="oauth-models">{t("模型（逗号分隔，可选）")}</Label>
                  <Input
                    id="oauth-models"
                    value={models}
                    onChange={(e) => setModels(e.target.value)}
                    placeholder="claude-sonnet-4-5,claude-opus-4-1"
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="oauth-priority">{t("优先级")}</Label>
                  <Input
                    id="oauth-priority"
                    type="number"
                    value={priority}
                    onChange={(e) => setPriority(e.target.value)}
                  />
                </div>
                {!isDeviceFlow && <div className="space-y-2">
                  <Label htmlFor="oauth-baseurl">{t("Base URL（可选）")}</Label>
                  <Input id="oauth-baseurl" value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} />
                </div>}
              </div>

              <div className="flex items-center gap-2">
                <Button permission={BIND_PERMISSIONS} onClick={() => exchangeMutation.mutate(flowRevision.current)} disabled={isPending} className="flex-1">
                  {exchangeMutation.isPending ? t("绑定中...") : t("完成绑定")}
                </Button>
                <Button permission={BIND_PERMISSIONS}
                  type="button"
                  variant="outline"
                  onClick={requestAuthorization}
                  disabled={isPending}
                >{t("重新生成")}</Button>
              </div>
            </>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
