import type { QueryClient } from '@tanstack/react-query';
import type { IAMReply } from '@/lib/iam-types';
import type { InternalAxiosRequestConfig } from 'axios';
import { t } from '@/lib/i18n';

type Row = Record<string, unknown>;
type Owner = {
  keys: readonly (readonly string[])[];
  revisions: readonly string[];
  id?: string;
  ids?: string;
};

const authorizationRevision = ['authorization_revision', 'authorizationRevision'];
const owners: Partial<Record<string, Owner>> = {
  channel: { keys: [['admin-channels']], revisions: authorizationRevision, id: 'channel_id', ids: 'channel_ids' },
  models: { keys: [['admin-models'], ['admin-model-detail']], revisions: authorizationRevision, id: 'model_pk', ids: 'model_pks' },
  user: { keys: [['admin-users']], revisions: authorizationRevision, id: 'user_id' },
  'subscription-accounts': { keys: [['admin-subscription-accounts']], revisions: ['credential_revision', 'credentialRevision'], id: 'id', ids: 'account_ids' },
  'subscription-plans': { keys: [['admin', 'subscription-plans']], revisions: ['revision'] },
  'subscription-groups': { keys: [['admin-subscription-groups']], revisions: ['revision'] },
  subscriptions: { keys: [['admin-subscriptions']], revisions: ['entitlement_revision', 'entitlementRevision'] },
  'routing-groups': { keys: [['admin-routing-group'], ['admin-routing-groups']], revisions: ['revision'] },
  redemption: { keys: [['admin-redemptions']], revisions: ['revision'] },
  'upstream-costs': { keys: [['admin-upstream-costs']], revisions: ['revision'] },
  option: { keys: [['admin-options']], revisions: ['revision'] },
};

function writeTarget(config: Pick<InternalAxiosRequestConfig, 'url' | 'method'>) {
  const [resource, first, action, last] = (config.url ?? '').split('?')[0]
    .replace(/^\/(?:api\/)?(?:v1\/)?(?:admin\/)?/, '').split('/');
  const method = config.method?.toLowerCase() ?? 'get';
  const legacyAction = resource === 'channel' && ['test', 'update_balance', 'enable', 'disable'].includes(first);
  const write = !['get', 'head', 'options'].includes(method) || (method === 'get' && legacyAction);
  const mapping = ['channels', 'subscription-accounts'].includes(resource) && action === 'models';
  const modelRelation = mapping || (resource === 'models' && ['aliases', 'channel-mappings', 'subscription-mappings'].includes(action));
  const routingPolicy = resource === 'routing-groups' && ['billing', 'user-price'].includes(action);
  const name = mapping ? 'models' : resource;
  const owner = !routingPolicy && Object.hasOwn(owners, name) ? owners[name] : undefined;
  const userStatusAlias = resource === 'user' && ['enable', 'disable'].includes(first);
  let routeId = first;
  if (mapping) routeId = last;
  else if (legacyAction || userStatusAlias) routeId = action;
  return { resource, first, action, routeId, method, write, modelRelation, owner };
}

function object(value: unknown): Row | undefined {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Row : undefined;
}

function ownerRows(value: unknown, key: unknown): Row[] {
  const record = object(value);
  // Models and routing groups expose both a list and a detail response.
  const rows = key === 'admin-models' ? record?.models ?? value
    : key === 'admin-model-detail' ? [record?.model]
    : key === 'admin-routing-groups' ? record?.groups
    : key === 'admin-routing-group' ? [record?.group]
    : value;
  return (Array.isArray(rows) ? rows : [rows]).map(object).filter(row => row !== undefined);
}

function queryMatches(queryKey: readonly unknown[], keys: Owner['keys']) {
  return keys.some(prefix => prefix.every((part, index) => queryKey[index] === part));
}

export function numericRevision(value: unknown) {
  const number = typeof value === 'number' || (typeof value === 'string' && /^\d+$/.test(value)) ? Number(value) : NaN;
  if (!Number.isSafeInteger(number) || number < 0) throw new Error(t('版本值无效，请重新加载数据'));
  return number;
}

export async function refreshConflictingWrite(config: InternalAxiosRequestConfig, client: QueryClient) {
  const { resource, owner, write } = writeTarget(config);
  if (!write || !owner) return;
  const keys = resource === 'channel' ? [...owner.keys, ['admin-channels-health']]
    : resource === 'subscription-accounts' ? [...owner.keys, ['admin-subscription-accounts-health']]
    : owner.keys;
  await client.invalidateQueries({ predicate: query => query.meta?.protected === true && queryMatches(query.queryKey, keys) });
}

