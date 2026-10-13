import { fireEvent, screen } from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import { describe, expect, it } from 'vitest';
import { UsagePage } from './UsagePage';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';

describe('native usage v2 display', () => {
  for (const status of ['verified', 'missing', 'invalid', 'unsupported']) {
    it(`keeps raw totals separate for ${status} zero financial usage`, async () => {
      server.use(http.get('/api/user/logs', () => HttpResponse.json({ success: true, data: { logs: [{
        id: 'native-v2', type: 'consume', amount: 0, balance_after: 10, created_at: 1,
        model_name: 'native-v2-fixture', prompt_tokens: 42, reported_total_tokens: 42,
        billable_total_tokens: 0, usage_contract_version: 2, usage_parse_status: status,
      }], total: 1 } })));
      renderWithQuery(<UsagePage />);
      const row = (await screen.findByText('native-v2-fixture')).closest('tr');
      const detail = row?.querySelector('[tabindex="0"]');
      expect(detail).not.toBeNull();
      fireEvent.mouseEnter(detail!);
      expect(await screen.findByText('上游上报总 Token')).toBeInTheDocument();
    });
  }
});
