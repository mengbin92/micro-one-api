import type { QueryClient } from '@tanstack/react-query';
import type { IAMReply } from '@/lib/iam-types';
import type { InternalAxiosRequestConfig } from 'axios';
import { t } from '@/lib/i18n';

function records(value: unknown): Record<string, unknown>[] {
  if (Array.isArray(value)) return value.flatMap(records);
  if (!value || typeof value !== 'object') return [];
  const record = value as Record<string, unknown>;
  return [record, ...Object.values(record).filter(v => v && typeof v === 'object').flatMap(records)];
}
function numericRevision(value: unknown) {
  const number = Number(value);
  if (!Number.isSafeInteger(number) || number < 0) throw new Error(t('版本值无效，请重新加载数据'));
  return number;
}
function revision(row?: Record<string, unknown>) {
  return row?.credentialRevision ?? row?.credential_revision ?? row?.authorizationRevision ?? row?.authorization_revision ?? row?.version ?? row?.revision ?? row?.entitlement_revision ?? row?.entitlementRevision;
}
// Reads only versions returned by owners in the current protected cache. Never
// fetch a fresh revision behind a user's edit, which would conceal a conflict.
export function prepareManagedWrite(config: InternalAxiosRequestConfig, snapshot: IAMReply | undefined, client: QueryClient, askReason: (text: string) => string | null = text => window.prompt(text)) {
  const url = config.url ?? '';
  const method = config.method?.toLowerCase() ?? 'get';
  const actionGet = /\/channel\/(test|update_balance)\//.test(url);
  if (url.includes('/iam/') || (method === 'get' && !actionGet)) return config;
  if (!snapshot) throw new Error(t('授权状态不可用'));
  if (snapshot.authorization_mode === 'legacy') return config;
  if (!snapshot.session || snapshot.session.activation_state !== 'active' || (snapshot.valid_until && Date.parse(snapshot.valid_until) <= Date.now())) throw new Error(t('授权状态不可用'));
  const data = config.data && typeof config.data === 'object' ? { ...config.data } : {};
  const params = { ...config.params };
  const reason = data.reason ?? params.reason ?? config.headers.get('x-authorization-reason') ?? askReason(t('请输入此次变更的原因'));
  if (typeof reason !== 'string' || !reason.trim()) throw new Error(t('变更原因不能为空'));
  config.headers.set('x-authorization-reason', reason.trim());
  data.reason = reason.trim();
  const ownerKey = /\/channel(?:\/|$)/.test(url) ? 'admin-channels' : /\/admin\/models/.test(url) ? 'admin-model' : /\/user(?:\/|$)/.test(url) ? 'admin-users' : /subscription-accounts/.test(url) ? 'admin-subscription-accounts' : /subscription-plans/.test(url) ? 'subscription-plans' : /subscription-groups/.test(url) ? 'admin-subscription-groups' : /subscriptions/.test(url) ? 'admin-subscriptions' : /routing-groups/.test(url) ? 'admin-routing-group' : /\/redemption/.test(url) ? 'admin-redemptions' : /upstream-costs/.test(url) ? 'admin-upstream-costs' : '';
  const cached = client.getQueryCache().findAll({ predicate: query => ownerKey !== '' && query.meta?.protected === true && query.queryKey.some(key => typeof key === 'string' && key.includes(ownerKey)) }).flatMap(query => records(query.state.data));
  const pathId = url.match(/\/(\d+)(?:\/|\?|$)/)?.[1];
  const id = data.id ?? data.channel_id ?? data.account_id ?? data.model_pk ?? data.user_id ?? data.subscription_id ?? pathId;
  const code = url.startsWith('/redemption/') ? decodeURIComponent(url.split('/').at(-1) ?? '') : undefined;
  const key = data.key ?? params.key;
  const row = cached.find(item => code ? item.code === code : key ? item.key === key : String(item.id ?? item.model_pk) === String(id));
  if (data.expected_revision === undefined && (id || code || key) && revision(row) !== undefined) data.expected_revision = numericRevision(revision(row));
  if (Array.isArray(data.model_pks) && !data.expected_revisions) data.expected_revisions = Object.fromEntries(data.model_pks.map((pk: unknown) => [String(pk), numericRevision(revision(cached.find(item => String(item.id) === String(pk))) ?? '0')]));
  if (/\/admin\/models\/\d+\/(aliases|channel-mappings|subscription-mappings)/.test(url) && revision(row) !== undefined) data.expected_model_revision ??= numericRevision(revision(row));
  for (const field of ['account_ids', 'channel_ids']) if (Array.isArray(data[field]) && !data.expected_revisions) data.expected_revisions = Object.fromEntries(data[field].map((pk: unknown) => [String(pk), numericRevision(revision(cached.find(item => String(item.id) === String(pk))) ?? '0')]));
  if (url.includes('/upstream-costs')) {
    const view = cached.find(item => item.revision !== undefined && Array.isArray(item.entries));
    if (view) data.expected_revision ??= url.endsWith('/migrate') ? String(view.revision) : numericRevision(view.revision);
  }
  if (url.startsWith('/option')) {
    const key = String(data.key ?? '');
    const options = client.getQueryCache().findAll({ queryKey: ['admin-options'] }).flatMap(q => records(q.state.data));
    const row = options.find(option => option.key === key);
    data.expected_revision ??= numericRevision(row?.revision ?? '0');
    data.expected_revisions ??= { [key]: data.expected_revision };
  }
  if (url.includes('/billing') && data.version !== undefined) data.expected_revision ??= numericRevision(data.version);
  if (url.startsWith('/user')) data.expected_policy_revision ??= numericRevision(snapshot.versions?.policy_revision ?? 0);
  // Query CAS is used by DELETE and legacy action aliases; bodies keep CAS for
  // protobuf and compatibility adapters. No automatic retry follows a 409.
  for (const key of ['reason', 'expected_revision', 'expected_policy_revision']) if (data[key] !== undefined) params[key] ??= data[key];
  config.params = params;
  if (method !== 'get' && method !== 'delete') config.data = data;
  return config;
}
