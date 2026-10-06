import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router';
import { http, HttpResponse } from 'msw';
import { describe, expect, it, vi } from 'vitest';
import { IAMPage } from './IAMPage';
import { server } from '@/test/msw/server';
import { renderWithQuery } from '@/test/render';
import type { IAMRequest } from '@/lib/iam-types';

function authorize(operations: string[]) {
  server.use(http.get('/api/user/authorization', () => HttpResponse.json({ authorization_mode: 'iam', session: { activation_state: 'active' }, permitted_operations: operations, versions: { policy_revision: '7' }, valid_until: new Date(Date.now() + 60_000).toISOString() })));
}

describe('IAM independent reads and metadata', () => {
  it('reads sessions with the read operation without requiring revoke', async () => {
    authorize(['identity.user_role.read', 'iam.authorization.user.read']);
    const sessions = vi.fn();
    server.use(
      http.get('/api/v1/admin/iam/users/20/roles', () => HttpResponse.json({ assignments: [], total: '0' })),
      http.get('/api/v1/admin/iam/users/20/sessions', () => { sessions(); return HttpResponse.json({ sessions: [{ session_id: 'target-jti', user_id: '20' }], target_revision: '3' }); }),
    );
    renderWithQuery(<MemoryRouter initialEntries={['/admin/iam/assignments?user_id=20']}><IAMPage /></MemoryRouter>);
    await screen.findByText('用户会话');
    expect(sessions).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole('button', { name: '确认撤销列出的用户会话' })).not.toBeInTheDocument();
  });

  it('submits permission description and numeric sort with the metadata mask', async () => {
    authorize(['iam.permission.list', 'iam.permission.metadata.update', 'iam.authorization.simulate']);
    let body: IAMRequest | undefined;
    server.use(
      http.get('/api/v1/admin/iam/permissions', () => HttpResponse.json({ permissions: [{ id: '99', resource_id: '2', code: 'channel.channel.read', name: 'Read channels', category: 'channel', status: 'enabled', revision: '3', description: 'Original', sort: 2 }], total: '1' })),
      http.post('/api/v1/admin/iam/authorization:simulate', async ({ request }) => { body = await request.json() as IAMRequest; return HttpResponse.json({ base_policy_revision: '7', content_digest: 'review-digest' }); }),
    );
    renderWithQuery(<MemoryRouter initialEntries={['/admin/iam/permissions']}><IAMPage /></MemoryRouter>);
    await userEvent.click(await screen.findByRole('button', { name: '详情' }));
    await userEvent.clear(screen.getByRole('textbox', { name: '说明' }));
    await userEvent.type(screen.getByRole('textbox', { name: '说明' }), 'Updated description');
    await userEvent.clear(screen.getByRole('textbox', { name: '排序' }));
    await userEvent.type(screen.getByRole('textbox', { name: '排序' }), '-10');
    await userEvent.click(screen.getByRole('button', { name: '保存目录资料' }));
    await userEvent.type(screen.getByRole('textbox', { name: '变更原因' }), 'Review metadata');
    await userEvent.click(screen.getByRole('button', { name: '预检变更' }));
    await waitFor(() => expect(body?.permission?.description).toBe('Updated description'));
    expect(body?.permission?.sort).toBe(-10);
    expect(body?.update_mask).toBe('name,description,sort,category,risk_level');
    expect(body?.expected_revision).toBe('3');
  });
});
