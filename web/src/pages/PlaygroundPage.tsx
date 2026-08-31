import { useEffect, useMemo, useRef, useState } from 'react';
import { Link } from 'react-router';
import { Activity, Check, ChevronDown, Copy, Eye, EyeOff, FlaskConical, Send, Square, Trash2 } from 'lucide-react';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { cn } from '@/lib/utils';
import { clearPlaygroundCredential, takePlaygroundCredential } from '@/lib/playground-credential';
import {
  executeChatCompletion,
  fetchRelayModels,
  RelayPlaygroundError,
  type PlaygroundErrorKind,
  type PlaygroundMessageInput,
  type PlaygroundRequest,
  type RelayModel,
  type RelayUsage,
} from '@/lib/relay-playground';
import { resolveRelayBaseUrl, type RelayAddress } from '@/lib/server-address';
import { locale, t } from '@/lib/i18n';

type MessageRole = 'system' | 'user' | 'assistant';
type MessageStatus = 'completed' | 'streaming' | 'stopped' | 'failed';
type PageStatus = 'needs_key' | 'loading_models' | 'ready' | 'submitting' | 'streaming' | 'completed' | 'stopped' | 'failed';

interface PlaygroundMessage {
  id: string;
  role: MessageRole;
  content: string;
  status?: MessageStatus;
  requestId?: string;
}

interface InspectorState {
  requestId?: string;
  status?: number;
  startedAt?: number;
  firstContentAt?: number;
  completedAt?: number;
  finishReason?: string | null;
  usage?: RelayUsage;
  requestBody?: PlaygroundRequest;
  rawEvents: string[];
  rawBytes: number;
}

const initialInspector: InspectorState = { rawEvents: [], rawBytes: 0 };
export const MAX_ASSISTANT_CONTENT_BYTES = 4 * 1024 * 1024;
const utf8Encoder = new TextEncoder();

