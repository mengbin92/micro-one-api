import { useState } from 'react';
import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { describe, expect, it } from 'vitest';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';
import { CoverageEditor, ContractSummary, type RoutingCoverage } from './SubscriptionContract';
function Editor() {
  const [coverage, setCoverage] = useState<RoutingCoverage[]>([]);
  return <><CoverageEditor value={coverage} onChange={setCoverage} /><output data-testid="coverage">{JSON.stringify(coverage)}</output></>;
}
describe('Subscription contract coverage', () => {
  it('keeps fee coverage separate from an access grant', async () => {
    server.use(http.get('/api/v1/admin/routing-groups', () => HttpResponse.json({ success: true, data: { groups: [{ id: 9, key: 'vip', display_name: 'VIP' }], next_page_token: '' } })));
    renderWithQuery(<Editor />);
    await userEvent.click(await screen.findByRole('checkbox', { name: 'VIP (vip)' }));
    expect(JSON.parse(screen.getByTestId('coverage').textContent!)).toEqual([{ routing_group_id: 9, routing_group_key: 'vip', grants_access: false }]);
    await userEvent.click(screen.getByRole('checkbox', { name: '授予访问权' }));
    expect(JSON.parse(screen.getByTestId('coverage').textContent!)[0].grants_access).toBe(true);
    await userEvent.click(screen.getByRole('checkbox', { name: 'VIP (vip)' }));
    expect(screen.getByTestId('coverage')).toHaveTextContent('[]');
  });
  it('labels legacy contracts without granting new access', () => {
    renderWithQuery(<ContractSummary />);
    expect(screen.getByText(/旧版合同：覆盖已授权组，不授予访问权/)).toBeInTheDocument();
  });
});
