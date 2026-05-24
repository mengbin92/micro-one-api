import { ShieldCheck } from 'lucide-react';
import { useState } from 'react';
import { Outlet } from 'react-router-dom';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';

export function AdminRoute() {
  const adminToken = localStorage.getItem('adminToken');
  const [token, setToken] = useState('');

  if (!adminToken) {
    return (
      <div className="mx-auto flex min-h-[60vh] max-w-md items-center justify-center">
        <Card className="w-full rounded-lg border-0 bg-white shadow-sm ring-1 ring-slate-200 dark:bg-card dark:ring-white/10">
          <CardHeader>
            <div className="mb-2 grid size-11 place-items-center rounded-lg bg-slate-950 text-white dark:bg-white dark:text-slate-950">
              <ShieldCheck className="size-5" />
            </div>
            <CardTitle>管理员访问</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <Input
              type="password"
              aria-label="Admin Token"
              placeholder="Admin Token"
              value={token}
              onChange={(event) => setToken(event.target.value)}
            />
            <Button
              className="w-full"
              disabled={!token.trim()}
              onClick={() => {
                localStorage.setItem('adminToken', token.trim());
                toast.success('Admin access enabled');
                window.location.reload();
              }}
            >
              进入管理后台
            </Button>
          </CardContent>
        </Card>
      </div>
    );
  }

  return <Outlet />;
}
