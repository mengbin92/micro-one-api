import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import AssistantMarkdown from './AssistantMarkdown';
import { AssistantContent, MAX_MARKDOWN_CHARACTERS } from './AssistantContent';

describe('safe assistant Markdown', () => {
  it('renders headings, emphasis, lists, tables, and disabled tasks', () => {
    const { container } = render(<AssistantMarkdown content={'# 回答\n\n**重点**与`代码`\n\n- 第一项\n- 第二项\n\n| 用量 | 金额 |\n| --- | --- |\n| 10 | $0.01 |\n\n- [x] 完成'} />);
    expect(screen.getByRole('heading', { name: '回答' })).toBeVisible();
    expect(container.querySelector('strong')).toHaveTextContent('重点');
    expect(screen.getByRole('table')).toHaveTextContent('$0.01');
    expect(screen.getByRole('checkbox')).toBeDisabled();
  });

  it('ignores raw HTML and never loads images, SVG, scripts, frames, or event handlers', () => {
    const { container } = render(<AssistantMarkdown content={'<script>alert(1)</script>\n\n<img src="https://evil.test/pixel" onerror="alert(1)">\n\n<svg onload="alert(1)"></svg>\n\n<iframe src="https://evil.test"></iframe>\n\n![说明](https://evil.test/track)\n\n正常内容'} />);
    expect(screen.getByText('正常内容')).toBeVisible();
    expect(screen.getByText('图片：说明')).toBeVisible();
    expect(container.querySelector('script, img, svg, iframe, style, [onerror], [onload]')).toBeNull();
  });

  it.each([
    'javascript:alert%281%29', 'JaVaScRiPt:alert%281%29', 'jav&#x61;script:alert%281%29',
    'data:text/html,attack', 'vbscript:attack', 'file:///etc/passwd', '//evil.test',
    '/admin/users', '#private', 'https://user:password@evil.test', 'https://[',
  ])('renders dangerous or console-relative link %s as text', (url) => {
    const { container } = render(<AssistantMarkdown content={`[点击](${url})`} />);
    expect(screen.getByText('点击')).toBeVisible();
    expect(container.querySelector('a')).toBeNull();
  });

  it('opens explicit web links with an isolated opener', () => {
    render(<AssistantMarkdown content={'[文档](https://example.test/path?q=1)'} />);
    const link = screen.getByRole('link', { name: '文档' });
    expect(link).toHaveAttribute('href', 'https://example.test/path?q=1');
    expect(link).toHaveAttribute('target', '_blank');
    expect(link).toHaveAttribute('rel', 'noopener noreferrer nofollow');
  });

  it('copies only literal code and lets the user recover from clipboard denial', async () => {
    const user = userEvent.setup();
    const write = vi.spyOn(navigator.clipboard, 'writeText').mockRejectedValueOnce(new Error('denied')).mockResolvedValueOnce();
    render(<AssistantMarkdown content={'```html\n<script>alert("原文")</script>\n```'} />);
    await user.click(screen.getByRole('button', { name: '复制代码' }));
    expect(screen.getByRole('status')).toHaveTextContent('复制失败');
    await user.click(screen.getByRole('button', { name: '复制代码' }));
    expect(write).toHaveBeenLastCalledWith('<script>alert("原文")</script>\n');
    expect(screen.getByRole('status')).toHaveTextContent('代码已复制');
    expect(document.querySelector('script')).toBeNull();
  });

  it('keeps incomplete streaming text stable, then formats terminal content', async () => {
    const { rerender } = render(<AssistantContent streaming content={'**正在生成'} />);
    expect(screen.getByText('**正在生成')).toBeVisible();
    rerender(<AssistantContent streaming content={'**正在生成**\n\n```js\nconst x = 1;'} />);
    expect(screen.queryByRole('button', { name: '复制代码' })).not.toBeInTheDocument();
    rerender(<AssistantContent content={'**完成**\n\n```js\nconst x = 1;\n```'} />);
    expect(await screen.findByRole('button', { name: '复制代码' })).toBeVisible();
    expect(screen.getByText('完成').tagName).toBe('STRONG');
  });

  it('falls back to text above the parse limit without dropping source', () => {
    const content = '**' + 'x'.repeat(MAX_MARKDOWN_CHARACTERS);
    const { container } = render(<AssistantContent content={content} />);
    expect(container.firstChild).toHaveTextContent(content);
    expect(screen.getByText('长回复以纯文本显示')).toBeVisible();
    expect(container.querySelector('strong')).toBeNull();
  });
});
