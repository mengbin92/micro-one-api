import { lazy, memo, Suspense } from 'react';
import { t } from '@/lib/i18n';

const AssistantMarkdown = lazy(() => import('./AssistantMarkdown'));
// Parsing untrusted Markdown is more expensive than displaying text. Keep the
// existing 4 MiB conversation cap, but bound each Markdown parse separately.
export const MAX_MARKDOWN_CHARACTERS = 64 * 1024;

export const AssistantContent = memo(function AssistantContent({ content, streaming = false }: { content: string; streaming?: boolean }) {
  const plain = <div className="whitespace-pre-wrap break-words leading-7 [overflow-wrap:anywhere]">{content || (streaming ? '…' : '')}</div>;
  // Never parse incomplete fences/links on every SSE delta. Terminal states
  // (completed, stopped, failed) render their retained content once.
  if (streaming || !content) return plain;
  if (content.length > MAX_MARKDOWN_CHARACTERS) return <>{plain}<p className="mt-2 text-xs text-muted-foreground">{t('长回复以纯文本显示')}</p></>;
  return <Suspense fallback={plain}><AssistantMarkdown content={content} /></Suspense>;
});
