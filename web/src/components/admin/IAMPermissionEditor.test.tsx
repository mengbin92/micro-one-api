import { useState } from 'react';
import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { IAMPermissionEditor } from './IAMPermissionEditor';
import type { IAMGrant } from '@/lib/iam-types';
import { renderWithQuery } from '@/test/render';
function Editor() { const [grants, setGrants] = useState<IAMGrant[]>([]); return <IAMPermissionEditor permissions={[{ code: 'channel.channel.read', name: 'read', binding: 'bound', status: 'enabled', supported_scopes: ['all'] }, { code: 'channel.channel.delete', name: 'delete', binding: 'unbound', status: 'draft' }]} grants={grants} onChange={setGrants} sources={[{ operation: 'channel.channel.read', role_id: '51', inheritance_path: ['52', '51'], active: true }]} />; }
describe('permission editor', () => {
 it('shares edits across tree and matrix, separates inherited source, disables unbound entries', () => {
  renderWithQuery(<Editor />);
  fireEvent.change(screen.getByLabelText('channel.channel.read 直接授权'), { target: { value: 'deny' } });
  expect(screen.getByLabelText('channel.channel.delete 直接授权')).toBeDisabled();
  fireEvent.click(screen.getByRole('button', { name: '资源 × 操作矩阵' }));
  expect(screen.getByLabelText('channel.channel.read 直接授权')).toHaveValue('deny'); expect(screen.getAllByText(/52 → 51/)[0]).toBeVisible();
  fireEvent.change(screen.getByLabelText('channel.channel.read 直接授权'), { target: { value: 'allow+deny' } });
  expect(screen.getByRole('button', { name: '编辑允许范围' })).toBeVisible();
  fireEvent.click(screen.getByRole('button', { name: '编辑拒绝范围' })); fireEvent.change(screen.getByLabelText('范围描述'), { target: { value: '{invalid' } }); fireEvent.click(screen.getByRole('button', { name: '应用范围' })); expect(screen.getByRole('alert')).toHaveTextContent('合法范围');
 });
});
