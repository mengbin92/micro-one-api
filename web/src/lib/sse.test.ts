import { describe, expect, it } from 'vitest';
import { MAX_SSE_EVENT_BYTES, parseSSEStream, SSEProtocolError } from './sse';

function streamFromChunks(chunks: string[]): ReadableStream<Uint8Array> {
  const encoder = new TextEncoder();
  return new ReadableStream({
    start(controller) {
      for (const chunk of chunks) controller.enqueue(encoder.encode(chunk));
      controller.close();
    },
  });
}

async function collect(chunks: string[]) {
  const events: Array<{ data: string; raw: string }> = [];
  for await (const event of parseSSEStream(streamFromChunks(chunks))) events.push(event);
  return events;
}

describe('parseSSEStream', () => {
  it('handles chunk boundaries and CRLF framing', async () => {
    await expect(collect(['data: {"a":', '1}\r\n\r\ndata: [DONE]\r\n\r\n'])).resolves.toEqual([
      { data: '{"a":1}', raw: 'data: {"a":1}' },
      { data: '[DONE]', raw: 'data: [DONE]' },
    ]);
  });

  it('does not invent an event boundary when CRLF is split across chunks', async () => {
    await expect(collect(['data: first\r', '\ndata: second\r', '\n\r', '\n'])).resolves.toEqual([
      { data: 'first\nsecond', raw: 'data: first\ndata: second' },
    ]);
  });

  it('joins repeated data fields and ignores comments', async () => {
    await expect(collect([': keep-alive\n', 'data: first\n', 'data: second\n\n'])).resolves.toEqual([
      { data: 'first\nsecond', raw: ': keep-alive\ndata: first\ndata: second' },
    ]);
  });

  it('flushes a final event without a trailing blank line', async () => {
    await expect(collect(['data: final'])).resolves.toEqual([{ data: 'final', raw: 'data: final' }]);
  });

  it('rejects an oversized unterminated event', async () => {
    await expect(collect([`data: ${'x'.repeat(MAX_SSE_EVENT_BYTES)}`])).rejects.toBeInstanceOf(SSEProtocolError);
  });

  it('cancels the reader when the consumer stops early', async () => {
    let cancelled = false;
    const encoder = new TextEncoder();
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(encoder.encode('data: first\n\n'));
      },
      cancel() {
        cancelled = true;
      },
    });

    for await (const event of parseSSEStream(stream)) {
      expect(event.data).toBe('first');
      break;
    }
    expect(cancelled).toBe(true);
  });
});
