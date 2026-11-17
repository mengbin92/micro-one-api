import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { MemoryRouter } from 'react-router';
import { expect, it, vi } from 'vitest';
import { AdminSubscriptionPlansPage } from './SubscriptionPlansPage';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';

it('loads plans for a session with subscription plan list permission', async () => {
  server.use(
    http.get('/api/user/authorization', () => HttpResponse.json({
      authorization_mode: 'iam', session: { activation_state: 'active' },
      permitted_operations: ['subscription.plan.list'],
    })),
    http.get('/api/v1/admin/subscription-plans', () => HttpResponse.json({ success: true, data: [{
      id: 1, name: 'Monthly', product_name: 'Pro', group_id: 1,
      price_quota: 10, validity_days: 30, validity_unit: 'day', for_sale: true,
    }] })),
  );
  renderWithQuery(<MemoryRouter><AdminSubscriptionPlansPage /></MemoryRouter>);
  expect(await screen.findByText('Monthly')).toBeInTheDocument();
  expect(screen.getByText('¥10.00')).toBeInTheDocument();
  expect(screen.getByText('价格 (CNY)')).toBeInTheDocument();
});

it('requires selecting a quota policy and submits the selected option', async () => {
  const save = vi.fn();
  server.use(
    http.get('/api/v1/admin/subscription-plans', () => HttpResponse.json({ success: true, data: [] })),
    http.get('/api/v1/admin/subscription-groups', () => HttpResponse.json({ success: true, data: [{
      id: 7, name: 'pro', display_name: 'Pro quota', daily_limit_usd: 10, weekly_limit_usd: null, monthly_limit_usd: 100,
    }] })),
    http.get('/api/v1/admin/routing-groups', () => HttpResponse.json({ success: true, data: { groups: [], next_page_token: '' } })),
    http.post('/api/v1/admin/subscription-plans', async ({ request }) => {
      save(await request.json());
      return HttpResponse.json({ success: true });
    }),
  );
  renderWithQuery(<MemoryRouter><AdminSubscriptionPlansPage /></MemoryRouter>);
  await userEvent.click(await screen.findByRole('button', { name: '新建套餐' }));
  await screen.findByRole('option', { name: 'Pro quota (#7)' });
  await userEvent.type(screen.getByLabelText('套餐名称'), 'New monthly');
  expect(screen.getByLabelText('额度策略')).toBeInvalid();
  await userEvent.click(screen.getByRole('button', { name: '保存' }));
  expect(save).not.toHaveBeenCalled();

  await userEvent.selectOptions(screen.getByLabelText('额度策略'), '7');
  expect(screen.getByText('额度: 10 / 不限 / 100 (日/周/月 USD)')).toBeInTheDocument();
  await userEvent.click(screen.getByRole('button', { name: '保存' }));
  await waitFor(() => expect(save).toHaveBeenCalledWith(expect.objectContaining({ group_id: 7 })));
});

it('keeps an existing policy selected when it is outside the policy list scope', async () => {
  server.use(
    http.get('/api/v1/admin/subscription-plans', () => HttpResponse.json({ success: true, data: [{
      id: 1, name: 'Monthly', product_name: 'Pro', group_id: 9,
      price_quota: 10, validity_days: 30, validity_unit: 'day', for_sale: false,
    }] })),
    http.get('/api/v1/admin/subscription-groups', () => HttpResponse.json({ success: true, data: [{ id: 7, name: 'pro', display_name: 'Pro quota' }] })),
    http.get('/api/v1/admin/routing-groups', () => HttpResponse.json({ success: true, data: { groups: [], next_page_token: '' } })),
  );
  renderWithQuery(<MemoryRouter><AdminSubscriptionPlansPage /></MemoryRouter>);
  const row = (await screen.findByText('Monthly')).closest('tr')!;
  await userEvent.click(row.querySelector('button')!);
  expect(screen.getByLabelText('额度策略')).toHaveValue('9');
  expect(screen.getByRole('option', { selected: true })).toHaveTextContent('#9');
});
