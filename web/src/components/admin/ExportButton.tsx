import { useState } from 'react';
import { Download } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { adminApiClient } from '@/lib/api';
import { useAuthorization } from '@/lib/authorization';
import { getApiErrorMessage } from '@/lib/api-error';
import { toCsv, type CsvColumn } from '@/lib/csv';
interface ExportButtonProps<T extends object> {
  filename: string; rows?: T[]; columns?: Array<CsvColumn<T>>; href?: string; label?: string; permission?: string | readonly string[];
}
function ExportDownload<T extends object>({ filename, rows, columns, href, label = 'Export CSV' }: ExportButtonProps<T>) {
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const download = async () => {
    setPending(true); setError('');
    try {
      const blob = href ? new Blob([(await adminApiClient.get(href, { responseType: 'blob' })).data], { type: 'text/csv;charset=utf-8' }) : new Blob([toCsv(rows ?? [], columns ?? [])], { type: 'text/csv;charset=utf-8' });
      const url = URL.createObjectURL(blob); const link = document.createElement('a'); link.href = url; link.download = filename; link.click(); URL.revokeObjectURL(url);
    } catch (err) { setError(getApiErrorMessage(err)); }
    finally { setPending(false); }
  };
  return <><Button variant="outline" size="sm" disabled={pending || (!href && (!rows?.length || !columns))} onClick={() => void download()}><Download className="size-3.5" />{label}</Button>{error && <span role="alert">{error}</span>}</>;
}

function AuthorizedExport<T extends object>(props: ExportButtonProps<T>) {
 const auth = useAuthorization();
 return (typeof props.permission === 'string' ? auth.can(props.permission) : auth.canAll(props.permission ?? [])) ? <ExportDownload {...props} /> : null;
}
export function ExportButton<T extends object>(props: ExportButtonProps<T>) {
 return props.permission ? <AuthorizedExport {...props} /> : <ExportDownload {...props} />;
}
