export interface CsvColumn<T extends object> {
  key: keyof T;
  label: string;
}

export function toCsv<T extends object>(rows: T[], columns: Array<CsvColumn<T>>) {
  const escapeCell = (value: unknown) => {
    let text = String(value ?? '');
    if (typeof value === 'string' && /^\s*[=+@-]/.test(value) && !/^-(?:\d+(?:\.\d*)?|\.\d+)(?:e[+-]?\d+)?$/i.test(value.trim())) text = `'${text}`;
    return `"${text.replaceAll('"', '""')}"`;
  };

  return [
    columns.map((column) => escapeCell(column.label)).join(','),
    ...rows.map((row) => columns.map((column) => escapeCell(row[column.key])).join(',')),
  ].join('\n');
}
