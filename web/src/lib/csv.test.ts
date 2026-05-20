import { describe, expect, it } from 'vitest';
import { toCsv } from './csv';

describe('toCsv', () => {
  it('keeps headers in configured order', () => {
    const csv = toCsv([{ name: 'Alice', id: 1 }], [
      { key: 'id', label: 'ID' },
      { key: 'name', label: 'Name' },
    ]);

    expect(csv.split('\n')[0]).toBe('"ID","Name"');
  });

  it('escapes commas quotes and empty values', () => {
    const csv = toCsv(
      [{ name: 'Alice, "Admin"', email: '', note: null }],
      [
        { key: 'name', label: 'Name' },
        { key: 'email', label: 'Email' },
        { key: 'note', label: 'Note' },
      ]
    );

    expect(csv).toBe('"Name","Email","Note"\n"Alice, ""Admin""","",""');
  });
});
