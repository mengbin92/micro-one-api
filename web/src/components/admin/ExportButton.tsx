import { Download } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { toCsv, type CsvColumn } from '@/lib/csv';

interface ExportButtonProps<T extends object> {
  filename: string;
  rows: T[];
  columns: Array<CsvColumn<T>>;
}

export function ExportButton<T extends object>({ filename, rows, columns }: ExportButtonProps<T>) {
  const handleExport = () => {
    const blob = new Blob([toCsv(rows, columns)], { type: 'text/csv;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    link.download = filename;
    link.click();
    URL.revokeObjectURL(url);
  };

  return (
    <Button type="button" variant="outline" size="sm" onClick={handleExport} disabled={rows.length === 0}>
      <Download className="size-3.5" />
      Export CSV
    </Button>
  );
}
