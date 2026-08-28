import { parseSSEStream, SSEProtocolError, type SSEEvent } from '@/lib/sse';
import { t } from '@/lib/i18n';

export interface RelayModel {
  id: string;
  object?: string;
  created?: number;
  owned_by?: string;
}

export interface PlaygroundMessageInput {
  role: 'system' | 'user' | 'assistant';
  content: string;
}

export interface PlaygroundRequest {
  model: string;
  messages: PlaygroundMessageInput[];
  stream: boolean;
  stream_options?: { include_usage: boolean };
  temperature?: number;
  max_tokens?: number;
}

export interface RelayUsage {
  prompt_tokens?: number;
  completion_tokens?: number;
  input_tokens?: number;
  output_tokens?: number;
  total_tokens?: number;
  prompt_tokens_details?: { cached_tokens?: number };
}

export interface RelayChoice {
  message?: { role?: string; content?: string };
  delta?: { role?: string; content?: string; reasoning_content?: string };
  finish_reason?: string | null;
}

export interface RelayCompletionResponse {
  id?: string;
  model?: string;
  choices?: RelayChoice[];
  usage?: RelayUsage;
}

export type PlaygroundErrorKind =
  | 'invalid_key'
  | 'forbidden_model'
  | 'insufficient_quota'
  | 'rate_limited'
  | 'upstream_unavailable'
  | 'invalid_request'
  | 'cors_or_network'
  | 'protocol_error'
  | 'aborted'
  | 'unknown';

export class RelayPlaygroundError extends Error {
  readonly kind: PlaygroundErrorKind;
  readonly status?: number;
  readonly requestId?: string;
  readonly bodyPreview?: string;

  constructor(message: string, options: { kind: PlaygroundErrorKind; status?: number; requestId?: string; bodyPreview?: string }) {
    super(message);
    this.name = 'RelayPlaygroundError';
    this.kind = options.kind;
    this.status = options.status;
    this.requestId = options.requestId;
    this.bodyPreview = options.bodyPreview;
  }
}

export interface ChatCompletionCallbacks {
  onEvent?: (event: SSEEvent) => void;
  onDelta?: (content: string) => void;
  onReasoning?: (content: string) => void;
  onUsage?: (usage: RelayUsage) => void;
  onFinishReason?: (reason: string | null | undefined) => void;
}

export interface ChatCompletionResult {
  status: number;
  requestId?: string;
  response?: RelayCompletionResponse;
  usage?: RelayUsage;
  finishReason?: string | null;
  streamed: boolean;
}

interface RequestOptions {
  baseUrl: string;
  apiKey: string;
  signal?: AbortSignal;
  requestId?: string;
}

function endpoint(baseUrl: string, path: string) {
  return `${baseUrl.replace(/\/+$/, '')}${path}`;
}

function safeRequestId(response: Response, fallback?: string) {
  return response.headers.get('X-Request-ID') || fallback;
}

function errorKind(status?: number, payload?: unknown): PlaygroundErrorKind {
  if (status === 401) return 'invalid_key';
  if (status === 403) return 'forbidden_model';
  if (status === 402) return 'insufficient_quota';
  if (status === 429) return 'rate_limited';
  if (status != null && status >= 500) return 'upstream_unavailable';
  if (status === 400 || status === 422) return 'invalid_request';

  const text = JSON.stringify(payload ?? '').toLowerCase();
  if (text.includes('quota') || text.includes('balance') || text.includes('insufficient')) return 'insufficient_quota';
  return 'unknown';
}

async function parseResponseBody(response: Response) {
  const contentType = response.headers.get('Content-Type') || '';
  const text = await response.text();
  const preview = text.slice(0, 4096);
  if (contentType.includes('json')) {
    try {
      return { payload: JSON.parse(text) as unknown, preview };
    } catch {
      return { payload: undefined, preview };
    }
  }
  return { payload: undefined, preview };
}

function messageFromPayload(payload: unknown, status?: number) {
  if (payload && typeof payload === 'object') {
    const value = payload as { error?: unknown; message?: unknown };
    const error = value.error;
    if (error && typeof error === 'object' && typeof (error as { message?: unknown }).message === 'string') {
      return (error as { message: string }).message.slice(0, 1000);
    }
    if (typeof error === 'string' && error.trim()) return error.slice(0, 1000);
    if (typeof value.message === 'string' && value.message.trim()) return value.message.slice(0, 1000);
  }
  return status ? t(`Relay 请求失败（HTTP ${status}）`) : t("Relay 请求失败");
}

function parseCompletionPayload(payload: unknown) {
  if (!payload || typeof payload !== 'object') return undefined;
  const response = payload as RelayCompletionResponse;
  if (!Array.isArray(response.choices)) return undefined;
  const choice = response.choices[0];
  if (!choice || typeof choice.message?.content !== 'string') return undefined;
  return { response, choice };
}

function normalizeUsage(usage?: RelayUsage): RelayUsage | undefined {
  if (!usage) return undefined;
  return {
    ...usage,
    prompt_tokens: usage.prompt_tokens ?? usage.input_tokens,
    completion_tokens: usage.completion_tokens ?? usage.output_tokens,
  };
}

async function throwResponseError(response: Response, fallbackRequestId?: string): Promise<never> {
  const body = await parseResponseBody(response);
  throw new RelayPlaygroundError(messageFromPayload(body.payload, response.status), {
    kind: errorKind(response.status, body.payload),
    status: response.status,
    requestId: safeRequestId(response, fallbackRequestId),
    bodyPreview: body.preview,
  });
}

