import { screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { describe, expect, it } from 'vitest';
import { UserRoutingAccess } from './UserRoutingAccess';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';

describe('UserRoutingAccess', () => {
  it('explains the target user’s access sources and effective price', async () => {
    server.use(
      http.get('/api/v1/admin/routing-access/9', () => HttpResponse.json({
        success: true,
        data: {
          default_routing_group_id: 2,
          revision: 4,
          public_group_access: 'explicit_only',
          tokens: [],
          grants: [{ routing_group_id: 2, source_type: 'admin', source_ref: 'manual', starts_at: 0, expires_at: 0, status: 'active' }],
        },
      })),
      http.get('/api/v1/admin/routing-groups', () => HttpResponse.json({
        success: true,
        data: { groups: [{ id: 2, key: 'vip', display_name: 'VIP', status: 'enabled' }], next_page_token: '' },
      })),
      http.get('/api/v1/admin/routing-access/9/available', () => HttpResponse.json({
        success: true,
        data: {
          groups: [{
            id: 2,
            key: 'vip',
            display_name: 'VIP',
            price_ratio: 0.8,
            price_source: 'user_routing_price_override',
            price_version: 'user_routing_price:2:9:1',
            billing_mode: 'subscription_first',
            subscription_covered: true,
            models: ['model-vip'],
            sources: [{ routing_group_id: 2, source_type: 'admin', source_ref: 'manual', starts_at: 0, expires_at: 0, status: 'active' }],
          }],
          facts: { default_routing_group_id: 2, revision: 4, public_group_access: 'explicit_only', grants: [] },
          default_available: true,
          creation_enabled: true,
          next_page_token: '',
        },
      })),
    );

    renderWithQuery(<UserRoutingAccess userId="9" />);
    await userEvent.click(await screen.findByRole('button', { name: '分组授权' }));
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText('当前可用分组与有效价格')).toBeVisible();
    expect(within(dialog).getAllByText(/管理员手动授权/).at(0)).toBeVisible();
    expect(within(dialog).getByText(/有效倍率：×0\.8/)).toBeVisible();
    expect(within(dialog).getByText(/用户专属倍率（替换基础倍率）/)).toBeVisible();
    expect(within(dialog).getByText(/订阅优先，余额补足/)).toBeVisible();
    expect(within(dialog).getByText(/实际扣费以请求预扣时冻结的快照为准/)).toBeVisible();
  });
});
