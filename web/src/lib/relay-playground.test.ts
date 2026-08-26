import { describe, expect, it, vi } from 'vitest';
import { executeChatCompletion, fetchRelayModels, RelayPlaygroundError } from './relay-playground';

function response(body: BodyInit | null, init?: ResponseInit) {
  return new Response(body, {
    headers: { 'Content-Type': 'application/json', 'X-Request-ID': 'req-test' },
    ...init,
  });
}

describe('relay playground client', () => {
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
});