function throwNetworkError(error: unknown): never {
  if (error instanceof RelayPlaygroundError) throw error;
  if (error instanceof DOMException && error.name === 'AbortError') {
    throw new RelayPlaygroundError(t("请求已停止"), { kind: 'aborted' });
  }
  throw new RelayPlaygroundError(t("无法连接 Relay，请检查 API 地址、网络和 CORS 配置"), {
    kind: 'cors_or_network',
  });
}

export async function fetchRelayModels(options: RequestOptions): Promise<RelayModel[]> {
  let response: Response;
  try {
    response = await fetch(endpoint(options.baseUrl, '/v1/models'), {
      method: 'GET',
      headers: {
        Authorization: `Bearer ${options.apiKey}`,
        Accept: 'application/json',
        ...(options.requestId ? { 'X-Request-ID': options.requestId } : {}),
      },
      signal: options.signal,
    });
  } catch (error) {
    return throwNetworkError(error);
  }

  if (!response.ok) return throwResponseError(response, options.requestId);
  let payload: unknown;
  try {
    payload = await response.json();
  } catch {
    throw new RelayPlaygroundError(t("Relay 模型列表不是有效 JSON"), { kind: 'protocol_error', status: response.status });
  }

  const rows = payload && typeof payload === 'object' ? (payload as { data?: unknown }).data : undefined;
  if (!Array.isArray(rows)) {
    throw new RelayPlaygroundError(t("Relay 模型列表格式异常"), { kind: 'protocol_error', status: response.status });
  }

  return rows
    .filter((item): item is RelayModel => Boolean(item && typeof item === 'object' && typeof (item as RelayModel).id === 'string'))
    .map((item) => ({ ...item, id: item.id.trim() }))
    .filter((item) => item.id)
    .filter((item, index, items) => items.findIndex((candidate) => candidate.id.toLowerCase() === item.id.toLowerCase()) === index)
    .sort((left, right) => left.id.localeCompare(right.id));
}

export async function executeChatCompletion(
  options: RequestOptions & { request: PlaygroundRequest; callbacks?: ChatCompletionCallbacks },
): Promise<ChatCompletionResult> {
  let response: Response;
  try {
    response = await fetch(endpoint(options.baseUrl, '/v1/chat/completions'), {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${options.apiKey}`,
        'Content-Type': 'application/json',
        Accept: options.request.stream ? 'text/event-stream, application/json' : 'application/json',
        ...(options.requestId ? { 'X-Request-ID': options.requestId } : {}),
      },
      body: JSON.stringify(options.request),
      signal: options.signal,
    });
  } catch (error) {
    return throwNetworkError(error);
  }

  if (!response.ok) return throwResponseError(response, options.requestId);
  const requestId = safeRequestId(response, options.requestId);
  const contentType = response.headers.get('Content-Type') || '';
  const callbacks = options.callbacks;

  if (!options.request.stream || !contentType.toLowerCase().includes('text/event-stream')) {
    let payload: unknown;
    try {
      payload = await response.json();
    } catch {
      throw new RelayPlaygroundError(t("Relay 返回了无法解析的响应"), { kind: 'protocol_error', status: response.status, requestId });
    }
    const completion = parseCompletionPayload(payload);
    if (!completion) {
      throw new RelayPlaygroundError(t("Relay 响应缺少 choices[0].message.content"), {
        kind: 'protocol_error',
        status: response.status,
        requestId,
      });
    }
    const { response: completionResponse, choice } = completion;
    const usage = normalizeUsage(completionResponse.usage);
    callbacks?.onUsage?.(usage ?? {});
    callbacks?.onFinishReason?.(choice?.finish_reason);
    callbacks?.onDelta?.(choice.message?.content || '');
    return { status: response.status, requestId, response: completionResponse, usage, finishReason: choice?.finish_reason, streamed: false };
  }

  if (!response.body) {
    throw new RelayPlaygroundError(t("Relay 没有返回可读取的流"), { kind: 'protocol_error', status: response.status, requestId });
  }

  let sawDone = false;
  let usage: RelayUsage | undefined;
  let finishReason: string | null | undefined;
  let malformedEvents = 0;

  try {
    for await (const event of parseSSEStream(response.body)) {
      callbacks?.onEvent?.(event);
      if (event.data === '[DONE]') {
        sawDone = true;
        continue;
      }
      let payload: unknown;
      try {
        payload = JSON.parse(event.data) as unknown;
        malformedEvents = 0;
      } catch {
        malformedEvents += 1;
        if (malformedEvents >= 3) {
          throw new RelayPlaygroundError(t("Relay 流式响应格式异常"), { kind: 'protocol_error', status: response.status, requestId });
        }
        continue;
      }
      if (!payload || typeof payload !== 'object') continue;
      const completion = payload as RelayCompletionResponse;
      const choice = completion.choices?.[0];
      const delta = choice?.delta?.content;
      if (delta) callbacks?.onDelta?.(delta);
      const reasoning = choice?.delta?.reasoning_content;
      if (reasoning) callbacks?.onReasoning?.(reasoning);
      if (completion.usage) {
        const normalizedUsage = normalizeUsage(completion.usage);
        if (normalizedUsage) {
          usage = normalizedUsage;
          callbacks?.onUsage?.(normalizedUsage);
        }
      }
      if (choice?.finish_reason != null) {
        finishReason = choice.finish_reason;
        callbacks?.onFinishReason?.(finishReason);
      }
    }
  } catch (error) {
    if (error instanceof RelayPlaygroundError) throw error;
    if (error instanceof SSEProtocolError) {
      throw new RelayPlaygroundError(error.message, { kind: 'protocol_error', status: response.status, requestId });
    }
    return throwNetworkError(error);
  }

  if (!sawDone) {
    throw new RelayPlaygroundError(t("Relay 流在收到 [DONE] 前结束"), { kind: 'protocol_error', status: response.status, requestId });
  }

  return { status: response.status, requestId, usage, finishReason, streamed: true };
}
