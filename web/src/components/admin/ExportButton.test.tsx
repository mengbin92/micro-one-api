import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { ExportButton } from './ExportButton';

describe('ExportButton', () => {
  it('is disabled when there are no rows', () => {
    render(<ExportButton filename="empty.csv" rows={[]} columns={[{ key: 'name', label: 'Name' }]} />);

    expect(screen.getByRole('button', { name: /export csv/i })).toBeDisabled();
  });
});
