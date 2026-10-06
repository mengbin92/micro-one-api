import { useState } from 'react';
import type { IAMGrant, IAMPermission, IAMSource } from '@/lib/iam-types';
import { SourcesView } from '@/components/admin/IAMDetails';
import { Button } from '@/components/ui/button';
import { t } from '@/lib/i18n';
export function IAMPermissionEditor({ permissions, grants, sources, onChange }: { permissions: IAMPermission[]; grants: IAMGrant[]; sources?: IAMSource[]; onChange: (grants: IAMGrant[]) => void }) {
  const [view, setView] = useState<'tree' | 'matrix'>('tree');
  const [moduleFilter, setModuleFilter] = useState('');
  const visible = permissions.filter(p => !moduleFilter || p.code?.startsWith(moduleFilter + '.'));
  const [selected, setSelected] = useState('');
  const [selectedEffect, setSelectedEffect] = useState('allow');
  const [scope, setScope] = useState('');
  const [scopeError, setScopeError] = useState('');
  const resources = [...new Set(visible.map(p => (p.code ?? '').split('.').slice(0, -1).join('.')))];
  const actions = [...new Set(visible.map(p => (p.code ?? '').split('.').at(-1) ?? ''))];
  const modules = [...new Set(permissions.map(p => (p.code ?? '').split('.')[0]))];
  const effectValue = (code?: string) => ['allow', 'deny'].filter(effect => grants.some(g => g.operation === code && g.effect === effect)).join('+');
  const update = (code: string, effect: string) => {
    const next = grants.filter(g => g.operation !== code);
    for (const kind of effect.split('+').filter(Boolean)) next.push({ operation: code, effect: kind, scope: grants.find(g => g.operation === code && g.effect === kind)?.scope ?? { clauses: [{ all: true }] } });
    onChange(next);
  };
  const scopeButtons = (permission: IAMPermission) => grants.filter(g => g.operation === permission.code).map(grant => <Button key={grant.effect} variant="outline" onClick={() => { setSelected(permission.code!); setSelectedEffect(grant.effect!); setScope(JSON.stringify(grant.scope, null, 2)); setScopeError(''); }}>{effectValue(permission.code) === 'allow+deny' ? t(grant.effect === 'deny' ? '编辑拒绝范围' : '编辑允许范围') : t('编辑范围')}</Button>);
  const cell = (permission: IAMPermission) => {
    const direct = grants.find(g => g.operation === permission.code);
    const inherited = sources?.filter(s => s.operation === permission.code && (s.inheritance_path?.length ?? 0) > 1);
    return <div className="min-w-40 space-y-2 p-2"><p className="text-xs">{permission.name} · {permission.status} · {permission.binding}</p><select aria-label={`${permission.code} ${t('直接授权')}`} value={effectValue(permission.code)} disabled={permission.binding === 'unbound' || permission.status !== 'enabled'} onChange={e => update(permission.code!, e.target.value)}><option value="">{t('未授权')}</option><option value="allow">{t('允许')}</option><option value="deny">{t('拒绝')}</option><option value="allow+deny">{t('允许与局部拒绝')}</option></select>{inherited?.map((source, i) => <p className="text-xs" key={i}>{t('继承来源')} #{source.role_id} · {source.inheritance_path?.join(' → ')} · {source.effect}</p>)}{direct && <><p className="break-all text-xs">{JSON.stringify(direct.scope)}</p>{scopeButtons(permission)}</>}</div>;
  };
  const row = (permission: IAMPermission) => {
    const direct = grants.find(g => g.operation === permission.code);
    const inherited = sources?.filter(s => s.operation === permission.code && (s.inheritance_path?.length ?? 0) > 1);
    return <tr key={permission.code} className="border-b"><td className="p-2 font-mono text-xs">{permission.code}<p>{permission.name} · {permission.binding} · {permission.status}{permission.protected && ` · ${t('保护项')}`}</p></td><td className="p-2"><select aria-label={`${permission.code} ${t('直接授权')}`} value={effectValue(permission.code)} disabled={permission.binding === 'unbound' || permission.status !== 'enabled'} onChange={e => update(permission.code!, e.target.value)}><option value="">{t('未授权')}</option><option value="allow">{t('允许')}</option><option value="deny">{t('拒绝')}</option><option value="allow+deny">{t('允许与局部拒绝')}</option></select></td><td className="p-2">{inherited?.map((s, i) => <div key={i}>{t('继承')} #{s.role_id} · {(s.inheritance_path ?? []).join(' → ')} · {s.effect}</div>)}</td><td className="p-2">{direct && scopeButtons(permission)}</td></tr>;
  };
  return <div className="min-w-0 space-y-3"><div className="flex gap-2"><Button variant={view === 'tree' ? 'default' : 'outline'} onClick={() => setView('tree')}>{t('模块树')}</Button><Button variant={view === 'matrix' ? 'default' : 'outline'} onClick={() => setView('matrix')}>{t('资源 × 操作矩阵')}</Button></div>
    <p className="text-sm text-muted-foreground">{t('树与矩阵编辑同一组明确操作码。继承为只读来源，取消继承需修改来源或显式拒绝。')}</p>
    {view === 'matrix' ? <div className="min-w-0 space-y-2"><label>{t('模块')}<select aria-label={t('矩阵模块')} className="ml-2 rounded border p-2" value={moduleFilter} onChange={e => setModuleFilter(e.target.value)}><option value="">{t('全部')}</option>{modules.map(module => <option key={module}>{module}</option>)}</select></label><div className="max-h-[32rem] max-w-full overflow-auto rounded border"><table className="w-full"><caption className="p-2 text-left">{t('直接授权')} / {t('继承来源')} / {t('范围')}</caption><thead><tr><th className="p-2">{t('资源')}</th>{actions.map(action => <th key={action}>{action}</th>)}</tr></thead><tbody>{resources.map(resource => <tr className="border-t" key={resource}><th className="p-2 text-left font-mono text-xs">{resource}</th>{actions.map(action => { const permission = permissions.find(p => p.code === `${resource}.${action}`); return <td className="border-l align-top" key={action}>{permission ? cell(permission) : '—'}</td>; })}</tr>)}</tbody></table></div></div> : modules.map(module => <details open key={module}><summary>{module} <Button variant="outline" onClick={() => {
      const codes = permissions.filter(p => p.code?.startsWith(module + '.') && p.binding !== 'unbound' && p.status === 'enabled').map(p => p.code!);
      const next = grants.filter(g => !codes.includes(g.operation ?? '') || g.effect === 'deny');
      codes.forEach(code => next.push({ operation: code, effect: 'allow', scope: grants.find(g => g.operation === code && g.effect === 'allow')?.scope ?? { clauses: [{ all: true }] } })); onChange(next);
    }}>{t('允许当前子项')}</Button></summary><table className="w-full"><tbody>{permissions.filter(p => p.code?.startsWith(module + '.')).map(row)}</tbody></table></details>)}
    {selected && <fieldset className="rounded border p-3"><legend>{selected} · {selectedEffect} · {t('范围')}</legend><p className="text-sm">{t('支持的范围')} {(permissions.find(p => p.code === selected)?.supported_scopes ?? []).join(', ')}</p><textarea aria-label={t('范围描述')} className="min-h-28 w-full rounded border p-2 font-mono text-xs" value={scope} onChange={e => setScope(e.target.value)} /><Button onClick={() => { try { const parsed = JSON.parse(scope); if (!Array.isArray(parsed.clauses)) throw new Error(); onChange(grants.map(g => g.operation === selected && g.effect === selectedEffect ? { ...g, scope: parsed } : g)); setSelected(''); } catch { setScopeError(t('请输入包含 clauses 的合法范围')); } }}>{t('应用范围')}</Button>{scopeError && <p role="alert">{scopeError}</p>}</fieldset>}
    <details><summary>{t('服务端授权来源')}</summary><SourcesView sources={sources} /></details>
  </div>;
}
