import { act, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { TurnstileChallenge } from './TurnstileChallenge';

describe('TurnstileChallenge', () => {
  afterEach(() => { delete window.turnstile; });

  it('passes a verified token, clears expired tokens, and removes the widget on unmount', () => {
    const callback = vi.fn();
    const renderWidget = vi.fn().mockReturnValue('widget-1');
    const remove = vi.fn();
    window.turnstile = { ready: fn => fn(), render: renderWidget, remove };
    const view = render(<TurnstileChallenge siteKey="public-site-key" onToken={callback} />);
    const options = renderWidget.mock.calls[0][1];
    expect(options.sitekey).toBe('public-site-key');
    act(() => options.callback('verified-token'));
    expect(callback).toHaveBeenLastCalledWith('verified-token');
    act(() => options['expired-callback']());
    expect(callback).toHaveBeenLastCalledWith('');
    view.unmount();
    expect(remove).toHaveBeenCalledWith('widget-1');
  });

  it('fails closed on a widget error', () => {
    const callback = vi.fn();
    const renderWidget = vi.fn().mockReturnValue('widget-1');
    window.turnstile = { ready: fn => fn(), render: renderWidget, remove: vi.fn() };
    render(<TurnstileChallenge siteKey="public-site-key" onToken={callback} />);
    act(() => renderWidget.mock.calls[0][1]['error-callback']());
    expect(callback).toHaveBeenLastCalledWith('');
    expect(screen.getByRole('alert')).toHaveTextContent('安全验证加载失败，请刷新页面重试。');
  });
});
