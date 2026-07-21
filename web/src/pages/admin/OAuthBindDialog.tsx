import { useMutation } from '@tanstack/react-query';
import { Copy, ExternalLink, ShieldCheck } from 'lucide-react';
import { useState } from 'react';
import { toast } from 'sonner';
import { adminApiClient } from '@/lib/api';
import { Button } from '@/components/ui/button';
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

const PLATFORM_OPTIONS: Array<{ value: string; label: string }> = [
  { value: 'claude', label: 'Claude (Claude Code OAuth)' },
  { value: 'codex', label: 'Codex (ChatGPT OAuth)' },
  // Kimi is wired here once the Kimi CLI OAuth authorize flow is captured
  // (roadmap P3 §3). GLM/MiniMax are static-key platforms and do not use
  // this dialog — they are created via the CreateAccountDialog.
];

interface AuthURLResult {
  auth_url: string;
  session_id: string;
  state: string;
  expires_at: number;
}

interface OAuthBindDialogProps {
  onBound: () => void;
}

/**
 * Two-step OAuth authorization-code binding for subscription accounts.
 * Step 1: request an auth URL (proxied to channel-service via admin-api).
 * Step 2: the operator authorizes in a browser, pastes the callback URL/code
 * back, and we exchange it to create the subscription account.
 */
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

  const reset = () => {
    setSession(null);
    setCodeInput('');
    setName('');
    setGroup('default');
    setModels('');
    setPriority('0');
    setBaseUrl('');
  };

  const authUrlMutation = useMutation({
    mutationFn: async () => {
      const res = await adminApiClient.post(
        `/v1/admin/accounts/subscription/oauth/${platform}/auth-url`,
        {}
      );
      return res.data as AuthURLResult;
    },
    onSuccess: (data) => {
      if (!data?.auth_url) {
        toast.error('生成授权链接失败：返回为空');
        return;
      }
      setSession(data);
      window.open(data.auth_url, '_blank', 'noopener,noreferrer');
    },
    onError: () => toast.error('生成授权链接失败'),
  });

  const exchangeMutation = useMutation({
    mutationFn: async () => {
      if (!session) throw new Error('请先生成授权链接');
      const parsed = parseOAuthCallbackInput(codeInput);
      if (!parsed.code) throw new Error('请填写授权码或回调 URL');
      if (parsed.state && parsed.state !== session.state) {
        throw new Error('回调 URL 的 state 与当前授权会话不一致，请重新生成授权链接');
      }
      const res = await adminApiClient.post(
        `/v1/admin/accounts/subscription/oauth/${platform}/exchange`,
        {
          session_id: session.session_id,
          state: session.state,
          code: parsed.code,
          name: name.trim(),
          group: group.trim(),
          models: models.trim(),
          priority: parseInt(priority || '0', 10),
          base_url: baseUrl.trim(),
        }
      );
      // Exchange returns {success, account_id, ...} directly (channel-service),
      // or {error} on failure.
      if (res.data?.error) throw new Error(res.data.error);
      if (res.data?.success === false) throw new Error(res.data?.message || '授权码兑换失败');
      return res.data;
    },
    onSuccess: () => {
      toast.success('订阅账号已通过 OAuth 绑定');
      reset();
      setOpen(false);
      onBound();
    },
    onError: (error: Error) => toast.error(error.message || '授权码兑换失败'),
  });

  const copyAuthUrl = () => {
    if (session?.auth_url) {
      void navigator.clipboard?.writeText(session.auth_url);
      toast.success('授权链接已复制');
    }
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) reset();
      }}
    >
      <DialogTrigger render={<Button variant="outline" />}>
        <ShieldCheck className="size-4" />
        OAuth 授权绑定
      </DialogTrigger>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>OAuth 授权绑定</DialogTitle>
          <DialogDescription>
            通过 OAuth 授权码流程绑定 Claude / Codex 订阅账号，无需手动粘贴 token。授权会话 5
            分钟内有效，且必须在生成授权链接的同一服务副本上完成兑换。
          </DialogDescription>
        </DialogHeader>

        <div className="grid gap-4 pt-2">
          <div className="space-y-2">
            <Label htmlFor="oauth-platform">平台</Label>
            <select
              id="oauth-platform"
              value={platform}
              disabled={!!session}
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

          {!session ? (
            <Button onClick={() => authUrlMutation.mutate()} disabled={authUrlMutation.isPending}>
              <ExternalLink className="size-4" />
              {authUrlMutation.isPending ? '生成中...' : '生成授权链接并打开'}
            </Button>
          ) : (
            <>
              <div className="space-y-2">
                <Label>授权链接(已在新标签打开)</Label>
                <div className="flex items-center gap-2">
                  <Input readOnly value={session.auth_url} className="font-mono text-xs" />
                  <Button type="button" variant="outline" size="icon-sm" onClick={copyAuthUrl}>
                    <Copy className="size-4" />
                  </Button>
                </div>
                <p className="text-xs text-muted-foreground">
                  在浏览器完成授权后，把回调地址(含 <code>?code=...</code>)或授权码粘贴到下方。
                </p>
                {platform === 'codex' ? (
                  <p className="rounded-md bg-amber-50 px-2.5 py-2 text-xs text-amber-700 dark:bg-amber-950/40 dark:text-amber-400">
                    ⚠️ Codex 授权完成后浏览器会跳转到 <code>http://localhost:1455/auth/callback?...</code>，
                    该页面<strong>无法打开(显示“无法访问/连接被拒绝”)属于正常现象</strong> —— 这个地址是
                    Codex CLI 的本地回调，本系统并不在该端口监听。请直接<strong>从浏览器地址栏复制整段 URL</strong>
                    (包含 <code>code=</code>)粘贴到下方即可。
                  </p>
                ) : null}
              </div>

              <div className="space-y-2">
                <Label htmlFor="oauth-code">授权码 / 回调 URL</Label>
                <Input
                  id="oauth-code"
                  value={codeInput}
                  onChange={(e) => setCodeInput(e.target.value)}
                  placeholder="粘贴 code 或 http://.../callback?code=...&state=..."
                />
              </div>

              <div className="grid gap-4 sm:grid-cols-2">
                <div className="space-y-2">
                  <Label htmlFor="oauth-name">账号名称</Label>
                  <Input id="oauth-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="claude-pro-1" />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="oauth-group">分组</Label>
                  <Input id="oauth-group" value={group} onChange={(e) => setGroup(e.target.value)} />
                </div>
                <div className="space-y-2 sm:col-span-2">
                  <Label htmlFor="oauth-models">模型（逗号分隔，可选）</Label>
                  <Input
                    id="oauth-models"
                    value={models}
                    onChange={(e) => setModels(e.target.value)}
                    placeholder="claude-sonnet-4-5,claude-opus-4-1"
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="oauth-priority">优先级</Label>
                  <Input
                    id="oauth-priority"
                    type="number"
                    value={priority}
                    onChange={(e) => setPriority(e.target.value)}
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="oauth-baseurl">Base URL（可选）</Label>
                  <Input id="oauth-baseurl" value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} />
                </div>
              </div>

              <div className="flex items-center gap-2">
                <Button onClick={() => exchangeMutation.mutate()} disabled={exchangeMutation.isPending} className="flex-1">
                  {exchangeMutation.isPending ? '绑定中...' : '完成绑定'}
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  onClick={() => authUrlMutation.mutate()}
                  disabled={authUrlMutation.isPending}
                >
                  重新生成
                </Button>
              </div>
            </>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
