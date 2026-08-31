import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { LanguageToggle } from '@/components/LanguageToggle';
import { renderWithQuery } from '@/test/render';
import { AdminPagination } from './AdminPagination';

describe('AdminPagination', () => {
  it('switches controls and page-size options together', async () => {
    const user = userEvent.setup();

    renderWithQuery(
      <>
        <LanguageToggle />
        <AdminPagination
          page={2}
          pageSize={20}
          hasNextPage
          onPageChange={vi.fn()}
          onPageSizeChange={vi.fn()}
        />
      </>,
    );

    expect(screen.getByRole('button', { name: '上一页' })).toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: '每页行数' })).toHaveDisplayValue('每页：20');

    await user.click(screen.getByRole('button', { name: '切换至英文' }));

    expect(await screen.findByRole('button', { name: 'Previous' })).toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: 'Rows per page' })).toHaveDisplayValue('Per page: 20');
  });
});
