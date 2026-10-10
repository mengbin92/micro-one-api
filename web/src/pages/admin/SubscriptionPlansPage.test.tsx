import { screen } from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import { MemoryRouter } from 'react-router';
import { expect, it } from 'vitest';
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
});
