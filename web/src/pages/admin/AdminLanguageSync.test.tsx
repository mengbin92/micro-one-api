import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { MemoryRouter } from 'react-router';
import { describe, expect, it } from 'vitest';
import { LanguageToggle } from '@/components/LanguageToggle';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';
import { AdminChannelsPage } from './ChannelsPage';
import { AdminUsersPage } from './UsersPage';

describe('admin language synchronization', () => {
  it('switches the user management title and content together', async () => {
    server.use(
      http.get('/api/user', () => HttpResponse.json({ success: true, data: [] })),
    );
    const user = userEvent.setup();

    renderWithQuery(
      <MemoryRouter>
        <LanguageToggle />
        <AdminUsersPage />
      </MemoryRouter>,
    );

    expect(await screen.findByRole('heading', { name: '用户管理' })).toBeInTheDocument();
    expect(await screen.findByText('未找到用户')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: '切换至英文' }));

    expect(await screen.findByRole('heading', { name: 'User Management' })).toBeInTheDocument();
    expect(await screen.findByText('No users found')).toBeInTheDocument();
  });

  it('switches the channel management title and content together', async () => {
    server.use(
      http.get('/api/channel', () => HttpResponse.json({ success: true, data: [] })),
    );
    const user = userEvent.setup();

    renderWithQuery(
      <MemoryRouter>
        <LanguageToggle />
        <AdminChannelsPage />
      </MemoryRouter>,
    );

    expect(await screen.findByRole('heading', { name: '渠道管理' })).toBeInTheDocument();
    expect(await screen.findByText('未找到渠道')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: '切换至英文' }));

    expect(await screen.findByRole('heading', { name: 'Channel Management' })).toBeInTheDocument();
    expect(await screen.findByText('No channels found')).toBeInTheDocument();
  });
});
