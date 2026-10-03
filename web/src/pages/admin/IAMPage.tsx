import { useState } from 'react';
import { Link, useLocation, useSearchParams } from 'react-router';
import { Button } from '@/components/ui/button';
import { IAMChangeDialog, type IAMChange } from '@/components/admin/IAMChangeDialog';
import { IAMPermissionEditor } from '@/components/admin/IAMPermissionEditor';
import { ScopeView, SourcesView, PreviewView } from '@/components/admin/IAMDetails';
import { useAuthorization, useAuthorizedQuery } from '@/lib/authorization';
import { readIAM, readAllIAM, writeIAM } from '@/lib/iam-api';
import { menuRoutes, menuIcons, adminPages } from '@/lib/admin-permissions';
import { platformContext, type IAMReply, type IAMRequest, type IAMRole, type IAMGrant, type IAMAssignment, type IAMPermission, type IAMDelegation, type IAMConstraint, type IAMMenu } from '@/lib/iam-types';
import { getApiErrorMessage } from '@/lib/api-error';
import { t } from '@/lib/i18n';

type Section = 'permissions' | 'roles' | 'assignments' | 'delegations' | 'constraints' | 'menus' | 'explain' | 'audits';
const sections: Record<Section, { title: string; path: string; permission: string }> = {
  permissions: { title: '权限目录', path: '/permissions', permission: 'iam.permission.list' },
  roles: { title: '角色与继承', path: '/roles', permission: 'iam.role.list' },
  assignments: { title: '用户角色', path: '', permission: 'identity.user_role.read' },
  delegations: { title: '授权委派', path: '/delegations', permission: 'iam.delegation.read' },
  constraints: { title: '职责分离', path: '/constraints', permission: 'iam.constraint.read' },
  menus: { title: '菜单管理', path: '/menus', permission: 'iam.menu.list' },
  explain: { title: '有效权限与模拟', path: '', permission: 'iam.authorization.explain' },
  audits: { title: '授权审计', path: '/audit-events', permission: 'iam.audit.read' },
};
const ids = (value: string) => value.split(/[,\s]+/).filter(Boolean);
const allScope = { clauses: [{ all: true }] };
function TextField({ label, value, onChange, multiline = false }: { label: string; value?: string; onChange: (value: string) => void; multiline?: boolean }) {
  return <label className="block space-y-1 text-sm"><span>{t(label)}</span>{multiline ? <textarea aria-label={t(label)} className="min-h-24 w-full rounded border p-2 font-mono" value={value ?? ''} onChange={e => onChange(e.target.value)} /> : <input aria-label={t(label)} className="w-full rounded border p-2" value={value ?? ''} onChange={e => onChange(e.target.value)} />}</label>;
}
function JsonField<T>({ label, value, onChange }: { label: string; value: T; onChange: (value: T) => void }) {
  const [text, setText] = useState(JSON.stringify(value, null, 2));
  const [error, setError] = useState(false);
  return <div><TextField label={label} value={text} multiline onChange={next => { setText(next); try { onChange(JSON.parse(next)); setError(false); } catch { setError(true); } }} />{error && <p role="alert">{t('JSON 格式无效，未应用此修改')}</p>}</div>;
}
export function IAMPage() {
  const location = useLocation();
  const section = location.pathname.split('/').pop() as Section;
  return <IAMWorkspace key={section} section={section in sections ? section : 'roles'} />;
}
function IAMWorkspace({ section }: { section: Section }) {
  const auth = useAuthorization();
  const [params] = useSearchParams();
  const [userInput, setUserInput] = useState(params.get('user_id') ?? '');
  const [userId, setUserId] = useState(params.get('user_id') ?? '');
  const [filterInput, setFilterInput] = useState('');
  const [filter, setFilter] = useState('');
  const [pages, setPages] = useState(['']);
  const [selected, setSelected] = useState('');
  const [change, setChange] = useState<IAMChange>();
  const config = sections[section];
  const path = section === 'assignments' ? `/users/${userId}/roles` : config.path;
  const query = useAuthorizedQuery<IAMReply>({ permission: config.permission, queryKey: ['iam', section, userId, filter, pages.at(-1)], enabled: !!path && (section !== 'assignments' || /^[1-9]\d*$/.test(userId)), queryFn: ({ signal }) => readIAM(path, { filter, page_size: 50, page_token: pages.at(-1) }, signal) });
  const roles = useAuthorizedQuery<IAMReply>({ permission: 'iam.role.list', queryKey: ['iam', 'role-options'], enabled: section !== 'roles', queryFn: ({ signal }) => readAllIAM('/roles', 'roles', {}, signal) });
  const permissions = useAuthorizedQuery<IAMReply>({ permission: 'iam.permission.list', queryKey: ['iam', 'permission-options'], enabled: section === 'roles', queryFn: ({ signal }) => readAllIAM('/permissions', 'permissions', {}, signal) });
  const grantDetail = useAuthorizedQuery<IAMReply>({ permission: 'iam.role.permissions.read', queryKey: ['iam', 'role-grants', selected], enabled: section === 'roles' && !!selected, queryFn: ({ signal }) => readIAM(`/roles/${selected}/permissions`, {}, signal) });
  const references = useAuthorizedQuery<IAMReply>({ permission: section === 'roles' ? 'iam.role.read' : 'iam.permission.references.read', queryKey: ['iam', section, 'references', selected], enabled: !!selected && (section === 'roles' || section === 'permissions'), queryFn: ({ signal }) => readIAM(`/${section}/${selected}/references`, {}, signal) });
  const members = useAuthorizedQuery<IAMReply>({ permission: 'iam.role.members.read', queryKey: ['iam', 'members', selected], enabled: section === 'roles' && !!selected, queryFn: ({ signal }) => readIAM(`/roles/${selected}/members`, {}, signal) });
  const sessions = useAuthorizedQuery<IAMReply>({ permission: 'identity.user.sessions.revoke', queryKey: ['iam', 'sessions', userId], enabled: section === 'assignments' && !!userId, queryFn: ({ signal }) => readIAM(`/users/${userId}/sessions`, {}, signal) });
  const prepare = (draft: IAMChange) => setChange(draft);
  const role = query.data?.roles?.find(item => item.id === selected);
  const permission = query.data?.permissions?.find(item => item.id === selected);
  const delegation = query.data?.delegations?.find(item => item.id === selected);
  const constraint = query.data?.constraints?.find(item => item.id === selected);
  const menu = query.data?.menus?.find(item => item.id === selected);
  const selectedAudit = query.data?.audits?.find(item => item.event_id === selected);
  const audit = useAuthorizedQuery<IAMReply>({ permission: 'iam.audit.read', queryKey: ['iam', 'audit', selected], enabled: section === 'audits' && !!selected, queryFn: ({ signal }) => readIAM(`/audit-events/${selected}`, {}, signal) });
  const rows = (query.data?.[section === 'assignments' ? 'assignments' : section === 'audits' ? 'audits' : section === 'explain' ? 'sources' : section] ?? []) as Array<{ id?: string; event_id?: string; code?: string; name?: string; status?: string; revision?: string; user_id?: string; role_id?: string; target_kind?: string; action?: string; result?: string; reason?: string; binding?: string; protected?: boolean; origin?: string }>;
  return <div className="space-y-5"><h2 className="text-2xl font-semibold">{t(config.title)}</h2><p className="text-sm text-muted-foreground">{t('只显示服务端管理范围内的对象。写入前必须预检，服务端提交时再次验证。')}</p>
    <nav className="flex flex-wrap gap-3">{Object.entries(sections).filter(([key]) => auth.can(adminPages[`/admin/iam/${key}`])).map(([key, item]) => <Link key={key} to={`/admin/iam/${key}`} className="underline">{t(item.title)}</Link>)}</nav>
    {section === 'assignments' || section === 'explain' ? <div className="flex items-end gap-3"><TextField label="用户 ID" value={userInput} onChange={setUserInput} /><Button onClick={() => { setUserId(userInput); setPages(['']); }}>{t('查询用户')}</Button></div> : <div className="flex items-end gap-3"><TextField label="筛选表达式" value={filterInput} onChange={setFilterInput} /><Button onClick={() => { setFilter(filterInput); setPages(['']); }}>{t('查询')}</Button></div>}
    {section === 'explain' ? <Explanation userId={userId} /> : <>
    {query.isLoading && <p role="status">{t('加载中...')}</p>}{query.isError && <p role="alert">{getApiErrorMessage(query.error)}</p>}{[grantDetail, references, members, sessions, audit].filter(result => result.isError).map((result, i) => <p key={i} role="alert">{getApiErrorMessage(result.error)}</p>)}
    <div className="overflow-x-auto rounded border"><table className="w-full text-left text-sm"><thead><tr><th className="p-3">ID</th><th>{t('名称 / 操作')}</th><th>{t('状态 / 来源')}</th><th>{t('版本')}</th><th>{t('操作')}</th></tr></thead><tbody>{rows.map((item, i) => <tr key={item.id ?? item.event_id ?? i} className="border-t"><td className="p-3 font-mono">{item.id ?? item.event_id}</td><td>{item.name ?? item.code ?? item.action ?? `user #${item.user_id} / role #${item.role_id}`}</td><td>{item.status ?? item.origin ?? item.target_kind ?? item.result} {item.binding} {item.protected && t('保护项')}</td><td>{item.revision}</td><td><Button variant="outline" onClick={() => setSelected(item.id ?? item.event_id ?? '')}>{t('详情')}</Button>{section === 'assignments' && auth.can('identity.user_role.revoke') && <Button variant="destructive" onClick={() => prepare({ title: t('撤销角色'), path: `/users/${userId}/roles/${item.id}`, method: 'delete', rpc: 'RevokeUserRole', permission: 'identity.user_role.revoke', request: { id: item.id, user_id: userId, expected_revision: item.revision } })}>{t('撤销')}</Button>}</td></tr>)}</tbody></table></div>
    <div className="flex items-center gap-3"><span>{t('总数')} {query.data?.total ?? '0'}</span><Button disabled={pages.length < 2} onClick={() => setPages(pages.slice(0, -1))}>{t('上一页')}</Button><Button disabled={!query.data?.next_page_token} onClick={() => setPages([...pages, query.data!.next_page_token!])}>{t('下一页')}</Button></div>
    </>}
    {section === 'roles' && <RoleEditor key={`${selected}:${grantDetail.data?.roles?.[0]?.revision ?? 'loading'}`} role={grantDetail.data?.roles?.[0] ?? role} roles={query.data?.roles ?? []} permissions={permissions.data?.permissions ?? []} detail={grantDetail.data} prepare={prepare} />}
    {section === 'permissions' && <PermissionEditor key={selected} item={permission} prepare={prepare} />}
    {section === 'assignments' && userId && <AssignmentEditor userId={userId} roles={roles.data?.roles ?? []} assignments={query.data?.assignments ?? []} prepare={prepare} />}
    {section === 'delegations' && <DelegationEditor key={selected} item={delegation} roles={roles.data?.roles ?? []} prepare={prepare} />}
    {section === 'constraints' && <ConstraintEditor key={selected} item={constraint} roles={roles.data?.roles ?? []} prepare={prepare} />}
    {section === 'menus' && <MenuEditor key={selected} item={menu} prepare={prepare} />}
    {section === 'audits' && <><AuditExport filter={filter} />{selectedAudit && <pre className="overflow-auto rounded border p-3 text-xs">{JSON.stringify(audit.data?.audits?.[0] ?? selectedAudit, null, 2)}</pre>}</>}
    {!!references.data?.references?.length && <details open><summary>{t('引用关系')}</summary><pre>{JSON.stringify(references.data.references, null, 2)}</pre></details>}
    {!!members.data?.assignments?.length && <details><summary>{t('角色成员')}</summary><pre>{JSON.stringify(members.data.assignments, null, 2)}</pre></details>}
    {section === 'assignments' && userId && auth.can('identity.user.sessions.revoke') && sessions.data && <RevokeSessions userId={userId} reply={sessions.data} refresh={() => sessions.refetch()} />}
    {!!sessions.data?.sessions?.length && <details><summary>{t('用户会话')}</summary><pre>{JSON.stringify(sessions.data.sessions, null, 2)}</pre></details>}
    {section === 'assignments' && selected && <pre className="overflow-auto rounded border p-3 text-xs">{JSON.stringify(query.data?.assignments?.find(a => a.id === selected), null, 2)}</pre>}
    {change && <IAMChangeDialog change={change} onClose={() => setChange(undefined)} />}
  </div>;
}
type Prepare = (change: IAMChange) => void;
function RoleEditor({ role, roles, permissions, detail, prepare }: { role?: IAMRole; roles: IAMRole[]; permissions: IAMPermission[]; detail?: IAMReply; prepare: Prepare }) {
  const auth = useAuthorization();
  const [name, setName] = useState(role?.name ?? '');
  const [code, setCode] = useState('');
  const [description, setDescription] = useState(role?.description ?? '');
  const [maxMembers, setMaxMembers] = useState(role?.max_members ?? '');
  const [grants, setGrants] = useState<IAMGrant[]>(role?.grants ?? []);
  const [inheritance, setInheritance] = useState<string[]>(role?.inherits ?? []);
  const [copyCode, setCopyCode] = useState('');
  const id = role?.id;
  const change = (rpc: string, path: string, permission: string, request: IAMRequest, method: IAMChange['method'] = 'post') => prepare({ title: t('角色变更'), rpc, path, permission, method, request: { id, expected_revision: role?.revision ?? '0', ...request } });
  return <section className="min-w-0 space-y-4 rounded border p-5"><h3 className="text-lg font-semibold">{id ? `${t('角色')} #${id} · ${role?.code} · ${role?.status} · revision ${role?.revision}` : t('创建草稿角色')}</h3>
    {!id && <TextField label="角色代码" value={code} onChange={setCode} />}<TextField label="名称" value={name} onChange={setName} /><TextField label="说明" value={description} onChange={setDescription} /><TextField label="成员上限（留空不限）" value={maxMembers} onChange={setMaxMembers} />
    {auth.can(id ? 'iam.role.update' : 'iam.role.create') && <Button disabled={!name || (!id && !code) || role?.code === 'root'} onClick={() => change(id ? 'UpdateRole' : 'CreateRole', id ? `/roles/${id}` : '/roles', id ? 'iam.role.update' : 'iam.role.create', { role: { context: platformContext, code: id ? role?.code : code, name, description, status: 'draft', ...(maxMembers ? { max_members: maxMembers } : {}) }, ...(id ? { update_mask: 'name,description,max_members' } : {}) }, id ? 'patch' : 'post')}>{t('保存角色资料')}</Button>}
    {id && <><div className="flex flex-wrap gap-2">{(['enabled', 'disabled'] as const).map(status => auth.can(`iam.role.${status === 'enabled' ? 'enable' : 'disable'}`) && <Button key={status} disabled={role?.code === 'root'} onClick={() => change('SetRoleStatus', `/roles/${id}/status`, `iam.role.${status === 'enabled' ? 'enable' : 'disable'}`, { role: { status } })}>{t(status === 'enabled' ? '启用' : '停用')}</Button>)}{auth.can('iam.role.archive') && <Button disabled={role?.code === 'root'} variant="destructive" onClick={() => change('ArchiveRole', `/roles/${id}`, 'iam.role.archive', {}, 'delete')}>{t('归档')}</Button>}</div>
    {auth.can('iam.role.copy') && <div className="flex items-end gap-2"><TextField label="复制后的角色代码" value={copyCode} onChange={setCopyCode} /><Button disabled={!copyCode} onClick={() => change('CopyRole', `/roles/${id}/copy`, 'iam.role.copy', { id: undefined, expected_revision: '0', source_id: id, role: { code: copyCode, name: `${name} (${t('副本')})`, context: platformContext, status: 'draft' } })}>{t('复制为草稿')}</Button><span className="text-sm">{t('复制不包含成员、会话或委派 authority')}</span></div>}
    {auth.can('iam.role.permissions.read') && <fieldset className="min-w-0" disabled={!auth.can('iam.role.permissions.update') || role?.code === 'root'}><IAMPermissionEditor permissions={permissions} grants={grants} sources={detail?.sources} onChange={setGrants} /></fieldset>}
    {auth.can('iam.role.permissions.update') && <Button disabled={role?.code === 'root' || !detail?.roles?.length} onClick={() => change('UpdateRolePermissions', `/roles/${id}/permissions`, 'iam.role.permissions.update', { grants }, 'put')}>{t('预检并保存授权')}</Button>}
    <fieldset className="space-y-2"><legend>{t('角色继承（senior → junior）')}</legend>{roles.filter(r => r.id !== id).map(r => <label key={r.id} className="flex gap-3"><input type="checkbox" checked={inheritance.includes(r.id!)} disabled={!auth.can('iam.role.hierarchy.update')} onChange={e => setInheritance(e.target.checked ? [...inheritance, r.id!] : inheritance.filter(value => value !== r.id))} />{r.name} · #{r.id} · {r.status}</label>)}<div role="img" aria-label={t('角色继承图')} className="flex flex-wrap items-center gap-3 rounded border p-3"><span className="rounded bg-muted p-2">#{id} · {name}</span><span>→</span>{inheritance.length ? inheritance.map(junior => <span key={junior} className="rounded border p-2">#{junior} · {roles.find(r => r.id === junior)?.name}</span>) : <span>∅</span>}</div>{auth.can('iam.role.hierarchy.update') && <Button disabled={role?.code === 'root'} onClick={() => change('UpdateRoleInheritance', `/roles/${id}/inheritance`, 'iam.role.hierarchy.update', { role_ids: inheritance }, 'put')}>{t('预检并保存继承')}</Button>}</fieldset>
    </>}
  </section>;
}
function PermissionEditor({ item, prepare }: { item?: IAMPermission; prepare: Prepare }) {
  const auth = useAuthorization();
  const [name, setName] = useState(item?.name ?? '');
  const [code, setCode] = useState(item?.code ?? '');
  const [category, setCategory] = useState(item?.category ?? '');
  const [risk, setRisk] = useState(item?.risk_level ?? 'normal');
  const [resourceId, setResourceId] = useState(item?.resource_id ?? '');
  const change = (rpc: string, path: string, permission: string, request: IAMRequest, method: IAMChange['method'] = 'post') => prepare({ title: t('目录变更'), rpc, path, permission, method, request: { id: item?.id, expected_revision: item?.revision ?? '0', ...request } });
  return <section className="space-y-3 rounded border p-5"><h3>{item ? `${item.code} · ${item.binding} · ${item.status}` : t('创建目录草稿')}</h3><p>{t('目录元数据不能增加执行点或扩大固定范围')}</p><TextField label="操作代码" value={code} onChange={setCode} /><TextField label="资源 ID" value={resourceId} onChange={setResourceId} /><TextField label="名称" value={name} onChange={setName} /><TextField label="分类" value={category} onChange={setCategory} /><TextField label="风险级别" value={risk} onChange={setRisk} /><p>{t('支持的范围')} {(item?.supported_scopes ?? []).join(', ')} · {t('保护项')} {String(!!item?.protected)}</p>
    {auth.can(item ? 'iam.permission.metadata.update' : 'iam.permission.create') && <Button disabled={!name || !code || !resourceId} onClick={() => change(item ? 'UpdatePermission' : 'CreatePermission', item ? `/permissions/${item.id}` : '/permissions', item ? 'iam.permission.metadata.update' : 'iam.permission.create', { permission: { ...item, name, code, resource_id: resourceId, category, risk_level: risk, status: item?.status ?? 'draft' }, ...(item ? { update_mask: 'name,category,risk_level' } : {}) }, item ? 'patch' : 'post')}>{t('保存目录资料')}</Button>}
    {item && <div className="flex gap-2">{(['enabled', 'disabled'] as const).map(status => auth.can(`iam.permission.${status === 'enabled' ? 'enable' : 'disable'}`) && <Button key={status} disabled={!!item.protected && status === 'disabled'} onClick={() => change('SetPermissionStatus', `/permissions/${item.id}/status`, `iam.permission.${status === 'enabled' ? 'enable' : 'disable'}`, { permission: { status } })}>{t(status === 'enabled' ? '启用' : '停用')}</Button>)}{auth.can('iam.permission.archive') && <Button disabled={item.protected} onClick={() => change('ArchivePermission', `/permissions/${item.id}`, 'iam.permission.archive', {}, 'delete')}>{t('归档')}</Button>}</div>}
  </section>;
}
function AssignmentEditor({ userId, roles, assignments, prepare }: { userId: string; roles: IAMRole[]; assignments: IAMAssignment[]; prepare: Prepare }) {
  const auth = useAuthorization();
  const [roleId, setRoleId] = useState('');
  const [start, setStart] = useState('');
  const [expires, setExpires] = useState('');
  const [boundary, setBoundary] = useState(allScope);
  const [batch, setBatch] = useState<IAMAssignment[]>([]);
  const assignment = { user_id: userId, role_id: roleId, context: platformContext, boundary, validity: { starts_at: start ? start : new Date().toISOString(), ...(expires ? { expires_at: expires } : {}) } };
  return <section className="space-y-3 rounded border p-5"><h3>{t('分配多个角色')}</h3><select aria-label={t('角色')} value={roleId} onChange={e => setRoleId(e.target.value)}><option value="">{t('选择角色')}</option>{roles.filter(r => r.code !== 'root').map(r => <option key={r.id} value={r.id}>{r.name} · #{r.id} · {r.status}</option>)}</select><TextField label="开始时间（ISO 8601）" value={start} onChange={setStart} /><TextField label="到期时间（留空无限期）" value={expires} onChange={setExpires} /><JsonField label="分配范围边界" value={boundary} onChange={setBoundary} />
    {auth.can('identity.user_role.assign') && <Button disabled={!roleId} onClick={() => prepare({ title: t('分配角色'), rpc: 'AssignUserRole', path: `/users/${userId}/roles`, permission: 'identity.user_role.assign', method: 'post', request: { user_id: userId, expected_revision: '0', assignment } })}>{t('预检分配')}</Button>}
    {auth.can('identity.user_role.batch_assign') && <><Button onClick={() => setBatch([...batch, assignment])} disabled={!roleId}>{t('添加到批量分配')}</Button><JsonField key={batch.length} label="批量分配（用户 ID、角色 ID、期限与边界）" value={batch} onChange={setBatch} /><Button disabled={!batch.length} onClick={() => prepare({ title: t('批量分配'), rpc: 'BatchAssignUserRoles', path: '/users/roles:batchAssign', permission: 'identity.user_role.batch_assign', method: 'post', request: { assignments: batch, expected_revision: '0' } })}>{t('整批预检')}</Button></>}
    {assignments.map(a => <details key={a.id}><summary>#{a.role_id} · {a.origin} · {a.revoked ? t('已撤销') : t('有效分配')} · {a.validity?.starts_at} → {a.validity?.expires_at ?? t('无限期')}</summary><ScopeView scope={a.boundary} /><p>{t('分配者')} #{a.assigned_by} · revision {a.revision}</p></details>)}
  </section>;
}
function DelegationEditor({ item, roles, prepare }: { item?: IAMDelegation; roles: IAMRole[]; prepare: Prepare }) {
  const auth = useAuthorization();
  const [draft, setDraft] = useState<IAMDelegation>(item ?? { context: platformContext, target_kind: 'role', actions: ['identity.user_role.assign', 'identity.user_role.revoke'], target_user_scope: allScope, grant_ceiling: [], validity: { starts_at: new Date().toISOString() }, can_redelegate: false });
  const patch = (value: Partial<IAMDelegation>) => setDraft({ ...draft, ...value });
  return <section className="space-y-3 rounded border p-5"><h3>{t('委派边界与授权上限')}</h3><label>{t('委派类型')}<select value={draft.target_kind} onChange={e => patch({ target_kind: e.target.value, target_role_id: e.target.value === 'role' ? undefined : '0', actions: e.target.value === 'user_credentials' ? ['identity.user.credential.update'] : e.target.value === 'role_creation' ? ['iam.role.create'] : ['identity.user_role.assign', 'identity.user_role.revoke'] })}>{['role', 'role_creation', 'user_credentials'].map(kind => <option key={kind}>{kind}</option>)}</select></label>{(['manager_role_id', 'target_role_id'] as const).filter(key => key !== 'target_role_id' || draft.target_kind === 'role').map(key => <label className="block" key={key}>{t(key === 'manager_role_id' ? '管理者角色' : '目标角色')}<select value={draft[key] ?? ''} onChange={e => patch({ [key]: e.target.value })}><option value="">{t('选择角色')}</option>{roles.map(r => <option key={r.id} value={r.id}>{r.name} · #{r.id}</option>)}</select></label>)}<TextField label="委派动作（逗号分隔）" value={draft.actions?.join(', ')} onChange={value => patch({ actions: ids(value) })} /><JsonField label="目标用户范围" value={draft.target_user_scope} onChange={value => patch({ target_user_scope: value })} /><JsonField label="权限与范围 ceiling" value={draft.grant_ceiling} onChange={value => patch({ grant_ceiling: value })} /><TextField label="开始时间（ISO 8601）" value={draft.validity?.starts_at} onChange={value => patch({ validity: { ...draft.validity, starts_at: value } })} /><TextField label="到期时间（留空无限期）" value={draft.validity?.expires_at} onChange={value => patch({ validity: { ...draft.validity, expires_at: value || undefined } })} /><label className="flex gap-2"><input type="checkbox" checked={draft.can_redelegate ?? false} onChange={e => patch({ can_redelegate: e.target.checked })} />{t('允许再委派')}</label>
    {auth.can(item ? 'iam.delegation.update' : 'iam.delegation.create') && <Button onClick={() => prepare({ title: t('保存委派'), rpc: item ? 'UpdateDelegation' : 'CreateDelegation', path: item ? `/delegations/${item.id}` : '/delegations', method: item ? 'patch' : 'post', permission: item ? 'iam.delegation.update' : 'iam.delegation.create', request: { id: item?.id, expected_revision: item?.revision ?? '0', delegation: draft, ...(item ? { update_mask: 'actions,target_user_scope,grant_ceiling,can_redelegate,validity' } : {}) } })}>{t('预检委派')}</Button>}{item && auth.can('iam.delegation.revoke') && <Button onClick={() => prepare({ title: t('撤销委派'), rpc: 'RevokeDelegation', path: `/delegations/${item.id}`, method: 'delete', permission: 'iam.delegation.revoke', request: { id: item.id, expected_revision: item.revision } })}>{t('撤销')}</Button>}
    <p className="text-sm">{t('撤销或到期停止后续管理，不移除已有业务分配。创建来源由服务器记录。')}</p>
  </section>;
}
function ConstraintEditor({ item, roles, prepare }: { item?: IAMConstraint; roles: IAMRole[]; prepare: Prepare }) {
  const auth = useAuthorization();
  const [draft, setDraft] = useState<IAMConstraint>(item ?? { context: platformContext, kind: 'SSD', enabled: true, max_count: '1', role_ids: [] });
  return <section className="space-y-3 rounded border p-5"><h3>{t('职责分离与未来基数')}</h3><TextField label="名称" value={draft.name} onChange={name => setDraft({ ...draft, name })} /><select aria-label={t('约束类型')} value={draft.kind} onChange={e => setDraft({ ...draft, kind: e.target.value })}><option value="SSD">SSD</option><option value="DSD">DSD</option></select><TextField label="最大角色数" value={draft.max_count} onChange={max_count => setDraft({ ...draft, max_count })} />{roles.map(r => <label className="flex gap-3" key={r.id}><input type="checkbox" checked={draft.role_ids?.includes(r.id!) ?? false} onChange={e => setDraft({ ...draft, role_ids: e.target.checked ? [...draft.role_ids ?? [], r.id!] : draft.role_ids?.filter(id => id !== r.id) })} />{r.name} · #{r.id}</label>)}<label className="flex gap-2"><input type="checkbox" checked={draft.enabled ?? false} onChange={e => setDraft({ ...draft, enabled: e.target.checked })} />{t('启用')}</label>{auth.can(item ? 'iam.constraint.update' : 'iam.constraint.create') && <Button onClick={() => prepare({ title: t('保存约束'), rpc: item ? 'UpdateRoleConstraint' : 'CreateRoleConstraint', path: item ? `/constraints/${item.id}` : '/constraints', method: item ? 'put' : 'post', permission: item ? 'iam.constraint.update' : 'iam.constraint.create', request: { id: item?.id, expected_revision: item?.revision ?? '0', constraint: draft, ...(item ? { update_mask: 'name,role_ids,max_count,enabled' } : {}) } })}>{t('预检未来冲突')}</Button>}{item && auth.can('iam.constraint.delete') && <Button onClick={() => prepare({ title: t('删除约束'), rpc: 'DeleteRoleConstraint', path: `/constraints/${item.id}`, method: 'delete', permission: 'iam.constraint.delete', request: { id: item.id, expected_revision: item.revision } })}>{t('删除')}</Button>}</section>;
}
function MenuEditor({ item, prepare }: { item?: IAMMenu; prepare: Prepare }) {
  const auth = useAuthorization();
  const [draft, setDraft] = useState<IAMMenu>(item ?? { enabled: true, route_key: 'overview', icon_key: 'home', sort: 0, parent_id: '0', required_all: [], required_any: [] });
  const patch = (value: Partial<IAMMenu>) => setDraft({ ...draft, ...value });
  return <section className="space-y-3 rounded border p-5"><h3>{t('菜单映射')}</h3><TextField label="名称" value={draft.name} onChange={name => patch({ name })} /><TextField label="父菜单 ID" value={draft.parent_id} onChange={parent_id => patch({ parent_id })} /><label>{t('注册路由')}<select value={draft.route_key} onChange={e => patch({ route_key: e.target.value })}>{Object.keys(menuRoutes).map(key => <option key={key}>{key}</option>)}</select></label><label>{t('图标')}<select value={draft.icon_key} onChange={e => patch({ icon_key: e.target.value })}>{menuIcons.map(key => <option key={key}>{key}</option>)}</select></label><TextField label="排序" value={String(draft.sort)} onChange={value => patch({ sort: Number(value) })} /><TextField label="全部必需操作" value={draft.required_all?.join(', ')} onChange={value => patch({ required_all: ids(value) })} /><TextField label="任一必需操作" value={draft.required_any?.join(', ')} onChange={value => patch({ required_any: ids(value) })} /><label><input type="checkbox" checked={draft.enabled ?? false} onChange={e => patch({ enabled: e.target.checked })} />{t('启用')}</label>{auth.can(item ? 'iam.menu.update' : 'iam.menu.create') && <Button onClick={() => prepare({ title: t('保存菜单'), rpc: item ? 'UpdateMenuItem' : 'CreateMenuItem', path: item ? `/menus/${item.id}` : '/menus', method: item ? 'patch' : 'post', permission: item ? 'iam.menu.update' : 'iam.menu.create', request: { id: item?.id, expected_revision: item?.revision ?? '0', menu: draft, ...(item ? { update_mask: 'name,parent_id,route_key,icon_key,sort,enabled,required_all,required_any' } : {}) } })}>{t('预检菜单')}</Button>}{item && auth.can('iam.menu.archive') && <Button onClick={() => prepare({ title: t('归档菜单'), rpc: 'ArchiveMenuItem', path: `/menus/${item.id}`, method: 'delete', permission: 'iam.menu.archive', request: { id: item.id, expected_revision: item.revision } })}>{t('归档')}</Button>}</section>;
}
function Explanation({ userId }: { userId: string }) {
  const auth = useAuthorization();
  const [operation, setOperation] = useState('');
  const [resourceId, setResourceId] = useState('');
  const [ownerId, setOwnerId] = useState('');
  const [groupIds, setGroupIds] = useState('');
  const [result, setResult] = useState<IAMReply>();
  const [error, setError] = useState('');
  const query = useAuthorizedQuery<IAMReply>({ permission: 'iam.authorization.user.read', queryKey: ['iam', 'effective', userId], enabled: /^[1-9]\d*$/.test(userId), queryFn: ({ signal }) => readIAM(`/users/${userId}/effective-permissions`, {}, signal) });
  return <section className="space-y-3 rounded border p-5"><TextField label="操作代码" value={operation} onChange={setOperation} /><TextField label="对象 ID" value={resourceId} onChange={setResourceId} /><TextField label="归属用户 ID" value={ownerId} onChange={setOwnerId} /><TextField label="路由组 ID（逗号分隔）" value={groupIds} onChange={setGroupIds} /><p className="text-sm">{t('模拟对象事实仅用于解释，不构成业务执行凭证。')}</p><Button disabled={!userId || !operation || !auth.can('iam.authorization.explain')} onClick={() => { setError(''); void writeIAM('/authorization:explain', 'post', { user_id: userId, operation, object: { context: platformContext, resource_id: resourceId || '0', owner_user_id: ownerId || '0', routing_group_ids: ids(groupIds) } }).then(setResult).catch(err => setError(getApiErrorMessage(err))); }}>{t('解释权限')}</Button>{error && <p role="alert">{error}</p>}{query.isError && <p role="alert">{getApiErrorMessage(query.error)}</p>}{result?.decision && <p role="status">{result.decision.allowed ? t('允许') : t('拒绝')} · {result.decision.reason} · {t('策略版本')} {result.versions?.policy_revision}</p>}<SourcesView sources={result?.sources ?? query.data?.sources} />{result?.impacts && <PreviewView reply={result} />}</section>;
}
function AuditExport({ filter }: { filter: string }) {
  const auth = useAuthorization();
  const [error, setError] = useState('');
  return auth.can('iam.audit.export') ? <><Button onClick={() => { void readAllIAM('/audit-events:export', 'audits', { filter }).then(reply => { const url = URL.createObjectURL(new Blob([JSON.stringify(reply.audits ?? [], null, 2)], { type: 'application/json' })); const link = document.createElement('a'); link.href = url; link.download = 'authorization-audits.json'; link.click(); URL.revokeObjectURL(url); }).catch(err => setError(getApiErrorMessage(err))); }}>{t('导出授权审计')}</Button>{error && <p role="alert">{error}</p>}</> : null;
}

function RevokeSessions({ userId, reply, refresh }: { userId: string; reply: IAMReply; refresh: () => Promise<unknown> }) {
  const auth = useAuthorization();
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  return <section className="space-y-3 rounded border p-3"><h3>{t('撤销用户全部会话')}</h3><p>{t('目标版本')} {reply.target_revision} · {t('会话数量')} {reply.sessions?.length ?? 0}</p><TextField label="会话撤销原因" value={reason} onChange={setReason} /><Button variant="destructive" disabled={busy || !reason.trim() || !reply.target_revision || !auth.can('identity.user.sessions.revoke')} onClick={() => {
    setBusy(true); setError('');
    void writeIAM(`/users/${userId}/sessions`, 'delete', { user_id: userId, expected_revision: reply.target_revision, expected_policy_revision: auth.snapshot?.versions?.policy_revision, reason, request_id: crypto.randomUUID() }).then(async () => { setReason(''); await auth.refresh(); await refresh(); }).catch(err => { setError(getApiErrorMessage(err)); }).finally(() => setBusy(false));
  }}>{t('确认撤销列出的用户会话')}</Button>{error && <p role="alert">{error}</p>}</section>;
}
