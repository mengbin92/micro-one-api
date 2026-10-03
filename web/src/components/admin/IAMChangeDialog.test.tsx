import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { describe, expect, it, vi } from 'vitest';
import { IAMChangeDialog } from './IAMChangeDialog';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';
import type { IAMRequest } from '@/lib/iam-types';
const auth = { authorization_mode: 'iam', session: { activation_state: 'active' }, versions: { policy_revision: '9' }, permitted_operations: ['iam.role.update', 'iam.authorization.simulate'] };
describe('same-version preview commit', () => {
 it('retains reviewed target CAS and preview digest, then requires reloading after 409', async () => {
  const committed: IAMRequest[] = []; const previewed: IAMRequest[] = [];
  server.use(http.get('/api/user/authorization', () => HttpResponse.json(auth)), http.post('/api/v1/admin/iam/authorization:simulate', async ({ request }) => { previewed.push(await request.json() as IAMRequest); return HttpResponse.json({ base_policy_revision: '9', content_digest: 'reviewed-digest', affected_user_ids: ['22'], conflicts: [] }); }), http.patch('/api/v1/admin/iam/roles/51', async ({ request }) => { committed.push(await request.json() as IAMRequest); return HttpResponse.json({ message: 'revision changed' }, { status: 409 }); }));
  const user = userEvent.setup(); const closed = vi.fn();
  renderWithQuery(<IAMChangeDialog change={{ title: '角色变更', path: '/roles/51', method: 'patch', rpc: 'UpdateRole', permission: 'iam.role.update', request: { id: '51', expected_revision: '3', role: { name: 'new name' }, update_mask: 'name' } }} onClose={closed} />);
  expect(screen.getByRole('button', { name: '确认提交' })).toBeDisabled();
  await user.type(screen.getByLabelText('变更原因'), 'reviewed change');
  await waitFor(() => expect(screen.getByRole('button', { name: '预检变更' })).toBeEnabled());
  await user.click(screen.getByRole('button', { name: '预检变更' })); await waitFor(() => expect(screen.getByRole('button', { name: '确认提交' })).toBeEnabled());
  await user.click(screen.getByRole('button', { name: '确认提交' })); await screen.findByText('版本冲突，请刷新数据并重新预检。');
  expect(committed[0]).toMatchObject({ ...previewed[0], expected_revision: '3', base_policy_revision: '9', content_digest: 'reviewed-digest' });
  expect(screen.getByRole('button', { name: '确认提交' })).toBeDisabled(); expect(screen.getByRole('button', { name: '预检变更' })).toBeDisabled();
  await user.click(screen.getByRole('button', { name: '关闭并重新加载后预检' })); expect(closed).toHaveBeenCalledTimes(1); expect(committed).toHaveLength(1);
 });
});
