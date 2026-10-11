import { describe, expect, it, vi } from 'vitest';
import { executeChatCompletion, fetchRelayModels, RelayPlaygroundError } from './relay-playground';

function response(body: BodyInit | null, init?: ResponseInit) {
  return new Response(body, {
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': 'req-test' },
    ...init,
  });
}

describe('relay playground client', () => {
  it('reports request and trace identity before an error or interrupted stream', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(response('{"error":{"message":"unavailable"}}', {
      status: 503, headers: { 'Content-Type': 'application/json', 'X-Request-ID': 'root-1', 'X-Trace-ID': 'compat-1', 'X-OTel-Trace-ID': 'otel-1' },
    }));
    const identity = vi.fn();
    await expect(executeChatCompletion({ baseUrl: 'https://relay.test', apiKey: 'sk-test', request: { model: 'm', messages: [], stream: true }, callbacks: { onIdentity: identity } })).rejects.toThrow();
    expect(identity).toHaveBeenCalledWith({ requestId: 'root-1', traceId: 'compat-1', otelTraceId: 'otel-1' });
  });
  it('loads, deduplicates, and sorts models with a bearer key', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      response(JSON.stringify({ data: [{ id: 'zeta' }, { id: 'Alpha' }, { id: 'alpha' }] })),
    );

    await expect(fetchRelayModels({ baseUrl: 'https://relay.test/', apiKey: 'sk-test' })).resolves.toEqual([
      { id: 'Alpha' },
      { id: 'zeta' },
    ]);
    expect(fetchMock).toHaveBeenCalledWith(
      'https://relay.test/v1/models',
      expect.objectContaining({ headers: expect.objectContaining({ Authorization: 'Bearer sk-test' }) }),
    );
  });

  it('parses streamed deltas, usage, and completion', async () => {
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        const encoder = new TextEncoder();
        controller.enqueue(encoder.encode('data: null\n\n'));
        controller.enqueue(encoder.encode('data: {"choices":[{"delta":{"content":"Hi"}}]}\n\n'));
        controller.enqueue(encoder.encode('data: {"choices":[{"delta":{"content":"!"},"finish_reason":"stop"}],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}\n\n'));
        controller.enqueue(encoder.encode('data: [DONE]\n\n'));
        controller.close();
      },
    });
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      response(stream, { headers: { 'Content-Type': 'text/event-stream', 'X-Request-ID': 'req-stream' } }),
    );
    const deltas: string[] = [];
    const usages: Array<{ prompt?: number; completion?: number }> = [];
    const result = await executeChatCompletion({
      baseUrl: 'https://relay.test',
      apiKey: 'sk-test',
      request: { model: 'demo', messages: [{ role: 'user', content: 'hello' }], stream: true },
      callbacks: {
        onDelta: (delta) => deltas.push(delta),
        onUsage: (usage) => usages.push({ prompt: usage.prompt_tokens, completion: usage.completion_tokens }),
      },
    });

    expect(deltas.join('')).toBe('Hi!');
    expect(usages).toEqual([{ prompt: 3, completion: 2 }]);
    expect(result).toMatchObject({
      streamed: true,
      finishReason: 'stop',
      requestId: 'req-stream',
      usage: { prompt_tokens: 3, completion_tokens: 2, total_tokens: 5 },
    });
  });

  it('accepts a terminal finish reason without DONE', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(response('data: {"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}\n\n', { headers: { 'Content-Type': 'text/event-stream' } }));
    const result = await executeChatCompletion({ baseUrl: 'https://relay.test', apiKey: 'sk-test', request: { model: 'demo', messages: [{role:'user',content:'hi'}], stream: true } });
    expect(result.finishReason).toBe('stop');
  });

  it('stops consuming after DONE and cancels an upstream that stays open', async () => {
    const cancel = vi.fn();
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(new TextEncoder().encode('data: [DONE]\n\ndata: {"choices":[{"delta":{"content":"unexpected"}}]}\n\n'));
      },
      cancel,
    });
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(response(stream, { headers: { 'Content-Type': 'text/event-stream' } }));
    const delta = vi.fn();
    const result = await executeChatCompletion({ baseUrl: 'https://relay.test', apiKey: 'sk-test', request: { model: 'demo', messages: [], stream: true }, callbacks: { onDelta: delta } });
    expect(result.streamed).toBe(true);
    expect(delta).not.toHaveBeenCalled();
    expect(cancel).toHaveBeenCalledTimes(1);
  });

  it('classifies HTTP errors without exposing the key', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      response(JSON.stringify({ error: { message: 'invalid api key' } }), { status: 401, statusText: 'Unauthorized' }),
    );

    const error = await fetchRelayModels({ baseUrl: 'https://relay.test', apiKey: 'sk-super-secret' }).catch((value) => value);
    expect(error).toBeInstanceOf(RelayPlaygroundError);
    expect(error).toMatchObject({ kind: 'invalid_key', status: 401, requestId: 'req-test' });
    expect((error as Error).message).not.toContain('sk-super-secret');
  });

  it('rejects a 2xx JSON response without completion content', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(response(JSON.stringify({ choices: [] })));

    const error = await executeChatCompletion({
      baseUrl: 'https://relay.test',
      apiKey: 'sk-test',
      request: { model: 'demo', messages: [{ role: 'user', content: 'hello' }], stream: false },
    }).catch((value) => value);

    expect(error).toBeInstanceOf(RelayPlaygroundError);
    expect(error).toMatchObject({ kind: 'protocol_error', status: 200, requestId: 'req-test' });
  });

  it.each([[401, 'invalid_key'], [403, 'forbidden_model'], [402, 'insufficient_quota'], [429, 'rate_limited'], [503, 'upstream_unavailable'], [400, 'invalid_request'], [422, 'invalid_request'], [409, 'unknown']] as const)('classifies HTTP %s without retrying a billable request', async (status, kind) => {
    const fetch = vi.spyOn(globalThis, 'fetch').mockResolvedValue(response('{"message":"denied"}', { status }));
    await expect(executeChatCompletion({ baseUrl: 'https://relay.test', apiKey: 'sk-test', requestId: 'client-1', request: { model: 'm', messages: [], stream: false } })).rejects.toMatchObject({ kind, status, requestId: 'req-test' });
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it.each([['{"error":"insufficient balance"}', 'insufficient_quota'], ['{"error":{"message":"quota exceeded"}}', 'insufficient_quota'], ['broken json', 'unknown']] as const)('handles nonstandard error payload %s', async (payload, kind) => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(response(payload, { status: 418 }));
    await expect(fetchRelayModels({ baseUrl: 'https://relay.test', apiKey: 'sk-test' })).rejects.toMatchObject({ kind });
  });

  it.each([new DOMException('stopped', 'AbortError'), new TypeError('network')])('distinguishes cancellation from connectivity failure', async (cause) => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(cause);
    const kind = cause instanceof DOMException ? 'aborted' : 'cors_or_network';
    await expect(fetchRelayModels({ baseUrl: 'https://relay.test', apiKey: 'sk-test' })).rejects.toMatchObject({ kind });
    await expect(executeChatCompletion({ baseUrl: 'https://relay.test', apiKey: 'sk-test', request: { model: 'm', messages: [], stream: true } })).rejects.toMatchObject({ kind });
  });

  it.each(['not json', '{"data":null}'])('rejects invalid model discovery %s', async (payload) => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(response(payload));
    await expect(fetchRelayModels({ baseUrl: 'https://relay.test', apiKey: 'sk-test' })).rejects.toMatchObject({ kind: 'protocol_error' });
  });

  it('filters invalid model rows and normalizes successful JSON usage', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(response('{"data":[null,{},123,{"id":" "},{"id":"m"}]}')).mockResolvedValueOnce(response('{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"input_tokens":3,"output_tokens":1}}'));
    expect(await fetchRelayModels({ baseUrl: 'https://relay.test', apiKey: 'sk-test' })).toEqual([{ id: 'm' }]);
    const delta = vi.fn();
    const result = await executeChatCompletion({ baseUrl: 'https://relay.test', apiKey: 'sk-test', request: { model: 'm', messages: [], stream: false }, callbacks: { onDelta: delta } });
    expect(result.usage).toMatchObject({ prompt_tokens: 3, completion_tokens: 1 }); expect(delta).toHaveBeenCalledWith('ok');
  });

  it.each([
    ['data: {"choices":[{"delta":{"content":"partial"}}]}\n\n', 'Relay 流在收到 [DONE] 前结束'],
    ['data: broken\n\ndata: broken\n\ndata: broken\n\n', 'Relay 流式响应格式异常'],
  ])('keeps incomplete or malformed streams out of the success state', async (text, message) => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(response(text, { headers: { 'Content-Type': 'text/event-stream' } }));
    await expect(executeChatCompletion({ baseUrl: 'https://relay.test', apiKey: 'sk-test', request: { model: 'm', messages: [], stream: true } })).rejects.toMatchObject({ kind: 'protocol_error', message });
  });

  it('allows an isolated malformed event to recover and forwards reasoning separately', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(response('data: broken\n\ndata: {"choices":[{"delta":{"reasoning_content":"private","content":"visible"}}]}\n\ndata: [DONE]\n\n', { headers: { 'Content-Type': 'text/event-stream' } }));
    const reasoning = vi.fn(); const delta = vi.fn();
    await executeChatCompletion({ baseUrl: 'https://relay.test', apiKey: 'sk-test', request: { model: 'm', messages: [], stream: true }, callbacks: { onDelta: delta, onReasoning: reasoning } });
    expect(reasoning).toHaveBeenCalledWith('private'); expect(delta).toHaveBeenCalledWith('visible');
  });

  it('rejects unreadable JSON and missing response streams', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(response('bad')).mockResolvedValueOnce(response(null, { headers: { 'Content-Type': 'text/event-stream' } }));
    const options = { baseUrl: 'https://relay.test', apiKey: 'sk-test', request: { model: 'm', messages: [], stream: true } };
    await expect(executeChatCompletion(options)).rejects.toMatchObject({ kind: 'protocol_error' });
    await expect(executeChatCompletion(options)).rejects.toMatchObject({ kind: 'protocol_error' });
  });
});
