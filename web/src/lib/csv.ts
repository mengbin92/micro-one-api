export interface CsvColumn<T extends object> {
  key: keyof T;
  label: string;
}

export function toCsv<T extends object>(rows: T[], columns: Array<CsvColumn<T>>) {
  const escapeCell = (value: unknown) => `"${String(value ?? '').replaceAll('"', '""')}"`;

  return [
    columns.map((column) => escapeCell(column.label)).join(','),
    ...rows.map((row) => columns.map((column) => escapeCell(row[column.key])).join(',')),
  ].join('\n');
}
