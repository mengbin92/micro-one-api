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

  it('requests and displays streaming input and output token usage', async () => {
    const user = userEvent.setup();
    const encoder = new TextEncoder();
    server.use(
      http.get('/api/status', () => HttpResponse.json({ success: true, data: { server_address: 'https://relay.test' } })),
      http.get('https://relay.test/v1/models', () => HttpResponse.json({ data: [{ id: 'demo-model' }] })),
      http.post('https://relay.test/v1/chat/completions', async ({ request }) => {
        const body = await request.json() as { stream_options?: { include_usage?: boolean } };
        expect(body.stream_options?.include_usage).toBe(true);
        return new HttpResponse(
          new ReadableStream<Uint8Array>({
            start(controller) {
              controller.enqueue(encoder.encode('data: {"choices":[{"delta":{"content":"answer"},"finish_reason":"stop"}]}\n\n'));
              controller.enqueue(encoder.encode('data: {"choices":[],"usage":{"input_tokens":12,"output_tokens":4,"total_tokens":16}}\n\n'));
              controller.enqueue(encoder.encode('data: [DONE]\n\n'));
              controller.close();
            },
          }),
          { headers: { 'Content-Type': 'text/event-stream' } },
        );
      }),
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

    expect(await screen.findByText('answer')).toBeInTheDocument();
    expect(await screen.findByText('12')).toBeInTheDocument();
    expect(screen.getByText('4')).toBeInTheDocument();
  });

  it('keeps each request snapshot available after later turns', async () => {
    const user = userEvent.setup();
    const requests: Array<{ messages: Array<{ role: string; content: string }> }> = [];
    server.use(
      http.get('/api/status', () => HttpResponse.json({ success: true, data: { server_address: 'https://relay.test' } })),
      http.get('https://relay.test/v1/models', () => HttpResponse.json({ data: [{ id: 'demo-model' }] })),
      http.post('https://relay.test/v1/chat/completions', async ({ request }) => {
        requests.push(await request.json() as typeof requests[number]);
        return HttpResponse.json(
          { choices: [{ message: { content: `answer ${requests.length}` }, finish_reason: 'stop' }] },
          { headers: { 'X-Request-ID': `server-request-${requests.length}` } },
        );
      }),
    );
    setPlaygroundCredential('sk-playground-secret');
    renderWithQuery(<MemoryRouter initialEntries={['/playground']}><PlaygroundPage /></MemoryRouter>);

    await screen.findByText('已验证：sk-p••••cret');
    await user.click(screen.getByRole('checkbox', { name: '流式输出' }));
    await user.type(screen.getByLabelText('System Prompt（可选）'), 'first system');
    await user.type(screen.getByLabelText('输入消息'), 'first question');
    await user.click(screen.getByRole('button', { name: '发送' }));
    expect(await screen.findByText('answer 1')).toBeInTheDocument();

    await user.clear(screen.getByLabelText('System Prompt（可选）'));
    await user.type(screen.getByLabelText('System Prompt（可选）'), 'second system');
    await user.type(screen.getByLabelText('输入消息'), 'second question');
    await user.click(screen.getByRole('button', { name: '发送' }));
    expect(await screen.findByText('answer 2')).toBeInTheDocument();

    expect(requests[0].messages).toEqual([
      { role: 'system', content: 'first system' },
      { role: 'user', content: 'first question' },
    ]);
    expect(requests[1].messages).toEqual([
      { role: 'system', content: 'second system' },
      { role: 'user', content: 'first question' },
      { role: 'assistant', content: 'answer 1' },
      { role: 'user', content: 'second question' },
    ]);

    await user.click(screen.getAllByRole('button', { name: /查看请求 playground-chat-/ })[0]);
    expect(screen.getByText('server-request-1')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: '复制请求 JSON' }));
    expect(JSON.parse(await navigator.clipboard.readText())).toMatchObject({ messages: requests[0].messages });
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