// Use the reviewed revision from the resource's own view to preserve CAS on stale edits.
export function prepareManagedWrite(config: InternalAxiosRequestConfig, snapshot: IAMReply | undefined, client: QueryClient, askReason: (text: string) => string | null = text => window.prompt(text)) {
  const { resource, first, action, routeId, method, write, modelRelation, owner } = writeTarget(config);
  if (resource === 'iam' || !write) return config;
  if (!snapshot) throw new Error(t('授权状态不可用'));
  if (snapshot.authorization_mode === 'legacy') return config;
  if (!snapshot.session || snapshot.session.activation_state !== 'active' || (snapshot.valid_until && Date.parse(snapshot.valid_until) <= Date.now())) throw new Error(t('授权状态不可用'));

  // Axios has already serialized the JSON body when retrying after verification.
  const data = { ...object(typeof config.data === 'string' && config.data.trim() ? JSON.parse(config.data) : config.data) };
  const params = { ...config.params };
  const reason = data.reason ?? params.reason ?? config.headers.get('x-authorization-reason') ?? askReason(t('请输入此次变更的原因'));
  if (typeof reason !== 'string' || !reason.trim()) throw new Error(t('变更原因不能为空'));
  // Browser headers cannot carry Unicode; the admin guard reads body/query.
  config.headers.delete('x-authorization-reason');
  data.reason = reason.trim();
  params.reason = reason.trim();

  if (owner) {
    const preferDetail = (key: unknown) => modelRelation && key === 'admin-model-detail';
    const cached = client.getQueryCache().findAll({ predicate: query => query.meta?.protected === true && queryMatches(query.queryKey, owner.keys) })
      .sort((a, b) => Number(b.isActive()) - Number(a.isActive())
        || Number(preferDetail(b.queryKey[0])) - Number(preferDetail(a.queryKey[0]))
        || b.state.dataUpdatedAt - a.state.dataUpdatedAt)
      .flatMap(query => ownerRows(query.state.data, query.queryKey[0]));
    const revision = (row?: Row) => owner.revisions.map(field => row?.[field]).find(value => value !== undefined && value !== null);
    const byId = (id: unknown) => cached.find(row => String(row.id) === String(id));
    const byKey = (key: string) => cached.find(row => row.key === key);
    const explicit = data.expected_revision ?? params.expected_revision;

    if (resource === 'option') {
      const values = object(data.values);
      if (values) {
        data.expected_revisions ??= Object.fromEntries(Object.keys(values).map(key => [key, String(numericRevision(revision(byKey(key)) ?? 0))]));
      } else {
        const key = String(data.key ?? '');
        data.expected_revision = String(explicit ?? numericRevision(revision(byKey(key)) ?? 0));
        data.expected_revisions ??= { [key]: data.expected_revision };
      }
    } else if (resource === 'upstream-costs') {
      const displayed = revision(cached.find(row => Array.isArray(row.entries)));
      if (displayed !== undefined) data.expected_revision ??= explicit ?? (first === 'migrate' ? String(displayed) : numericRevision(displayed));
    } else {
      const id = /^\d+$/.test(routeId ?? '') ? routeId : owner.id ? data[owner.id] ?? data.id : undefined;
      const code = resource === 'redemption' && first ? decodeURIComponent(first) : undefined;
      const displayed = revision(code ? cached.find(row => row.code === code) : id === undefined ? undefined : byId(id));
      const field = modelRelation ? 'expected_model_revision' : 'expected_revision';
      const supplied = data[field] ?? params[field];
      if (supplied !== undefined || displayed !== undefined) {
        data[field] = supplied ?? numericRevision(displayed);
        if (resource === 'routing-groups' && action === 'resource-overrides') data[field] = String(data[field]);
      }
      const ids = owner.ids ? data[owner.ids] : undefined;
      if (Array.isArray(ids) && !data.expected_revisions) {
        data.expected_revisions = Object.fromEntries(ids.map(id => [String(id), numericRevision(revision(byId(id)) ?? 0)]));
      }
    }
  }
  if (resource === 'billing' && data.version !== undefined) data.expected_revision ??= params.expected_revision ?? numericRevision(data.version);
  if (resource === 'user') data.expected_policy_revision ??= params.expected_policy_revision ?? numericRevision(snapshot.versions?.policy_revision ?? 0);
  // Query CAS is used by DELETE and legacy action aliases; bodies keep CAS for
  // protobuf and compatibility adapters. No automatic retry follows a 409.
  for (const key of ['reason', 'expected_revision', 'expected_model_revision', 'expected_policy_revision']) if (data[key] !== undefined) params[key] ??= data[key];
  config.params = params;
  if (method !== 'get' && method !== 'delete') config.data = data;
  return config;
}
