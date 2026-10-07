import { Children, isValidElement, useState, type ReactNode } from 'react';
import Markdown, { type Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { t } from '@/lib/i18n';
import './markdown.css';

// Only deliberate external web navigation is supported. Relative links could
// navigate privileged console routes; images must never fetch remote content.
function safeLink(value: string): string | undefined {
  // eslint-disable-next-line no-control-regex -- Reject whitespace/control bytes before URL normalization.
  if (!/^https?:\/\//i.test(value) || /[\u0000-\u0020\u007f]/.test(value)) return undefined;
  try {
    const url = new URL(value);
    if (url.username || url.password) return undefined;
    return url.href;
  } catch {
    return undefined;
  }
}

function CodeBlock({ children }: { children?: ReactNode }) {
  const [copyState, setCopyState] = useState<'idle' | 'copied' | 'failed'>('idle');
  const code = Children.toArray(children)[0];
  const text = isValidElement<{ children?: string }>(code) ? code.props.children ?? '' : '';
  async function copy() {
    try {
      await navigator.clipboard.writeText(text);
      setCopyState('copied');
    } catch {
      setCopyState('failed');
    }
  }
  return <div className="playground-code">
    <div className="flex justify-end border-b border-border px-3 py-1.5">
      <button type="button" onClick={() => void copy()} className="text-xs text-muted-foreground underline underline-offset-2 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring">{t('复制代码')}</button>
    </div>
    <pre tabIndex={0}>{children}</pre>
    {copyState !== 'idle' ? <p role="status" className="px-3 pb-2 text-xs text-muted-foreground">{copyState === 'copied' ? t('代码已复制') : t('复制失败，请选择代码手动复制')}</p> : null}
  </div>;
}

const components: Components = {
  a: ({ href, children }) => href
    ? <a href={href} target="_blank" rel="noopener noreferrer nofollow">{children}</a>
    : <span>{children}</span>,
  img: ({ alt }) => <span className="text-muted-foreground">{t('图片：{alt}', { alt: alt || t('未提供说明') })}</span>,
  pre: ({ children }) => <CodeBlock>{children}</CodeBlock>,
  table: ({ children }) => <div className="playground-table" tabIndex={0} role="region" aria-label={t('回复表格')}><table>{children}</table></div>,
};
const allowedElements = ['p', 'br', 'hr', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'strong', 'em', 'del', 'blockquote', 'ul', 'ol', 'li', 'a', 'img', 'pre', 'code', 'table', 'thead', 'tbody', 'tr', 'th', 'td', 'input', 'sup'];

export default function AssistantMarkdown({ content }: { content: string }) {
  return <div className="playground-markdown">
    <Markdown skipHtml allowedElements={allowedElements} unwrapDisallowed remarkPlugins={[remarkGfm]} urlTransform={safeLink} components={components}>{content}</Markdown>
  </div>;
}
