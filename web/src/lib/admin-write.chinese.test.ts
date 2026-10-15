import { QueryClient } from '@tanstack/react-query';
import { http, HttpResponse } from 'msw';
import { afterEach, describe, expect, it } from 'vitest';
import { adminApiClient } from './api';
import { prepareManagedWrite } from './admin-write';
import { setWritePreparer } from './authorization-events';
import { activateRoles, writeIAM } from './iam-api';
import { server } from '@/test/msw/server';
import type { IAMReply } from './iam-types';

const reason = '上游缺货，调整配置 ✅';
const snapshot: IAMReply = { authorization_mode: 'iam', session: { activation_state: 'active' } };
afterEach(() => setWritePreparer(undefined));

describe('Chinese reasons across management transports', () => {
  it.each([
    ['channel edit', 'put', '/channel'],
    ['channel enable', 'post', '/channel/enable/7'],
    ['channel test', 'get', '/channel/test/7'],
    ['channel balance refresh', 'get', '/channel/update_balance/7'],
    ['channel delete', 'delete', '/channel/7'],
    ['model status', 'patch', '/admin/models/7/status'],
    ['model delete', 'delete', '/admin/models/7'],
    ['model alias delete', 'delete', '/admin/models/7/aliases/31'],
    ['model channel mapping', 'post', '/admin/channels/11/models'],
    ['model subscription mapping', 'post', '/admin/subscription-accounts/12/models'],
    ['model import', 'post', '/admin/models/import'],
    ['user status', 'post', '/user/disable/7'],
    ['subscription account edit', 'put', '/subscription-accounts/7'],
    ['subscription quota reset', 'post', '/subscription-accounts/7/reset-quota'],
    ['subscription batch quota reset', 'post', '/subscription-accounts/batch-reset-quota'],
    ['subscription revoke', 'post', '/v1/admin/subscriptions/7/revoke'],
    ['subscription group edit', 'put', '/v1/admin/subscription-groups/7'],
    ['subscription plan delete', 'delete', '/v1/admin/subscription-plans/7'],
    ['routing group edit', 'patch', '/v1/admin/routing-groups/7'],
    ['routing access', 'patch', '/v1/admin/routing-access/7'],
    ['routing billing policy', 'patch', '/v1/admin/routing-groups/7/billing'],
    ['system options', 'put', '/option/'],
    ['upstream cost delete', 'delete', '/admin/upstream-costs'],
    ['redemption delete', 'delete', '/redemption/test-code'],
    ['log cleanup', 'delete', '/log'],
    ['OAuth binding', 'post', '/v1/admin/accounts/subscription/oauth/codex/exchange'],
  ])('preserves the Chinese reason in the serialized %s request', async (_name, method, url) => {
    const client = new QueryClient();
    let received: Request | undefined;
    let body: Record<string, unknown> | undefined;
    server.use(http.all('/api/*', async ({ request }) => {
      received = request;
      if (method !== 'get' && method !== 'delete') body = await request.json() as Record<string, unknown>;
      return HttpResponse.json({ success: true });
    }));
    setWritePreparer(config => prepareManagedWrite(config, snapshot, client, () => reason));
    try {
      await adminApiClient.request({ url, method, ...(method !== 'get' && method !== 'delete' ? { data: { expected_revision: 3 } } : {}) });
      expect(received).toBeDefined();
      expect(received!.headers.has('x-authorization-reason')).toBe(false);
      expect(new URL(received!.url).searchParams.get('reason')).toBe(reason);
      if (method === 'get' || method === 'delete') expect(await received!.text()).toBe('');
      else expect(body).toMatchObject({ reason, expected_revision: url === '/option/' ? '3' : 3 });
    } finally { client.clear(); }
  });

  it.each(['post', 'patch', 'delete'] as const)('preserves Chinese IAM reasons on %s without invoking the business prompt', async (method) => {
    const client = new QueryClient();
    let received: Request | undefined;
    let body: Record<string, unknown> | undefined;
    server.use(http.all('/api/v1/admin/iam/*', async ({ request }) => {
      received = request;
      if (method !== 'delete') body = await request.json() as Record<string, unknown>;
      return HttpResponse.json({});
    }));
    setWritePreparer(config => prepareManagedWrite(config, snapshot, client, () => { throw new Error('unexpected prompt'); }));
    try {
      await writeIAM('/roles/7', method, { reason, expected_revision: '3' });
      expect(received!.headers.has('x-authorization-reason')).toBe(false);
      if (method === 'delete') {
        expect(new URL(received!.url).searchParams.get('reason')).toBe(reason);
        expect(new URL(received!.url).searchParams.get('expected_revision')).toBe('3');
        expect(await received!.text()).toBe('');
      } else expect(body).toMatchObject({ reason, expected_revision: '3' });
    } finally { client.clear(); }
  });

  it('preserves Chinese reasons when activating session roles through the user client', async () => {
    let body: Record<string, unknown> | undefined;
    server.use(http.put('/api/user/session/roles', async ({ request }) => {
      expect(request.headers.has('x-authorization-reason')).toBe(false);
      body = await request.json() as Record<string, unknown>;
      return HttpResponse.json({});
    }));
    await activateRoles(['7'], '3', reason);
    expect(body).toMatchObject({ role_ids: ['7'], expected_revision: '3', reason });
  });
});
