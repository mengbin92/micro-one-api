import { describe, expect, it } from 'vitest';
import { getNextSortDirection, sortRows } from './table-utils';

describe('table utils', () => {
  it('cycles sort direction through asc desc and none', () => {
    expect(getNextSortDirection(null)).toBe('asc');
    expect(getNextSortDirection('asc')).toBe('desc');
    expect(getNextSortDirection('desc')).toBe(null);
  });

  it('sorts strings and numbers without mutating input rows', () => {
    const rows = [
      { name: 'beta', quota: 20 },
      { name: 'Alpha', quota: 10 },
    ];

    expect(sortRows(rows, { key: 'name', direction: 'asc' }).map((row) => row.name)).toEqual(['Alpha', 'beta']);
    expect(sortRows(rows, { key: 'quota', direction: 'desc' }).map((row) => row.quota)).toEqual([20, 10]);
    expect(rows.map((row) => row.name)).toEqual(['beta', 'Alpha']);
  });
});