function id(prefix: string) {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return `${prefix}-${crypto.randomUUID()}`;
  }
  return `${prefix}-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

function maskSecret(value: string) {
  if (value.length <= 8) return '••••••••';
  return `${value.slice(0, 4)}••••${value.slice(-4)}`;
}

function statusLabel(status: PageStatus) {
  switch (status) {
    case 'loading_models': return t("正在加载模型");
    case 'submitting': return t("正在连接");
    case 'streaming': return t("生成中");
    case 'completed': return t("已完成");
    case 'stopped': return t("已停止");
    case 'failed': return t("调用失败");
    case 'ready': return t("已验证");
    default: return t("等待 API Key");
  }
}

function formatDuration(start?: number, end?: number) {
  if (start == null || end == null) return '—';
  return `${Math.max(0, end - start).toFixed(0)} ms`;
}

function formatTokens(value?: number) {
  return typeof value === 'number' ? value.toLocaleString(locale()) : '—';
}

function errorTitle(kind: PlaygroundErrorKind) {
  switch (kind) {
    case 'invalid_key': return t("API Key 无效");
    case 'forbidden_model': return t("模型无权限");
    case 'insufficient_quota': return t("额度不足");
    case 'rate_limited': return t("请求过于频繁");
    case 'upstream_unavailable': return t("上游暂不可用");
    case 'invalid_request': return t("请求参数有误");
    case 'cors_or_network': return t("无法连接 Relay");
    case 'protocol_error': return t("响应格式异常");
    case 'aborted': return t("请求已停止");
    default: return t("调用失败");
  }
}

function isRelayError(error: unknown): error is RelayPlaygroundError {
  return Boolean(error && typeof error === 'object' && 'kind' in error && 'message' in error);
}

export function PlaygroundPage() {
  const [handedOffKey] = useState<string | null>(() => takePlaygroundCredential());
  const [address, setAddress] = useState<RelayAddress | null>(null);
  const [addressError, setAddressError] = useState('');
  const [apiKey, setApiKey] = useState(handedOffKey || '');
  const [showKey, setShowKey] = useState(false);
  const [verifiedKey, setVerifiedKey] = useState(false);
  const [models, setModels] = useState<RelayModel[]>([]);
  const [selectedModel, setSelectedModel] = useState('');
  const [systemPrompt, setSystemPrompt] = useState('');
  const [draft, setDraft] = useState('');
  const [temperature, setTemperature] = useState('');
  const [maxTokens, setMaxTokens] = useState('');
  const [stream, setStream] = useState(true);
  const [messages, setMessages] = useState<PlaygroundMessage[]>([]);
  const [status, setStatus] = useState<PageStatus>('needs_key');
  const [error, setError] = useState<RelayPlaygroundError | null>(null);
  const [modelError, setModelError] = useState<Error | null>(null);
  const [inspector, setInspector] = useState<InspectorState>(initialInspector);
  const [showInspector, setShowInspector] = useState(false);
  const autoVerifyKey = useRef(handedOffKey);
  const modelRequestSequence = useRef(0);
  const modelRequest = useRef<{ controller: AbortController } | null>(null);
  const activeRequest = useRef<{ id: string; controller: AbortController } | null>(null);
  const requestSequence = useRef(0);

  useEffect(() => {
    let cancelled = false;
    resolveRelayBaseUrl()
      .then((resolved) => {
        if (!cancelled) setAddress(resolved);
      })
      .catch((reason: unknown) => {
        if (!cancelled) setAddressError(reason instanceof Error ? reason.message : t("Relay 地址无效"));
      });

    return () => {
      cancelled = true;
      modelRequestSequence.current += 1;
      modelRequest.current?.controller.abort();
      modelRequest.current = null;
      activeRequest.current?.controller.abort();
      clearPlaygroundCredential();
    };
  }, []);

  useEffect(() => {
    const pendingKey = autoVerifyKey.current;
    if (!address || !pendingKey) return;
    autoVerifyKey.current = null;
    void verifyKey(pendingKey);
    // The one-time key is intentionally consumed only once after address discovery.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [address, autoVerifyKey]);

  const canSend = verifiedKey && Boolean(selectedModel) && draft.trim().length > 0 && !activeRequest.current;
  const currentAssistant = useMemo(() => messages.findLast((message) => message.role === 'assistant' && message.status !== 'completed'), [messages]);

  function resetActiveRequest() {
    activeRequest.current = null;
  }

  async function verifyKey(secret = apiKey) {
    const normalized = secret.trim();
    if (!normalized) {
      setModelError(new Error(t("请先输入 API Key")) as RelayPlaygroundError);
      setStatus('needs_key');
      return;
    }
    if (!address) {
      setModelError(new Error(addressError || t("Relay 地址尚未准备好")) as RelayPlaygroundError);
      return;
    }

    modelRequest.current?.controller.abort();
    const modelSequence = ++modelRequestSequence.current;
    const modelController = new AbortController();
    modelRequest.current = { controller: modelController };
    setStatus('loading_models');
    setModelError(null);
    setError(null);
    setVerifiedKey(false);
    setModels([]);
    const requestId = id('playground-models');
    try {
      const result = await fetchRelayModels({ baseUrl: address.url, apiKey: normalized, requestId, signal: modelController.signal });
      if (modelRequestSequence.current !== modelSequence || modelRequest.current?.controller !== modelController) return;
      setApiKey(normalized);
      setVerifiedKey(true);
      setModels(result);
      setSelectedModel((current) => result.some((model) => model.id === current) ? current : result[0]?.id || '');
      setStatus(result.length > 0 ? 'ready' : 'failed');
      if (result.length === 0) setModelError(new Error(t("当前 API Key 没有可用模型")) as RelayPlaygroundError);
    } catch (reason) {
      if (modelRequestSequence.current !== modelSequence || modelRequest.current?.controller !== modelController) return;
      const relayError = isRelayError(reason)
        ? reason
        : new Error(reason instanceof Error ? reason.message : t("模型加载失败")) as RelayPlaygroundError;
      setModelError(relayError);
      setStatus('needs_key');
    } finally {
      if (modelRequestSequence.current === modelSequence && modelRequest.current?.controller === modelController) {
        modelRequest.current = null;
      }
    }
  }

  function changeApiKey(value: string) {
    modelRequest.current?.controller.abort();
    modelRequest.current = null;
    modelRequestSequence.current += 1;
    activeRequest.current?.controller.abort();
    setApiKey(value);
    setVerifiedKey(false);
    setModels([]);
    setSelectedModel('');
    setError(null);
    setModelError(null);
    setStatus('needs_key');
  }

  function appendRawEvent(raw: string) {
    setInspector((current) => {
      if (current.rawBytes >= 2 * 1024 * 1024 || current.rawEvents.length >= 1000) return current;
      const nextBytes = current.rawBytes + utf8Encoder.encode(raw).byteLength;
      if (nextBytes > 2 * 1024 * 1024) return { ...current, rawEvents: [...current.rawEvents, '[raw event truncated]'], rawBytes: nextBytes };
      return { ...current, rawEvents: [...current.rawEvents, raw], rawBytes: nextBytes };
    });
  }

  async function sendMessage() {
    if (!canSend || !address) return;
    const content = draft.trim();
    const requestId = id('playground-chat');
    const controller = new AbortController();
    const startedAt = performance.now();
    const temperatureValue = temperature.trim() ? Number(temperature) : undefined;
    const maxTokensValue = maxTokens.trim() ? Number(maxTokens) : undefined;
    if (
      (temperatureValue != null && (!Number.isFinite(temperatureValue) || temperatureValue < 0 || temperatureValue > 2)) ||
      (maxTokensValue != null && (!Number.isInteger(maxTokensValue) || maxTokensValue < 1 || maxTokensValue > 128000))
    ) {
      setError(new RelayPlaygroundError(t("Temperature 或 Max Tokens 超出允许范围"), { kind: 'invalid_request' }));
      setStatus('failed');
      return;
    }
    const conversation: PlaygroundMessageInput[] = [
      ...(systemPrompt.trim() ? [{ role: 'system' as const, content: systemPrompt.trim() }] : []),
      ...messages.filter((message) => message.content.trim()).map(({ role, content }) => ({ role, content })),
      { role: 'user', content },
    ];
    const assistantId = id('assistant');
    const request = {
      model: selectedModel,
      messages: conversation,
      stream,
      ...(stream ? { stream_options: { include_usage: true } } : {}),
      ...(temperatureValue != null ? { temperature: temperatureValue } : {}),
      ...(maxTokensValue != null ? { max_tokens: maxTokensValue } : {}),
    };
    const sequence = ++requestSequence.current;
    let assistantContentBytes = messages.reduce(
      (total, message) => total + (message.role === 'assistant' ? utf8Encoder.encode(message.content).byteLength : 0),
      0,
    );
    activeRequest.current = { id: requestId, controller };
    setDraft('');
    setError(null);
    setStatus('submitting');
    setInspector({ requestId, startedAt, requestBody: request, rawEvents: [], rawBytes: 0 });
    setMessages((current) => [
      ...current,
      { id: id('user'), role: 'user', content },
      { id: assistantId, role: 'assistant', content: '', status: 'streaming', requestId },
    ]);

    const isCurrent = () => requestSequence.current === sequence && activeRequest.current?.id === requestId;
    const appendAssistant = (delta: string) => {
      if (!delta || !isCurrent()) return;
      const deltaBytes = utf8Encoder.encode(delta).byteLength;
      if (assistantContentBytes + deltaBytes > MAX_ASSISTANT_CONTENT_BYTES) {
        throw new RelayPlaygroundError(t("Assistant 响应超过 4 MiB，已停止读取"), { kind: 'protocol_error' });
      }
      assistantContentBytes += deltaBytes;
      setStatus((current) => current === 'submitting' ? 'streaming' : current);
      setInspector((current) => ({ ...current, firstContentAt: current.firstContentAt ?? performance.now() }));
      setMessages((current) => current.map((message) => message.id === assistantId ? { ...message, content: message.content + delta } : message));
    };

    try {
      const result = await executeChatCompletion({
        baseUrl: address.url,
        apiKey: apiKey.trim(),
        request,
        requestId,
        signal: controller.signal,
        callbacks: {
          onEvent: (event) => isCurrent() && appendRawEvent(event.raw),
          onDelta: appendAssistant,
          onUsage: (usage) => isCurrent() && setInspector((current) => ({ ...current, usage })),
          onFinishReason: (reason) => isCurrent() && setInspector((current) => ({ ...current, finishReason: reason })),
        },
      });
      if (!isCurrent()) return;
      const completedAt = performance.now();
      setInspector((current) => ({ ...current, status: result.status, requestId: result.requestId || requestId, completedAt, usage: result.usage || current.usage, finishReason: result.finishReason ?? current.finishReason }));
      setMessages((current) => current.map((message) => message.id === assistantId ? { ...message, status: 'completed' } : message));
      setStatus('completed');
      resetActiveRequest();
    } catch (reason) {
      if (!isCurrent()) return;
      const relayError = isRelayError(reason)
        ? reason
        : new Error(reason instanceof Error ? reason.message : t("调用失败")) as RelayPlaygroundError;
      const stopped = relayError.kind === 'aborted' || controller.signal.aborted;
      setError(stopped ? null : relayError);
      setStatus(stopped ? 'stopped' : 'failed');
      setInspector((current) => ({ ...current, status: relayError.status, requestId: relayError.requestId || requestId, completedAt: performance.now() }));
      setMessages((current) => current.map((message) => message.id === assistantId ? { ...message, status: stopped ? 'stopped' : 'failed' } : message));
      resetActiveRequest();
    }
  }

  function stopGeneration() {
    const active = activeRequest.current;
    if (!active) return;
    active.controller.abort();
    setStatus('stopped');
    setMessages((current) => current.map((message) => message.requestId === active.id ? { ...message, status: 'stopped' } : message));
    resetActiveRequest();
  }

  function clearConversation() {
    activeRequest.current?.controller.abort();
    requestSequence.current += 1;
    resetActiveRequest();
    setMessages([]);
    setDraft('');
    setError(null);
    setInspector(initialInspector);
    setStatus(verifiedKey ? 'ready' : 'needs_key');
  }

  async function copyRequest() {
    if (!inspector.requestBody) return;
    await navigator.clipboard.writeText(JSON.stringify(inspector.requestBody, null, 2));
  }

  return (
    <div className="mx-auto max-w-[1600px] space-y-5">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <div className="flex items-center gap-3">
            <FlaskConical className="size-7 text-blue-600 dark:text-blue-300" />
            <h2 className="text-3xl font-bold tracking-normal text-foreground">{t("在线调试")}</h2>
            <span className={cn('rounded-full px-2.5 py-1 text-xs font-semibold', status === 'failed' ? 'bg-red-100 text-red-700 dark:bg-red-500/15 dark:text-red-300' : 'bg-accent text-accent-foreground')}>
              {statusLabel(status)}
            </span>
          </div>
          <p className="mt-2 text-sm text-muted-foreground">{t("使用真实 API Key 调试 Chat Completions，结果会计入正常使用记录。")}</p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button type="button" variant="outline" size="sm" onClick={clearConversation} disabled={messages.length === 0 && !inspector.requestId}>
            <Trash2 className="size-4" />{t("清空")}</Button>
          <Button type="button" variant="outline" size="sm" render={<Link to="/usage" />}>{t("查看使用记录")}</Button>
        </div>
      </div>

      {addressError ? <div role="alert" className="rounded-xl border border-red-200 bg-red-50 px-4 py-3 text-sm font-semibold text-red-700 dark:border-red-500/30 dark:bg-red-500/10 dark:text-red-300">{addressError}</div> : null}
      {address?.warning ? <div className="rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 text-sm font-semibold text-amber-800 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-300">{address.warning}</div> : null}
      {error ? (
        <div role="alert" className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-red-200 bg-red-50 px-4 py-3 text-sm dark:border-red-500/30 dark:bg-red-500/10">
          <div>
            <p className="font-bold text-red-800 dark:text-red-200">{errorTitle(error.kind)}：{error.message}</p>
            {error.requestId ? <p className="mt-1 font-mono text-xs text-red-600/80 dark:text-red-300/80">Request ID: {error.requestId}</p> : null}
          </div>
          {error.kind === 'insufficient_quota' ? <Link className="font-bold text-red-700 underline dark:text-red-200" to="/recharge">{t("前往充值")}</Link> : null}
        </div>
      ) : null}

      <div className="grid gap-5 xl:grid-cols-[280px_minmax(0,1fr)_340px]">
        <Card className="rounded-2xl">
          <CardHeader className="border-b border-border">
            <CardTitle className="text-base">{t("连接与模型")}</CardTitle>
            <CardDescription>{t("密钥只保留在当前页面内存中。")}</CardDescription>
          </CardHeader>
          <CardContent className="space-y-5 p-5">
            <div className="space-y-2">
              <Label htmlFor="playground-address">{t("Relay 地址")}</Label>
              <Input id="playground-address" readOnly value={address?.url || t("正在读取…")} className="font-mono text-xs" />
              {address?.source === 'same-origin-fallback' ? <p className="text-xs text-amber-600 dark:text-amber-300">{t("同源回退地址，请确认已配置反向代理。")}</p> : null}
            </div>
            <div className="space-y-2">
              <Label htmlFor="playground-key">{t('API 密钥')}</Label>
              <div className="flex gap-2">
                <Input id="playground-key" type={showKey ? 'text' : 'password'} autoComplete="off" value={verifiedKey ? maskSecret(apiKey) : apiKey} onChange={(event) => changeApiKey(event.target.value)} placeholder={t("粘贴 sk-...")} disabled={status === 'loading_models' || Boolean(activeRequest.current)} />
                <Button type="button" variant="outline" size="icon" onClick={() => setShowKey((value) => !value)} aria-label={showKey ? t("隐藏 API Key") : t("显示 API Key")} disabled={verifiedKey}>
                  {showKey ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
                </Button>
              </div>
              {verifiedKey ? <p className="text-xs font-semibold text-emerald-600 dark:text-emerald-300"><Check className="mr-1 inline size-3.5" />{t("已验证：")}{maskSecret(apiKey)}</p> : null}
              {!apiKey ? <Link className="text-xs font-semibold text-primary hover:underline" to="/tokens">{t("还没有 API Key？前往创建")}</Link> : null}
              <Button type="button" className="w-full" onClick={() => void verifyKey()} disabled={!apiKey.trim() || !address || status === 'loading_models' || Boolean(activeRequest.current)}>
                {status === 'loading_models' ? t("加载模型中…") : t("验证并加载模型")}
              </Button>
            </div>

            <div className="space-y-2">
              <Label htmlFor="playground-model">{t("模型")}</Label>
              <select id="playground-model" value={selectedModel} onChange={(event) => setSelectedModel(event.target.value)} disabled={!verifiedKey || models.length === 0 || Boolean(activeRequest.current)} className="h-9 w-full rounded-xl border border-input bg-background px-3 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/25 disabled:cursor-not-allowed disabled:opacity-50">
                <option value="">{verifiedKey ? t("请选择模型") : t("先验证 API Key")}</option>
                {models.map((model) => <option key={model.id} value={model.id}>{model.id}</option>)}
              </select>
              {modelError ? <p role="alert" className="text-xs font-semibold text-red-600 dark:text-red-300">{isRelayError(modelError) ? `${errorTitle(modelError.kind)}：${modelError.message}` : modelError.message}</p> : null}
            </div>

            <details className="rounded-xl border border-border p-3" open>
              <summary className="flex cursor-pointer list-none items-center justify-between text-sm font-semibold text-foreground"><span>{t("高级参数")}</span><ChevronDown className="size-4" /></summary>
              <div className="mt-4 space-y-4">
                <div className="space-y-2">
                  <Label htmlFor="playground-temperature">{t('温度')}</Label>
                  <Input id="playground-temperature" type="number" min="0" max="2" step="0.1" value={temperature} onChange={(event) => setTemperature(event.target.value)} placeholder={t("自动")} disabled={Boolean(activeRequest.current)} />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="playground-max-tokens">{t('最大 Token')}</Label>
                  <Input id="playground-max-tokens" type="number" min="1" max="128000" step="1" value={maxTokens} onChange={(event) => setMaxTokens(event.target.value)} placeholder={t("自动")} disabled={Boolean(activeRequest.current)} />
                </div>
                <label className="flex items-center justify-between gap-3 text-sm font-semibold text-foreground">{t("流式输出")}<input type="checkbox" checked={stream} onChange={(event) => setStream(event.target.checked)} disabled={Boolean(activeRequest.current)} className="size-4 accent-primary" />
                </label>
              </div>
            </details>
          </CardContent>
        </Card>

        <Card className="flex min-h-[620px] flex-col rounded-2xl">
          <CardHeader className="border-b border-border">
            <CardTitle className="flex items-center gap-2 text-base"><Activity className="size-4 text-blue-600" />{t("对话")}</CardTitle>
            <CardDescription>{selectedModel || t("验证 API Key 后选择模型")}</CardDescription>
          </CardHeader>
          <CardContent className="flex flex-1 flex-col gap-4 p-5">
            <div className="min-h-0 flex-1 space-y-4 overflow-y-auto rounded-xl bg-muted p-4">
              {messages.length === 0 ? (
                <div className="grid min-h-[380px] place-items-center text-center text-sm text-muted-foreground">
                  <div><FlaskConical className="mx-auto mb-3 size-8 text-muted-foreground" /><p>{t("输入一条消息开始调试")}</p><p className="mt-1 text-xs">{t("Ctrl / Cmd + Enter 发送")}</p></div>
                </div>
              ) : messages.map((message) => {
                const roleLabel = message.role === 'user' ? t("用户") : message.role === 'system' ? t("系统") : t("助手");
                return (
                  <article
                    key={message.id}
                    aria-label={t(`${roleLabel}消息`)}
                    className={cn(
                      'rounded-2xl px-4 py-3 text-sm motion-safe:animate-in motion-safe:fade-in-0',
                      message.role === 'user'
                        ? 'ml-8 bg-primary text-primary-foreground'
                        : 'mr-8 border border-border bg-card text-foreground shadow-sm',
                    )}
                  >
                    <div className="mb-1 text-xs font-medium opacity-70">{roleLabel}{message.status === 'stopped' ? ` · ${t('已停止')}` : ''}</div>
                    <div className="whitespace-pre-wrap break-words leading-6">{message.content || (message.status === 'streaming' ? '…' : '')}</div>
                  </article>
                );
              })}
              {currentAssistant?.status === 'streaming' && !currentAssistant.content ? <span className="sr-only" aria-live="polite">{t("正在生成")}</span> : null}
            </div>
            <div className="space-y-3">
              <div className="space-y-2">
                <Label htmlFor="playground-system">{t("System Prompt（可选）")}</Label>
                <textarea id="playground-system" value={systemPrompt} onChange={(event) => setSystemPrompt(event.target.value)} disabled={Boolean(activeRequest.current)} rows={2} placeholder={t("定义助手行为…")} className="w-full resize-y rounded-xl border border-input bg-background px-3 py-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/25 disabled:cursor-not-allowed disabled:opacity-50" />
              </div>
              <div className="flex flex-col gap-2 sm:flex-row">
                <textarea aria-label={t("输入消息")} value={draft} onChange={(event) => setDraft(event.target.value)} onKeyDown={(event) => { if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') { event.preventDefault(); void sendMessage(); } }} disabled={!verifiedKey || Boolean(activeRequest.current)} rows={3} placeholder={verifiedKey ? t("输入消息…") : t("先验证 API Key")} className="min-h-20 flex-1 resize-y rounded-xl border border-input bg-background px-3 py-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/25 disabled:cursor-not-allowed disabled:opacity-50" />
                {activeRequest.current ? <Button type="button" variant="destructive" className="sm:w-28" onClick={stopGeneration}><Square className="size-4" />{t("停止")}</Button> : <Button type="button" className="sm:w-28" onClick={() => void sendMessage()} disabled={!canSend}><Send className="size-4" />{t("发送")}</Button>}
              </div>
            </div>
          </CardContent>
        </Card>

        <Card className="rounded-2xl">
          <CardHeader className="border-b border-border">
            <div className="flex items-center justify-between gap-2"><div><CardTitle className="text-base">{t("调试信息")}</CardTitle><CardDescription>{t("不显示完整 API Key")}</CardDescription></div><Button type="button" variant="ghost" size="sm" onClick={() => setShowInspector((value) => !value)}>{showInspector ? t("收起") : t("展开")}</Button></div>
          </CardHeader>
          <CardContent className="space-y-4 p-5">
            <div className="grid grid-cols-2 gap-3 text-sm">
              <Metric label="HTTP" value={inspector.status ? String(inspector.status) : '—'} />
              <Metric label={t('完成原因')} value={inspector.finishReason || '—'} />
              <Metric label="TTFT" value={formatDuration(inspector.startedAt, inspector.firstContentAt)} />
              <Metric label={t("总耗时")} value={formatDuration(inspector.startedAt, inspector.completedAt)} />
              <Metric label={t("输入 Token")} value={formatTokens(inspector.usage?.prompt_tokens)} />
              <Metric label={t("输出 Token")} value={formatTokens(inspector.usage?.completion_tokens)} />
            </div>
            {inspector.requestId ? <div className="rounded-xl bg-muted p-3"><p className="text-xs font-semibold text-muted-foreground">{t('请求 ID')}</p><p className="mt-1 break-all font-mono text-xs text-foreground">{inspector.requestId}</p></div> : null}
            <div className="flex flex-wrap gap-2">
              <Button type="button" variant="outline" size="sm" onClick={() => void copyRequest()} disabled={!inspector.requestBody}><Copy className="size-3.5" />{t("复制请求 JSON")}</Button>
              <Button type="button" variant="outline" size="sm" onClick={() => setShowInspector((value) => !value)}><Eye className="size-3.5" />{t("原始事件")}</Button>
            </div>
            {showInspector ? <pre className="max-h-72 overflow-auto rounded-xl bg-slate-950 p-3 text-xs leading-5 text-slate-200">{inspector.rawEvents.length ? inspector.rawEvents.join('\n\n') : t("暂无原始事件")}</pre> : null}
            <div className="rounded-xl border border-primary/20 bg-accent p-3 text-xs leading-5 text-accent-foreground">{t("费用和最终 Token 以使用记录为准。停止流式请求后，服务端可能已经产生部分用量。")}</div>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}

function Metric({ label, value }: { label: string; value: string }) {
  return <div className="rounded-xl bg-muted p-3"><p className="text-xs font-semibold text-muted-foreground">{label}</p><p className="mt-1 truncate font-semibold text-foreground">{value}</p></div>;
}
