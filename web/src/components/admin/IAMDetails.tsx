import type { IAMReply, IAMSource, IAMScope } from '@/lib/iam-types';
import { t } from '@/lib/i18n';
export function ScopeView({ scope }: { scope?: IAMScope }) {
  if (!scope?.clauses?.length) return <span>{t('空范围')}</span>;
  return <code className="break-all text-xs">{JSON.stringify(scope)}</code>;
}
export function SourcesView({ sources }: { sources?: IAMSource[] }) {
  return <div className="space-y-2">{sources?.map((source, i) => <div className="rounded border p-3 text-sm" key={i}>
    <strong>{source.operation}</strong> · {String(source.effect) === 'AUTHORIZATION_EFFECT_DENY' || source.effect === 2 ? t('强制拒绝') : source.active ? t('激活允许') : t('未激活')}
    <p>{t('分配')} #{source.assignment_id} · {t('角色')} #{source.role_id} · {t('继承路径')} {(source.inheritance_path ?? []).join(' → ')}</p>
    <p>{t('角色范围')} <ScopeView scope={source.role_scope} /></p><p>{t('分配边界')} <ScopeView scope={source.assignment_boundary} /></p>
    <p>{source.validity?.starts_at} → {source.validity?.expires_at ?? t('无限期')}</p>
  </div>)}</div>;
}
export function PreviewView({ reply }: { reply: IAMReply }) {
  return <div className="space-y-3 rounded border p-4" role="status">
    <p>{t('策略版本')} {reply.base_policy_revision} · {t('内容摘要')} <code className="break-all text-xs">{reply.content_digest}</code></p>
    <p>{t('受影响用户')} {(reply.affected_user_ids ?? []).join(', ') || '—'}</p>
    {(reply.conflicts ?? []).map((c, i) => <div key={i} className="rounded bg-destructive/10 p-3" role="alert">{c.constraint_name || c.kind} · {t('角色')} {(c.role_ids ?? []).join(', ')} · {t('用户')} {(c.user_ids ?? []).join(', ')} · {t('会话')} {(c.session_ids ?? []).join(', ')} · {c.actual}/{c.limit}<p>{c.validity?.starts_at} → {c.validity?.expires_at ?? t('无限期')}</p></div>)}
    {(reply.impacts ?? []).map(impact => <details key={impact.user_id}><summary>{t('用户')} #{impact.user_id} · {t('修改前后差异')}</summary><p>{t('修改前')}</p><SourcesView sources={impact.before} /><p>{t('修改后')}</p><SourcesView sources={impact.after} /></details>)}
  </div>;
}
