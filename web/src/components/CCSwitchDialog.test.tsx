import { screen } from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import { describe, expect, it, vi } from 'vitest';
import { CCSwitchDialog } from './CCSwitchDialog';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';

describe('CCSwitchDialog', () => {
  it('preserves the platform API key without adding an sk- prefix', async () => {
    const tokenKey = 'TEST-PLATFORM-KEY';
    server.use(
      http.get('/api/status', () =>
        HttpResponse.json({ success: true, data: { server_address: 'https://api.example.com' } }),
      ),
    );

    renderWithQuery(
      <CCSwitchDialog
        open
        onOpenChange={vi.fn()}
        tokenKey={tokenKey}
        modelOptions={['deepseek-v4-flash-0731']}
      />,
    );

    expect(await screen.findByLabelText(/API Key/)).toHaveValue(tokenKey);
  });
});
