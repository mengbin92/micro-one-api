import { Download } from 'lucide-react';
import { Button, buttonVariants } from '@/components/ui/button';
import { adminApiClient } from '@/lib/api';
import { toCsv, type CsvColumn } from '@/lib/csv';

interface ExportButtonProps<T extends object> {
  filename: string;
  rows?: T[];
  columns?: Array<CsvColumn<T>>;
  href?: string;
  label?: string;
}

export function ExportButton<T extends object>({ filename, rows, columns, href, label = 'Export CSV' }: ExportButtonProps<T>) {
  const downloadBlob = (blob: Blob) => {
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    link.download = filename;
    link.click();
    URL.revokeObjectURL(url);
  };

  const handleExport = async () => {
    if (href) {
      const response = await adminApiClient.get(href, { responseType: 'blob' });
      downloadBlob(new Blob([response.data], { type: 'text/csv;charset=utf-8' }));
      return;
    }
    if (!rows || !columns) return;
    downloadBlob(new Blob([toCsv(rows, columns)], { type: 'text/csv;charset=utf-8' }));
  };

  if (href) {
    return (
      <button type="button" onClick={handleExport} className={buttonVariants({ variant: 'outline', size: 'sm' })}>
        <Download className="size-3.5" />
        {label}
      </button>
    );
  }

  return (
    <Button type="button" variant="outline" size="sm" onClick={handleExport} disabled={!rows || !columns || rows.length === 0}>
      <Download className="size-3.5" />
      {label}
    </Button>
  );
}
