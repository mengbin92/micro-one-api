import { QueryClient, QueryObserver } from '@tanstack/react-query';
import { AxiosHeaders, type InternalAxiosRequestConfig } from 'axios';
import { describe, expect, it } from 'vitest';
import { prepareManagedWrite } from './admin-write';
import type { IAMReply } from './iam-types';
import { normalizeSubscriptionAccount } from './subscription-account';
const snapshot: IAMReply = { authorization_mode: 'iam', session: { activation_state: 'active' }, versions: { policy_revision: '12' } };
function config(url: string, method = 'put', data?: unknown): InternalAxiosRequestConfig { return { url, method, data, headers: new AxiosHeaders() }; }
function client() { const c = new QueryClient(); c.setQueryDefaults(['admin-channels'], { meta: { protected: true } }); c.setQueryDefaults(['admin-options'], { meta: { protected: true } }); c.setQueryData(['admin-channels', 'authorization', 'platform', '12'], [{ id: 7, authorization_revision: '3' }]); return c; }
describe('owner write preconditions', () => {
 it.each([1, 3])('assigns a subscription without inferring CAS from a cached object with id %s', (userId) => {
  const c = new QueryClient();
  c.setQueryDefaults(['admin-subscriptions'], { meta: { protected: true } });
  // The cache holds a v2 contract: quota_policy.version is a hash, and its id
  // can equal the user id typed in the assign dialog.
  c.setQueryData(['admin-subscriptions', null], [{
   id: 3,
   user_id: 1,
   entitlement_revision: 4,
   contract: { version: 2, digest: 'digest', coverage_mode: 'selected_groups', coverage: [], quota_policy: { id: 1, version: '9f2c7b1ad0e4', rate_multiplier: 1, daily_limit_usd: null, weekly_limit_usd: null, monthly_limit_usd: null } },
  }]);
  const out = prepareManagedWrite(
   config('/v1/admin/subscriptions/assign', 'post', { user_id: userId, group_id: 1, coverage: [], expires_at: 1800000000, reason: '运营补偿' }),
   snapshot,
   c,
  );
  expect(out.data).toMatchObject({ user_id: userId, group_id: 1, reason: '运营补偿' });
  expect((out.data as Record<string, unknown>).expected_revision).toBeUndefined();
  expect(out.params.expected_revision).toBeUndefined();
 });
 it('uses entitlement_revision for subscription writes despite cached contract hash versions', () => {
  const c = new QueryClient();
  c.setQueryDefaults(['admin-subscriptions'], { meta: { protected: true } });
  c.setQueryData(['admin-subscriptions', null], [{
   id: 7,
   version: 2,
   entitlement_revision: 4,
   contract: { version: 2, digest: 'hash', coverage_mode: 'selected_groups', coverage: [], quota_policy: { id: 7, version: '9f2c7b1ad0e4', rate_multiplier: 1, daily_limit_usd: null, weekly_limit_usd: null, monthly_limit_usd: null } },
  }]);
  const out = prepareManagedWrite(config('/v1/admin/subscriptions/7/revoke', 'post', { reason: 'policy violation' }), snapshot, c);
  expect(out.data.expected_revision).toBe(4);
 });
 it.each(['revoke', 'extend', 'reset-quota'])('ignores a preceding contract policy with the same id when preparing %s', (action) => {
  const c = new QueryClient();
  c.setQueryDefaults(['admin-subscriptions'], { meta: { protected: true } });
  c.setQueryData(['admin-subscriptions', null], [
   { id: 3, entitlement_revision: 8, contract: { quota_policy: { id: 7, version: '9f2c7b1ad0e4' } } },
   { id: 7, entitlement_revision: 4 },
  ]);
  const out = prepareManagedWrite(config(`/v1/admin/subscriptions/7/${action}`, 'post', { reason: 'reviewed' }), snapshot, c);
  expect(out.data.expected_revision).toBe(4);
  expect(out.params.expected_revision).toBe(4);
 });
 it('preserves an explicit stale subscription revision for owner CAS', () => {
  const c = new QueryClient();
  c.setQueryDefaults(['admin-subscriptions'], { meta: { protected: true } });
  c.setQueryData(['admin-subscriptions', null], [{ id: 7, entitlement_revision: 4 }]);
  const out = prepareManagedWrite(config('/v1/admin/subscriptions/7/revoke', 'post', { reason: 'reviewed', expected_revision: 2 }), snapshot, c);
  expect(out.data.expected_revision).toBe(2);
  expect(out.params.expected_revision).toBe(2);
 });
 it.each(['9f2c7b1ad0e4', '9007199254740993'])('rejects invalid subscription entitlement revision %s', (entitlementRevision) => {
  const c = new QueryClient();
  c.setQueryDefaults(['admin-subscriptions'], { meta: { protected: true } });
  c.setQueryData(['admin-subscriptions', null], [{ id: 7, entitlement_revision: entitlementRevision }]);
  expect(() => prepareManagedWrite(config('/v1/admin/subscriptions/7/revoke', 'post', { reason: 'reviewed' }), snapshot, c)).toThrow('版本值无效，请重新加载数据');
 });
 it('keeps all displayed pricing versions together and preserves stale batch preconditions', () => {
  const c = client();
  c.setQueryData(['admin-options'], [{ key: 'ModelPrice', revision: '7' }, { key: 'ModelRatio', revision: '8' }, { key: 'CompletionRatio', revision: '9' }]);
  const values = { ModelPrice: '{}', ModelRatio: '{}', CompletionRatio: '{}' };
  expect(prepareManagedWrite(config('/option/', 'put', { values, reason: 'batch pricing' }), snapshot, c).data.expected_revisions).toEqual({ ModelPrice: '7', ModelRatio: '8', CompletionRatio: '9' });
  const stale = { ModelPrice: '6', ModelRatio: '8', CompletionRatio: '9' };
  expect(prepareManagedWrite(config('/option/', 'put', { values, expected_revisions: stale, reason: 'batch pricing' }), snapshot, c).data.expected_revisions).toEqual(stale);
 });
 it('uses the channel list instead of an active older health cache', () => {
  const c = new QueryClient();
  c.setQueryDefaults(['admin-channels-health'], { meta: { protected: true } });
  c.setQueryDefaults(['admin-channels'], { meta: { protected: true } });
  c.setQueryData(['admin-channels-health'], [{ id: '7', authorizationRevision: '3' }]);
  c.setQueryData(['admin-channels'], [{ id: '7', authorizationRevision: '4' }]);
  const stopHealth = new QueryObserver(c, { queryKey: ['admin-channels-health'], staleTime: Infinity }).subscribe(() => {});
  const stopList = new QueryObserver(c, { queryKey: ['admin-channels'], staleTime: Infinity }).subscribe(() => {});
  try {
   const out = prepareManagedWrite(config('/channel/disable/7', 'post'), snapshot, c, () => 'disable');
   expect(out.data.expected_revision).toBe(4);
  } finally { stopHealth(); stopList(); c.clear(); }
 });
 it('sends the displayed subscription account revision and reason when disabling', () => {
  const c = new QueryClient(); c.setQueryDefaults(['admin-subscription-accounts'], { meta: { protected: true } });
  const raw = { id: 5, credential_revision: '1' };
  c.setQueryData(['admin-subscription-accounts'], [normalizeSubscriptionAccount(raw)]);
  const result = prepareManagedWrite(config('/subscription-accounts/5/status', 'put', { account_id: 5, status: 2 }), snapshot, c, () => '上游到期');
  expect(result.data).toMatchObject({ account_id: 5, status: 2, expected_revision: 1, reason: '上游到期' });
  expect(result.params).toMatchObject({ expected_revision: 1, reason: '上游到期' });
 });
 it.each(['enable', 'disable'])('uses both revisions for the user %s alias', action => {
  const c = new QueryClient();
  c.setQueryDefaults(['admin-users'], { meta: { protected: true } });
  c.setQueryData(['admin-users'], [{ id: 7, authorization_revision: '3' }]);
  const result = prepareManagedWrite(config(`/user/${action}/7`, 'post', { reason: 'reviewed' }), snapshot, c);
  expect(result.params).toMatchObject({ expected_revision: 3, expected_policy_revision: 12 });
 });
 it('does not use an upstream account identifier as a subscription account revision target', () => {
  const c = new QueryClient();
  c.setQueryDefaults(['admin-subscription-accounts'], { meta: { protected: true } });
  c.setQueryData(['admin-subscription-accounts'], [{ id: 7, credential_revision: '4' }]);
  const result = prepareManagedWrite(config('/subscription-accounts', 'post', { account_id: '7', reason: 'reviewed' }), snapshot, c);
  expect(result.data).toEqual({ account_id: '7', reason: 'reviewed' });
  expect(result.params.expected_revision).toBeUndefined();
 });
 it('fails closed while authorization refreshes, and cancels empty reason', () => {
  expect(() => prepareManagedWrite(config('/channel', 'put'), undefined, client())).toThrow();
  expect(() => prepareManagedWrite(config('/channel', 'put', { id: 7 }), snapshot, client(), () => null)).toThrow();
 });
 it('sends Chinese reasons through UTF-8 body and query instead of browser-limited headers', () => {
  const out = prepareManagedWrite(config('/option/', 'put', { key: 'ModelPrice', value: '{}', reason: '新模型上架测试' }), snapshot, client());
  expect(() => new Headers(out.headers.toJSON() as Record<string, string>)).not.toThrow();
  expect(out.headers.has('x-authorization-reason')).toBe(false);
  expect(out.data.reason).toBe('新模型上架测试');
  expect(out.params.reason).toBe('新模型上架测试');
 });
 it('forwards displayed versions and preserves an explicit stale revision for owner CAS', () => {
  const c = client(); const current = prepareManagedWrite(config('/channel', 'put', { id: 7 }), snapshot, c, () => 'reviewed reason');
  expect(current.data).toMatchObject({ expected_revision: 3, reason: 'reviewed reason' }); expect(current.params.expected_revision).toBe(3);
  expect(prepareManagedWrite(config('/channel', 'put', { id: 7, expected_revision: 2 }), snapshot, c, () => 'reason').data.expected_revision).toBe(2);
 });
 it('preserves the serialized request body and its reviewed revision after verification', () => {
  const body = { id: 7, name: 'updated channel', expected_revision: 2, reason: 'reviewed' };
  const out = prepareManagedWrite(config('/channel', 'put', JSON.stringify(body)), snapshot, client());
  expect(out.data).toEqual(body);
  expect(out.params.expected_revision).toBe(2);
 });
 it('treats a blank serialized action body as empty', () => {
  const out = prepareManagedWrite(config('/channel/disable/7', 'post', ' '), snapshot, client(), () => 'disable');
  expect(out.data).toMatchObject({ reason: 'disable', expected_revision: 3 });
 });
 it('preserves a caller-supplied query revision in both transports', () => {
  const request = { ...config('/channel', 'put', { id: 7, reason: 'reviewed' }), params: { expected_revision: 2 } };
  const result = prepareManagedWrite(request, snapshot, client());
  expect(result.data.expected_revision).toBe(2);
  expect(result.params.expected_revision).toBe(2);
 });
 it('keeps DELETE and action aliases CAS and reason in query, never re-fetches versions', () => {
  const out = prepareManagedWrite(config('/channel/test/7', 'get'), snapshot, client(), () => 'probe');
  expect(out.params).toMatchObject({ expected_revision: 3, reason: 'probe' }); expect(out.headers.has('x-authorization-reason')).toBe(false);
 });
 it('sends the parent model revision when deleting an alias', () => {
  const c = new QueryClient(); c.setQueryDefaults(['admin-model-detail'], { meta: { protected: true } });
  c.setQueryData(['admin-model-detail', 7], { model: { id: 7, authorization_revision: '4' }, aliases: [{ id: 31, model_pk: 7 }] });
  const out = prepareManagedWrite(config('/admin/models/7/aliases/31', 'delete'), snapshot, c, () => 'remove alias');
  expect(out.params).toMatchObject({ expected_model_revision: 4, reason: 'remove alias' });
  expect(out.data).toBeUndefined();
 });
 it('uses the displayed model detail after an alias write advances its revision', () => {
  const c = new QueryClient();
  c.setQueryDefaults(['admin-models'], { meta: { protected: true } });
  c.setQueryDefaults(['admin-model-detail'], { meta: { protected: true } });
  c.setQueryData(['admin-models'], { models: [{ id: 7, authorization_revision: '3' }] });
  c.setQueryData(['admin-model-detail', 7], { model: { id: 7, authorization_revision: '4' } });
  const out = prepareManagedWrite(config('/admin/models/7/aliases', 'post', { alias: 'new-alias' }), snapshot, c, () => 'add alias');
  expect(out.data.expected_model_revision).toBe(4);
 });
 it('uses the active account list instead of an earlier inactive filter cache', () => {
  const c = new QueryClient(); c.setQueryDefaults(['admin-subscription-accounts'], { meta: { protected: true } });
  c.setQueryData(['admin-subscription-accounts', 'old-filter'], [{ id: 7, credentialRevision: '3' }]);
  c.setQueryData(['admin-subscription-accounts', 'current-filter'], [{ id: 7, credentialRevision: '4' }]);
  const unsubscribe = new QueryObserver(c, { queryKey: ['admin-subscription-accounts', 'current-filter'], staleTime: Infinity }).subscribe(() => {});
  try {
   const out = prepareManagedWrite(config('/subscription-accounts/7/status', 'put', { account_id: 7, status: 2 }), snapshot, c, () => 'disable');
   expect(out.data.expected_revision).toBe(4);
  } finally { unsubscribe(); c.clear(); }
 });
 it('uses the account editor list instead of the account health view', () => {
  const c = new QueryClient();
  c.setQueryDefaults(['admin-subscription-accounts-health'], { meta: { protected: true } });
  c.setQueryDefaults(['admin-subscription-accounts'], { meta: { protected: true } });
  c.setQueryData(['admin-subscription-accounts-health'], [{ id: 7, credentialRevision: '3' }]);
  c.setQueryData(['admin-subscription-accounts'], [{ id: 7, credentialRevision: '4' }]);
  const result = prepareManagedWrite(config('/subscription-accounts/7/status', 'put', { account_id: 7, status: 2, reason: 'reviewed' }), snapshot, c);
  expect(result.data.expected_revision).toBe(4);
 });
 it.each(['/admin/channels/8/models', '/admin/subscription-accounts/8/models'])('uses the model revision when writing a mapping at %s', (url) => {
  const c = new QueryClient();
  c.setQueryDefaults(['admin-subscription-accounts'], { meta: { protected: true } });
  c.setQueryDefaults(['admin-model-detail'], { meta: { protected: true } });
  c.setQueryData(['admin-subscription-accounts'], [{ id: 7, credentialRevision: '3' }]);
  c.setQueryData(['admin-model-detail', 7], { model: { id: 7, authorization_revision: '4' } });
  const result = prepareManagedWrite(config(url, 'post', { model_pk: 7, reason: 'reviewed' }), snapshot, c);
  expect(result.data.expected_model_revision).toBe(4);
  expect(result.data.expected_revision).toBeUndefined();
 });
 it('uses the route id and entitlement revision for an existing subscription', () => {
  const c = new QueryClient();
  c.setQueryDefaults(['admin-subscriptions'], { meta: { protected: true } });
  c.setQueryData(['admin-subscriptions'], [{ id: 1, entitlement_revision: 5 }, { id: 7, entitlement_revision: 4 }]);
  const out = prepareManagedWrite(config('/v1/admin/subscriptions/7/extend', 'post', { user_id: 1, reason: 'reviewed' }), snapshot, c);
  expect(out.data.expected_revision).toBe(4);
 });
 it('does not inject a routing group revision into a per-user price write', () => {
  const c = new QueryClient();
  c.setQueryDefaults(['admin-routing-group'], { meta: { protected: true } });
  c.setQueryData(['admin-routing-group', 7], { group: { id: 7, revision: 4 } });
  const out = prepareManagedWrite(config('/v1/admin/routing-groups/7/user-price/1', 'put', { expected_version: '9', reason: 'reviewed' }), snapshot, c);
  expect(out.data).toEqual({ expected_version: '9', reason: 'reviewed' });
 });
 it.each(['batch-reset-quota', 'batch-quota-template'])('sends per-account revisions for %s', (action) => {
  const c = new QueryClient(); c.setQueryDefaults(['admin-subscription-accounts'], { meta: { protected: true } });
  c.setQueryData(['admin-subscription-accounts'], [{ id: 7, credentialRevision: '4' }, { id: 8, credentialRevision: '5' }]);
  const out = prepareManagedWrite(config(`/subscription-accounts/${action}`, 'post', { account_ids: [7, 8] }), snapshot, c, () => 'quota review');
  expect(out.data.expected_revisions).toEqual({ '7': 4, '8': 5 });
 });
 it('never loses precision in legacy JSON numeric adapters', () => {
  const c = client(); c.setQueryData(['admin-channels', 'authorization', 'platform', '12'], [{ id: 7, authorization_revision: '9007199254740993' }]);
  expect(() => prepareManagedWrite(config('/channel', 'put', { id: 7 }), snapshot, c, () => 'reason')).toThrow();
 });
 it('rejects inactive and expired authorization before prompting or sending', () => {
  for (const invalid of [{ ...snapshot, session: undefined }, { ...snapshot, session: { activation_state: 'selection_required' } }, { ...snapshot, valid_until: new Date(0).toISOString() }]) {
   let prompts = 0;
   expect(() => prepareManagedWrite(config('/channel', 'put'), invalid, client(), () => { prompts++; return 'reason'; })).toThrow('授权状态不可用');
   expect(prompts).toBe(0);
  }
 });
 it('leaves read-only and IAM requests alone, and supports reviewed legacy writes', () => {
  for (const request of [config('/channel/7', 'get'), config('/iam/roles', 'post')]) expect(prepareManagedWrite(request, undefined, client())).toBe(request);
  const request = config('/channel', 'put');
  expect(prepareManagedWrite(request, { authorization_mode: 'legacy' }, client())).toBe(request);
 });
 it.each([
  ['/user/7', ['admin-users'], [{ id: 7, authorization_revision: '4' }]],
  ['/admin/models/7/aliases', ['admin-models'], { models: [{ id: 7, authorization_revision: '4' }] }],
  ['/subscription-accounts/7', ['admin-subscription-accounts'], [{ id: 7, credentialRevision: '4' }]],
  ['/subscription-plans/7', ['admin', 'subscription-plans'], [{ id: 7, revision: '4' }]],
  ['/subscription-groups/7', ['admin-subscription-groups'], [{ id: 7, revision: '4' }]],
  ['/subscriptions/7', ['admin-subscriptions'], [{ id: 7, entitlement_revision: '4' }]],
  ['/routing-groups/7', ['admin-routing-groups'], { groups: [{ id: 7, revision: '4' }] }],
 ] as const)('uses only the displayed owner revision for %s', (url, key, rows) => {
  const c = new QueryClient(); c.setQueryDefaults(key, { meta: { protected: true } }); c.setQueryData(key, rows);
  const result = prepareManagedWrite(config(url, 'put', { reason: '  reviewed  ' }), snapshot, c);
  expect(result.params.reason).toBe('reviewed');
  expect(result.data[url.includes('/aliases') ? 'expected_model_revision' : 'expected_revision']).toBe(4);
  if (url.startsWith('/user')) expect(result.data.expected_policy_revision).toBe(12);
 });
 it.each(['model_pks', 'account_ids', 'channel_ids'])('keeps per-item CAS for bulk field %s', (field) => {
  const c = client(); const url = field === 'model_pks' ? '/admin/models' : field === 'account_ids' ? '/subscription-accounts/batch-reset-quota' : '/channel';
  c.setQueryDefaults(['admin-models'], { meta: { protected: true } }); c.setQueryData(['admin-models'], { models: [{ id: 7, authorization_revision: '3' }] });
  c.setQueryDefaults(['admin-subscription-accounts'], { meta: { protected: true } }); c.setQueryData(['admin-subscription-accounts'], [{ id: 7, credentialRevision: '3' }]);
  const result = prepareManagedWrite(config(url, 'put', { [field]: [7, 8], reason: 'bulk review' }), snapshot, c);
  expect(result.data.expected_revisions).toEqual({ '7': 3, '8': 0 });
 });
 it('uses displayed config and financial versions without overwriting explicit CAS', () => {
  const c = client(); c.setQueryData(['admin-options'], [{ key: 'Price', revision: '5' }]);
  const option = prepareManagedWrite(config('/option', 'put', { key: 'Price', reason: 'price' }), snapshot, c);
  expect(option.data.expected_revisions).toEqual({ Price: '5' });
  const billing = prepareManagedWrite(config('/billing/config', 'put', { version: '6', reason: 'billing' }), snapshot, c);
  expect(billing.data.expected_revision).toBe(6);
  expect(prepareManagedWrite(config('/billing/config', 'put', { version: '6', expected_revision: 2, reason: 'billing' }), snapshot, c).data.expected_revision).toBe(2);
 });
 it.each(['/option', '/option/'])('sends string revisions required by the system-options JSON contract for %s', (url) => {
  const c = client(); c.setQueryData(['admin-options'], [{ key: 'ModelPrice', revision: '7' }]);
  const out = prepareManagedWrite(config(url, 'put', { key: 'ModelPrice', value: '{}', reason: 'publish model pricing' }), snapshot, c);
  expect(out.data).toMatchObject({ expected_revision: '7', expected_revisions: { ModelPrice: '7' }, reason: 'publish model pricing' });
  expect(prepareManagedWrite(config(url, 'put', { key: 'ModelPrice', value: '{}', expected_revision: 6, reason: 'stale write' }), snapshot, c).data.expected_revision).toBe('6');
 });
 it('preserves string migration versions and redemption DELETE preconditions', () => {
  const c = new QueryClient();
  c.setQueryDefaults(['admin-upstream-costs'], { meta: { protected: true } }); c.setQueryData(['admin-upstream-costs'], { revision: '9', entries: [] });
  expect(prepareManagedWrite(config('/upstream-costs/migrate', 'post', { reason: 'migrate' }), snapshot, c).data.expected_revision).toBe('9');
  expect(prepareManagedWrite(config('/upstream-costs', 'put', { reason: 'edit' }), snapshot, c).data.expected_revision).toBe(9);
  c.setQueryDefaults(['admin-redemptions'], { meta: { protected: true } }); c.setQueryData(['admin-redemptions'], { codes: [{ code: 'test code', revision: 2 }], total: 1 });
  const result = prepareManagedWrite(config('/redemption/test%20code', 'delete'), snapshot, c, () => 'delete');
  expect(result.params.expected_revision).toBe(2); expect(result.data).toBeUndefined();
 });
});
