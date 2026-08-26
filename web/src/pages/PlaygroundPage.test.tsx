import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { http, HttpResponse } from 'msw';
import { describe, expect, it } from 'vitest';
import { MAX_ASSISTANT_CONTENT_BYTES, PlaygroundPage } from './PlaygroundPage';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';
import { setPlaygroundCredential } from '@/lib/playground-credential';

describe('PlaygroundPage', () => {
  it('consumes a one-time token handoff and loads models from Relay', async () => {
    const secret = 'sk-playground-secret';
    server.use(
      http.get('/api/status', () => HttpResponse.json({ success: true, data: { server_address: 'https://relay.test' } })),
      http.get('https://relay.test/v1/models', ({ request }) => {
        expect(request.headers.get('authorization')).toBe(`Bearer ${secret}`);
        return HttpResponse.json({ data: [{ id: 'demo-model' }] });
      }),
    );
    setPlaygroundCredential(secret);

    renderWithQuery(
      <MemoryRouter initialEntries={['/playground']}>
        <PlaygroundPage />
      </MemoryRouter>,
    );

    expect(await screen.findByText('已验证：sk-p••••cret')).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'demo-model' })).toBeInTheDocument();
    expect(screen.queryByText(secret)).not.toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole('button', { name: '发送' })).toBeDisabled());
  });

  it('keeps reasoning content out of the visible assistant conversation', async () => {
    const user = userEvent.setup();
    const encoder = new TextEncoder();
    server.use(
      http.get('/api/status', () => HttpResponse.json({ success: true, data: { server_address: 'https://relay.test' } })),
      http.get('https://relay.test/v1/models', () => HttpResponse.json({ data: [{ id: 'demo-model' }] })),
      http.post('https://relay.test/v1/chat/completions', () =>
        new HttpResponse(
          new ReadableStream<Uint8Array>({
            start(controller) {
              controller.enqueue(encoder.encode('data: {"choices":[{"delta":{"reasoning_content":"private reasoning"}}]}\n\n'));
              controller.enqueue(encoder.encode('data: {"choices":[{"delta":{"content":"visible answer"},"finish_reason":"stop"}]}\n\n'));
              controller.enqueue(encoder.encode('data: [DONE]\n\n'));
              controller.close();
            },
          }),
          { headers: { 'Content-Type': 'text/event-stream' } },
        ),
      ),
    );
    setPlaygroundCredential('sk-playground-secret');
    renderWithQuery(
      <MemoryRouter initialEntries={['/playground']}>
        <PlaygroundPage />
      </MemoryRouter>,
    );

    await screen.findByText('已验证：sk-p••••cret');
    await user.type(screen.getByLabelText('输入消息'), 'hello');
    await user.click(screen.getByRole('button', { name: '发送' }));

    expect(await screen.findByText('visible answer')).toBeInTheDocument();
    expect(screen.queryByText('private reasoning')).not.toBeInTheDocument();
  });

  it('stops before retaining more than 4 MiB of assistant content', async () => {
    const user = userEvent.setup();
    server.use(
      http.get('/api/status', () => HttpResponse.json({ success: true, data: { server_address: 'https://relay.test' } })),
      http.get('https://relay.test/v1/models', () => HttpResponse.json({ data: [{ id: 'demo-model' }] })),
      http.post('https://relay.test/v1/chat/completions', () =>
        HttpResponse.json({
          choices: [{ message: { content: 'x'.repeat(MAX_ASSISTANT_CONTENT_BYTES + 1) }, finish_reason: 'stop' }],
        }),
      ),
    );
    setPlaygroundCredential('sk-playground-secret');
    renderWithQuery(
      <MemoryRouter initialEntries={['/playground']}>
        <PlaygroundPage />
      </MemoryRouter>,
    );

    await screen.findByText('已验证：sk-p••••cret');
    await user.type(screen.getByLabelText('输入消息'), 'hello');
    await user.click(screen.getByRole('button', { name: '发送' }));

    expect(await screen.findByText(/Assistant 响应超过 4 MiB/)).toBeInTheDocument();
  });
});
