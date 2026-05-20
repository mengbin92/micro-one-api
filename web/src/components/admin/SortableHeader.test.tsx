import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { SortableHeader } from './SortableHeader';

describe('SortableHeader', () => {
  it('cycles sort state when activated', async () => {
    const user = userEvent.setup();
    const onSortChange = vi.fn();

    render(
      <SortableHeader columnKey="name" sort={{ key: null, direction: null }} onSortChange={onSortChange}>
        Name
      </SortableHeader>
    );

    await user.click(screen.getByRole('button', { name: /sort by name/i }));

    expect(onSortChange).toHaveBeenCalledWith({ key: 'name', direction: 'asc' });
  });
});
