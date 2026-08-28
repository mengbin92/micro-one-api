import { t } from '@/lib/i18n';export interface SSEEvent {
  data: string;
  raw: string;
}

export const MAX_SSE_EVENT_BYTES = 2 * 1024 * 1024;

export class SSEProtocolError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'SSEProtocolError';
  }
}

function parseEvent(raw: string): SSEEvent | null {
  const dataLines: string[] = [];
  for (const line of raw.split('\n')) {
    if (!line || line.startsWith(':')) continue;
    const separator = line.indexOf(':');
    const field = separator === -1 ? line : line.slice(0, separator);
    if (field !== 'data') continue;
    let value = separator === -1 ? '' : line.slice(separator + 1);
    if (value.startsWith(' ')) value = value.slice(1);
    dataLines.push(value);
  }

  if (dataLines.length === 0) return null;
  return { data: dataLines.join('\n'), raw };
}

function normalizeLineEndings(value: string) {
  return value.replace(/\r\n/g, '\n').replace(/\r/g, '\n');
}

export async function* parseSSEStream(stream: ReadableStream<Uint8Array>): AsyncGenerator<SSEEvent> {
  const reader = stream.getReader();
  const decoder = new TextDecoder('utf-8');
  const encoder = new TextEncoder();
  let buffer = '';
  let bufferBytes = 0;
  let pendingCR = false;
  let completed = false;

  const appendDecoded = (decoded: string, final = false) => {
    let value = pendingCR ? `\r${decoded}` : decoded;
    pendingCR = false;
    if (!final && value.endsWith('\r')) {
      value = value.slice(0, -1);
      pendingCR = true;
    }
    const normalized = normalizeLineEndings(value);
    buffer += normalized;
    bufferBytes += encoder.encode(normalized).byteLength;
  };

  const drainEvents = () => {
    const events: SSEEvent[] = [];
    let boundary = buffer.indexOf('\n\n');
    while (boundary >= 0) {
      const raw = buffer.slice(0, boundary);
      bufferBytes -= encoder.encode(buffer.slice(0, boundary + 2)).byteLength;
      buffer = buffer.slice(boundary + 2);
      const event = parseEvent(raw);
      if (event) events.push(event);
      boundary = buffer.indexOf('\n\n');
    }
    if (bufferBytes > MAX_SSE_EVENT_BYTES) {
      throw new SSEProtocolError(t("SSE 单个事件超过 2 MiB 限制"));
    }
    return events;
  };

  try {
    while (true) {
      const result = await reader.read();
      if (result.done) break;
      appendDecoded(decoder.decode(result.value, { stream: true }));
      for (const event of drainEvents()) yield event;
    }

    appendDecoded(decoder.decode(), true);
    for (const event of drainEvents()) yield event;
    const finalEvent = parseEvent(buffer);
    if (finalEvent) yield finalEvent;
    completed = true;
  } finally {
    if (!completed) {
      try {
        await reader.cancel();
      } catch {
        // The stream may already be errored or aborted.
      }
    }
    reader.releaseLock();
  }
}
