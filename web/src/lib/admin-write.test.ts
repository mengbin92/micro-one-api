import { QueryClient, QueryObserver } from '@tanstack/react-query';
import { AxiosHeaders, type InternalAxiosRequestConfig } from 'axios';
import { describe, expect, it } from 'vitest';
import { prepareManagedWrite } from './admin-write';
import type { IAMReply } from './iam-types';
import { normalizeSubscriptionAccount } from './subscription-account';
const snapshot: IAMReply = { authorization_mode: 'iam', session: { activation_state: 'active' }, versions: { policy_revision: '12' } };
function config(url: string, method = 'put', data?: unknown): InternalAxiosRequestConfig { return { url, method, data, headers: new AxiosHeaders() }; }
function client() { const c = new QueryClient(); c.setQueryDefaults(['admin-channels'], { meta: { protected: true } }); c.setQueryData(['admin-channels', 'authorization', 'platform', '12'], [{ id: 7, revision: '3' }]); return c; }
describe('owner write preconditions', () => {
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
 it('fails closed while authorization refreshes, and cancels empty reason', () => {
  expect(() => prepareManagedWrite(config('/channel', 'put'), undefined, client())).toThrow();
  expect(() => prepareManagedWrite(config('/channel', 'put', { id: 7 }), snapshot, client(), () => null)).toThrow();
 });
 it('forwards displayed versions and preserves an explicit stale revision for owner CAS', () => {
  const c = client(); const current = prepareManagedWrite(config('/channel', 'put', { id: 7 }), snapshot, c, () => 'reviewed reason');
  expect(current.data).toMatchObject({ expected_revision: 3, reason: 'reviewed reason' }); expect(current.params.expected_revision).toBe(3);
  expect(prepareManagedWrite(config('/channel', 'put', { id: 7, expected_revision: 2 }), snapshot, c, () => 'reason').data.expected_revision).toBe(2);
 });
 it('keeps DELETE and action aliases CAS in query and headers, never re-fetches versions', () => {
  const out = prepareManagedWrite(config('/channel/test/7', 'get'), snapshot, client(), () => 'probe');
  expect(out.params).toMatchObject({ expected_revision: 3, reason: 'probe' }); expect(out.headers.get('x-authorization-reason')).toBe('probe');
 });
 it('sends the parent model revision when deleting an alias', () => {
  const c = new QueryClient(); c.setQueryDefaults(['admin-model-detail'], { meta: { protected: true } });
  c.setQueryData(['admin-model-detail', 7], { model: { id: 7, revision: '4' }, aliases: [{ id: 31, model_pk: 7 }] });
  const out = prepareManagedWrite(config('/admin/models/7/aliases/31', 'delete'), snapshot, c, () => 'remove alias');
  expect(out.params).toMatchObject({ expected_model_revision: 4, reason: 'remove alias' });
  expect(out.data).toBeUndefined();
 });
 it('uses the displayed model detail after an alias write advances its revision', () => {
  const c = new QueryClient();
  c.setQueryDefaults(['admin-models'], { meta: { protected: true } });
  c.setQueryDefaults(['admin-model-detail'], { meta: { protected: true } });
  c.setQueryData(['admin-models'], { models: [{ id: 7, revision: '3' }] });
  c.setQueryData(['admin-model-detail', 7], { model: { id: 7, revision: '4' } });
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
 it.each(['batch-reset-quota', 'batch-quota-template'])('sends per-account revisions for %s', (action) => {
  const c = new QueryClient(); c.setQueryDefaults(['admin-subscription-accounts'], { meta: { protected: true } });
  c.setQueryData(['admin-subscription-accounts'], [{ id: 7, credentialRevision: '4' }, { id: 8, credentialRevision: '5' }]);
  const out = prepareManagedWrite(config(`/subscription-accounts/${action}`, 'post', { account_ids: [7, 8] }), snapshot, c, () => 'quota review');
  expect(out.data.expected_revisions).toEqual({ '7': 4, '8': 5 });
 });
 it('never loses precision in legacy JSON numeric adapters', () => {
  const c = client(); c.setQueryData(['admin-channels', 'authorization', 'platform', '12'], [{ id: 7, revision: '9007199254740993' }]);
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
  ['/user/7', 'admin-users', { authorization_revision: '4' }],
  ['/admin/models/7/aliases', 'admin-models', { version: '4' }],
  ['/subscription-accounts/7', 'admin-subscription-accounts', { credentialRevision: '4' }],
  ['/subscription-plans/7', 'subscription-plans', { credential_revision: '4' }],
  ['/subscription-groups/7', 'admin-subscription-groups', { authorizationRevision: '4' }],
  ['/subscriptions/7', 'admin-subscriptions', { entitlement_revision: '4' }],
  ['/routing-groups/7', 'admin-routing-groups', { entitlementRevision: '4' }],
 ] as const)('uses only the displayed owner revision for %s', (url, owner, version) => {
  const c = new QueryClient(); c.setQueryDefaults([owner], { meta: { protected: true } }); c.setQueryData([owner], { rows: [{ id: 7, ...version }] });
  const result = prepareManagedWrite(config(url, 'put', { reason: '  reviewed  ' }), snapshot, c);
  expect(result.data.expected_revision).toBe(4); expect(result.headers.get('x-authorization-reason')).toBe('reviewed');
  if (url.startsWith('/user')) expect(result.data.expected_policy_revision).toBe(12);
  if (url.includes('/aliases')) expect(result.data.expected_model_revision).toBe(4);
 });
 it.each(['model_pks', 'account_ids', 'channel_ids'])('keeps per-item CAS for bulk field %s', (field) => {
  const c = client(); const url = field === 'model_pks' ? '/admin/models' : '/channel';
  c.setQueryDefaults(['admin-models'], { meta: { protected: true } }); c.setQueryData(['admin-models'], [{ id: 7, revision: '3' }]);
  const result = prepareManagedWrite(config(url, 'put', { [field]: [7, 8], reason: 'bulk review' }), snapshot, c);
  expect(result.data.expected_revisions).toEqual({ '7': 3, '8': 0 });
 });
 it('uses displayed config and financial versions without overwriting explicit CAS', () => {
  const c = client(); c.setQueryData(['admin-options'], [{ key: 'Price', revision: '5' }]);
  const option = prepareManagedWrite(config('/option', 'put', { key: 'Price', reason: 'price' }), snapshot, c);
  expect(option.data.expected_revisions).toEqual({ Price: 5 });
  const billing = prepareManagedWrite(config('/billing/config', 'put', { version: '6', reason: 'billing' }), snapshot, c);
  expect(billing.data.expected_revision).toBe(6);
  expect(prepareManagedWrite(config('/billing/config', 'put', { version: '6', expected_revision: 2, reason: 'billing' }), snapshot, c).data.expected_revision).toBe(2);
 });
 it('preserves string migration versions and redemption DELETE preconditions', () => {
  const c = new QueryClient();
  c.setQueryDefaults(['admin-upstream-costs'], { meta: { protected: true } }); c.setQueryData(['admin-upstream-costs'], { revision: '9', entries: [] });
  expect(prepareManagedWrite(config('/upstream-costs/migrate', 'post', { reason: 'migrate' }), snapshot, c).data.expected_revision).toBe('9');
  expect(prepareManagedWrite(config('/upstream-costs', 'put', { reason: 'edit' }), snapshot, c).data.expected_revision).toBe(9);
  c.setQueryDefaults(['admin-redemptions'], { meta: { protected: true } }); c.setQueryData(['admin-redemptions'], [{ code: 'test code', revision: 2 }]);
  const result = prepareManagedWrite(config('/redemption/test%20code', 'delete'), snapshot, c, () => 'delete');
  expect(result.params.expected_revision).toBe(2); expect(result.data).toBeUndefined();
 });
});
