import { screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { MemoryRouter } from 'react-router';
import { expect, it } from 'vitest';
import { AdminReconciliationPage } from './ReconciliationPage';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';

it('shows wallet and subscription differences precisely while preserving the CNY units of old refund runs', async () => {
  const run = { run_id: 1, run_at: 1, expired_cleaned: 0, total_accounts: 1, total_channels: 0, total_reservations: 0, discrepancy_count: 4 };
  server.use(
    http.get('/api/user/authorization', () => HttpResponse.json({ authorization_mode: 'iam', session: { activation_state: 'active' }, permitted_operations: ['billing.reconciliation.read'] })),
    http.get('/api/reconciliation', () => HttpResponse.json({ success: true, data: { runs: [run], total: 1 } })),
    http.get('/api/reconciliation/1', () => HttpResponse.json({ success: true, data: { ...run, discrepancies: [
      { type: 'refund_reversal', trade_no: 'NEW', expected_quota: 10000000, actual_quota: 9999999, difference: 1, refunded_order_money_cents: 10000, reversal_ledger_amount: 9999999, money_cents_diff: 0 },
      { type: 'refund_reversal', refunded_order_money_cents: 10000, reversal_ledger_amount: 90, money_cents_diff: 1000, expected_quota: 0, actual_quota: 0, difference: 0 },
      { type: 'subscription_absorption', subscription_id: 2, ledger_subscription_cost: 10, subscription_used_usd: 0.0005, subscription_difference: 0.0005 },
      { type: 'refund_reversal', trade_no: 'DUPLICATE', expected_quota: 10000000, actual_quota: 10000000, difference: 0, refunded_order_count: 1, reversal_ledger_count: 2 },
    ] } })),
  );
  renderWithQuery(<MemoryRouter><AdminReconciliationPage /></MemoryRouter>);
  await userEvent.click(await screen.findByRole('button', { name: '查看对账 1' }));
  const newRow = (await screen.findByText('NEW')).closest('tr') as HTMLElement;
  expect(within(newRow).getByText('$1000.0000')).toBeInTheDocument();
  expect(within(newRow).getByText('$999.9999')).toBeInTheDocument();
  expect(within(newRow).getByText('$0.0001')).toBeInTheDocument();
  const oldRow = screen.getByText('退款订单').closest('tr') as HTMLElement;
  expect(within(oldRow).getByText('¥100.00')).toBeInTheDocument();
  expect(within(oldRow).getByText('¥10.00')).toBeInTheDocument();
  const subscriptionRow = screen.getByText('订阅 2').closest('tr') as HTMLElement;
  expect(within(subscriptionRow).getByText('$0.0010')).toBeInTheDocument();
  expect(within(subscriptionRow).getAllByText('$0.0005')).toHaveLength(2);
  const duplicateRow = screen.getByText('DUPLICATE').closest('tr') as HTMLElement;
  expect(within(duplicateRow).getByText('退款订单 1 条 / 冲正账本 2 条')).toBeInTheDocument();
});
