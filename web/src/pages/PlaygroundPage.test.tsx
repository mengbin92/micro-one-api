import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { http, HttpResponse } from 'msw';
import { describe, expect, it, vi } from 'vitest';
import * as relay from '@/lib/relay-playground';
import { MAX_ASSISTANT_CONTENT_BYTES, PlaygroundPage } from './PlaygroundPage';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';
import { setPlaygroundCredential } from '@/lib/playground-credential';

describe('PlaygroundPage', () => {
  async function ready() {
    server.use(
      http.get('/api/status', () => HttpResponse.json({ success: true, data: { server_address: 'https://relay.test' } })),
      http.get('https://relay.test/v1/models', () => HttpResponse.json({ data: [{ id: 'demo-model' }] })),
    );
    setPlaygroundCredential('sk-playground-secret');
    const view = renderWithQuery(<MemoryRouter><PlaygroundPage /></MemoryRouter>);
    await screen.findByText('已验证：sk-p••••cret');
    return view;
  }

  it('guards simultaneous shortcut and click submissions before React renders', async () => {
    const execute = vi.spyOn(relay, 'executeChatCompletion').mockImplementation(() => new Promise(() => {}));
    await ready();
    fireEvent.change(screen.getByLabelText('输入消息'), { target: { value: 'one billable request' } });
    const input = screen.getByLabelText('输入消息');
    const send = screen.getByRole('button', { name: '发送' });
    act(() => {
      fireEvent.keyDown(input, { key: 'Enter', ctrlKey: true });
      fireEvent.click(send);
    });
    expect(execute).toHaveBeenCalledTimes(1);
    expect(screen.getByRole('button', { name: '重用消息' })).toBeDisabled();
  });

  it.each(['停止', '清空'])('ignores late deltas, usage and completion after %s and a subsequent request', async (action) => {
    type Options = Parameters<typeof relay.executeChatCompletion>[0];
    const pending: Array<{ options: Options; resolve: (result: relay.ChatCompletionResult) => void }> = [];
    vi.spyOn(relay, 'executeChatCompletion').mockImplementation(options => new Promise(resolve => pending.push({ options, resolve })));
    const user = userEvent.setup();
    await ready();
    await user.type(screen.getByLabelText('输入消息'), 'first');
    await user.click(screen.getByRole('button', { name: '发送' }));
    act(() => pending[0].options.callbacks?.onDelta?.('**partial**'));
    expect(screen.getByText('**partial**')).toBeVisible();
    await user.click(screen.getByRole('button', { name: action }));
    expect(pending[0].options.signal?.aborted).toBe(true);
    await user.type(screen.getByLabelText('输入消息'), 'second');
    await user.click(screen.getByRole('button', { name: '发送' }));
    await act(async () => {
      pending[0].options.callbacks?.onDelta?.('OLD LATE DELTA');
      pending[0].options.callbacks?.onUsage?.({ completion_tokens: 99999 });
      pending[0].resolve({ status: 200, streamed: true });
    });
    expect(screen.queryByText(/OLD LATE DELTA/)).not.toBeInTheDocument();
    expect(screen.queryByText('99,999')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '停止' })).toBeVisible();
    await act(async () => {
      pending[1].options.callbacks?.onDelta?.('**new answer**');
      pending[1].resolve({ status: 200, streamed: true });
    });
    expect(await screen.findByText('new answer')).toBeVisible();
    expect(screen.getByRole('button', { name: '发送' })).toBeDisabled();
  });

  it('aborts an active request on unmount', async () => {
    const execute = vi.spyOn(relay, 'executeChatCompletion').mockImplementation(() => new Promise(() => {}));
    const view = await ready();
    fireEvent.change(screen.getByLabelText('输入消息'), { target: { value: 'hello' } });
    fireEvent.click(screen.getByRole('button', { name: '发送' }));
    view.unmount();
    expect(execute.mock.calls[0][0].signal?.aborted).toBe(true);
  });

  it.each([['温度', '3'], ['最大 Token', '0'], ['最大 Token', '1.5']])('validates %s=%s before starting a billable request', async (label, value) => {
    const execute = vi.spyOn(relay, 'executeChatCompletion');
    await ready();
    fireEvent.change(screen.getByLabelText(label), { target: { value } });
    fireEvent.change(screen.getByLabelText('输入消息'), { target: { value: 'keep draft' } });
    fireEvent.click(screen.getByRole('button', { name: '发送' }));
    expect(screen.getByRole('alert')).toHaveTextContent('超出允许范围');
    expect(execute).not.toHaveBeenCalled();
    expect(screen.getByLabelText('输入消息')).toHaveValue('keep draft');
  });

  it.each([[402, '额度不足'], [403, '模型无权限']])('exposes HTTP %s recovery without automatic retries or losing the session', async (status, title) => {
    let calls = 0;
    server.use(http.post('https://relay.test/v1/chat/completions', () => { calls++; return HttpResponse.json({ error: { message: 'denied' } }, { status }); }));
    localStorage.setItem('token', 'user-session');
    const user = userEvent.setup();
    await ready();
    await user.type(screen.getByLabelText('输入消息'), 'hello');
    await user.click(screen.getByRole('button', { name: '发送' }));
    expect(await screen.findByRole('alert')).toHaveTextContent(title);
    if (status === 402) expect(screen.getByRole('link', { name: '前往充值' })).toHaveAttribute('href', '/recharge');
    else expect(screen.queryByRole('link', { name: '前往充值' })).not.toBeInTheDocument();
    expect(calls).toBe(1); expect(localStorage.getItem('token')).toBe('user-session');
  });
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

  it('reuses a historical user message as an editable draft without sending automatically', async () => {
    const user = userEvent.setup();
    const requests: Array<{ messages: Array<{ role: string; content: string }> }> = [];
    server.use(
      http.get('/api/status', () => HttpResponse.json({ success: true, data: { server_address: 'https://relay.test' } })),
      http.get('https://relay.test/v1/models', () => HttpResponse.json({ data: [{ id: 'demo-model' }] })),
      http.post('https://relay.test/v1/chat/completions', async ({ request }) => {
        requests.push(await request.json() as typeof requests[number]);
        return HttpResponse.json({ choices: [{ message: { content: `answer ${requests.length}` }, finish_reason: 'stop' }] });
      }),
    );
    setPlaygroundCredential('sk-playground-secret');
    renderWithQuery(<MemoryRouter initialEntries={['/playground']}><PlaygroundPage /></MemoryRouter>);

    await screen.findByText('已验证：sk-p••••cret');
    await user.click(screen.getByRole('checkbox', { name: '流式输出' }));
    const input = screen.getByLabelText('输入消息');
    await user.type(input, 'first question');
    await user.click(screen.getByRole('button', { name: '发送' }));
    expect(await screen.findByText('answer 1')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: '重用消息' }));
    expect(input).toHaveValue('first question');
    expect(input).toHaveFocus();
    expect(requests).toHaveLength(1);
    expect(screen.getByText('answer 1')).toBeInTheDocument();

    await user.type(input, ' edited');
    await user.click(screen.getByRole('button', { name: '发送' }));
    expect(await screen.findByText('answer 2')).toBeInTheDocument();
    expect(requests[1].messages).toEqual([
      { role: 'user', content: 'first question' },
      { role: 'assistant', content: 'answer 1' },
      { role: 'user', content: 'first question edited' },
    ]);
    expect(screen.getAllByRole('button', { name: '重用消息' })).toHaveLength(2);
  });
});
