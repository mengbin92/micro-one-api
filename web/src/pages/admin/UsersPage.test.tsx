import { screen, within } from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import { MemoryRouter } from 'react-router';
import { describe, expect, it } from 'vitest';
import { AdminUsersPage } from './UsersPage';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';

describe('IAM user fields', () => {
  it('distinguishes authorized empty contacts, denied fields, and billing outages', async () => {
    server.use(
      http.get('/api/user/authorization', () => HttpResponse.json({ authorization_mode: 'iam', session: { activation_state: 'active' }, permitted_operations: ['identity.user.list'] })),
      http.get('/api/user', () => HttpResponse.json({ success: true, data: [
        { id: '1', username: 'visible', email: '', contactFieldsVisible: true, balance: '500', usedAmount: '100', status: 1 },
        { id: '2', username: 'restricted', email: '', contactFieldsVisible: false, status: 1 },
        { id: '3', username: 'unavailable', email: '', contactFieldsVisible: true, billingFieldsUnavailable: true, status: 1 },
        { id: '4', username: 'zero-balance', email: '', contactFieldsVisible: true, balance: '0', usedAmount: '1', status: 1 },
      ] })),
    );
    renderWithQuery(<MemoryRouter><AdminUsersPage /></MemoryRouter>);
    const visible = (await screen.findByText('visible')).closest('tr')!;
    expect(within(visible).queryByText('受限')).not.toBeInTheDocument();
    expect(within(visible).getAllByRole('cell')[3]).toHaveTextContent('—');
    expect(within(visible).getByText('0.0500')).toBeVisible();
    expect(within(visible).getByText('0.0100')).toBeVisible();
    const zero = screen.getByText('zero-balance').closest('tr')!;
    expect(within(zero).getByText('0.0000')).toBeVisible();
    expect(within(zero).getByText('0.0001')).toBeVisible();
    expect(within(zero).queryByText('受限')).not.toBeInTheDocument();
    expect(within(screen.getByText('restricted').closest('tr')!).getAllByText('受限')).toHaveLength(3);
    expect(within(screen.getByText('unavailable').closest('tr')!).getAllByText('暂不可用')).toHaveLength(2);
  });
});
