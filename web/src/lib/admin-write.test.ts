import { QueryClient } from '@tanstack/react-query';
import { AxiosHeaders, type InternalAxiosRequestConfig } from 'axios';
import { describe, expect, it } from 'vitest';
import { prepareManagedWrite } from './admin-write';
import type { IAMReply } from './iam-types';
const snapshot: IAMReply = { authorization_mode: 'iam', session: { activation_state: 'active' }, versions: { policy_revision: '12' } };
function config(url: string, method = 'put', data?: unknown): InternalAxiosRequestConfig { return { url, method, data, headers: new AxiosHeaders() }; }
function client() { const c = new QueryClient(); c.setQueryDefaults(['admin-channels'], { meta: { protected: true } }); c.setQueryData(['admin-channels', 'authorization', 'platform', '12'], [{ id: 7, revision: '3' }]); return c; }
describe('owner write preconditions', () => {
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
 it('never loses precision in legacy JSON numeric adapters', () => {
  const c = client(); c.setQueryData(['admin-channels', 'authorization', 'platform', '12'], [{ id: 7, revision: '9007199254740993' }]);
  expect(() => prepareManagedWrite(config('/channel', 'put', { id: 7 }), snapshot, c, () => 'reason')).toThrow();
 });
});
