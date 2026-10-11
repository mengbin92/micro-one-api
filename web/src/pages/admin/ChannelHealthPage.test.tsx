import { screen } from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import { MemoryRouter } from 'react-router';
import { expect, it, vi } from 'vitest';
import { ChannelHealthPage } from './ChannelHealthPage';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';

it('preserves local subscription quota precision in channel health', async () => {
  server.use(
    http.get('/api/channel', () => HttpResponse.json({ success: true, data: [] })),
    http.get('/api/subscription-accounts', () => HttpResponse.json({ accounts: [{ id: 1, name: 'Precise quota', platform: 'claude', status: 1,
      quotaDailyUsedUsd: 0.000002, quotaDailyLimitUsd: 1.0249, quotaDailyWindowStart: Math.floor(Date.now() / 1000) }], total: 1 })),
  );
  renderWithQuery(<MemoryRouter><ChannelHealthPage /></MemoryRouter>);

  expect(await screen.findByText('$0.000002 / $1.0249')).toBeInTheDocument();
});

it('keeps server-rolled total and fixed daily quota usage', async () => {
  vi.spyOn(Date, 'now').mockReturnValue(Date.parse('2026-11-02T04:30:00Z'));
  server.use(
    http.get('/api/channel', () => HttpResponse.json({ success: true, data: [] })),
    http.get('/api/subscription-accounts', () => HttpResponse.json({ accounts: [{ id: 1, name: 'Fixed quota', platform: 'claude', status: 1,
      quotaUsedUsd: 2, quotaLimitUsd: 10, quotaResetStrategy: 'fixed', quotaTimezone: 'America/New_York',
      quotaDailyUsedUsd: 3, quotaDailyLimitUsd: 10, quotaDailyWindowStart: Date.parse('2026-11-01T04:00:00Z') / 1000 }], total: 1 })),
  );
  renderWithQuery(<MemoryRouter><ChannelHealthPage /></MemoryRouter>);

  expect(await screen.findByText('$2.00 / $10.00')).toBeInTheDocument();
  expect(screen.getByText('$3.00 / $10.00')).toBeInTheDocument();
});
