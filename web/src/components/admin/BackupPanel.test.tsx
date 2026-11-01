import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { http, HttpResponse } from 'msw';
import { describe, expect, it } from 'vitest';
import { renderWithQuery } from '@/test/render';
import { server } from '@/test/msw/server';
import { BackupPanel } from './BackupPanel';
const policy = { revision: 5, enabled: false, interval_hours: 24, retain: 7, rpo_hours: 24, rto_minutes: 60 };
function handlers() {
  server.use(http.get('/api/admin/backup-policy', () => HttpResponse.json(policy)), http.get('/api/admin/backups', () => HttpResponse.json({ jobs: [{ id: 'backup-receipt', status: 'uncertain', created_at: 1700000000, error_code: 'worker_interrupted' }], events: [], worker_last_seen: Math.floor(Date.now() / 1000), worker_phase: 'ready' })));
}
describe('BackupPanel', () => {
  it('preserves the displayed revision and reuses the request id on uncertain submission', async () => {
    handlers(); const user = userEvent.setup(); const saves: unknown[] = []; const ids: string[] = [];
    server.use(http.put('/api/admin/backup-policy', async ({ request }) => { saves.push(await request.json()); return HttpResponse.json({ ...policy, revision: 6 }); }), http.post('/api/admin/backups', async ({ request }) => { const body = await request.json() as { request_id: string }; ids.push(body.request_id); return ids.length === 1 ? HttpResponse.json({ message: 'network outcome unknown' }, { status: 503 }) : HttpResponse.json({ id: body.request_id, status: 'queued' }); }));
    renderWithQuery(<BackupPanel />);
    expect(await screen.findByText('结果不确定')).toBeInTheDocument();
    expect(await screen.findByText('备份 worker 已连接')).toBeInTheDocument();
    await user.type(screen.getByLabelText('操作原因'), '恢复演练');
    await user.clear(screen.getByLabelText('保留成功备份数')); await user.type(screen.getByLabelText('保留成功备份数'), '4');
    await user.click(screen.getByRole('button', { name: '保存备份策略' }));
    await waitFor(() => expect(saves).toEqual([{ policy: { ...policy, retain: 4 }, reason: '恢复演练' }]));
    await user.click(screen.getByRole('button', { name: '立即备份' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('network outcome unknown');
    await user.click(screen.getByRole('button', { name: '立即备份' }));
    await waitFor(() => expect(ids).toHaveLength(2)); expect(ids[1]).toBe(ids[0]);
    expect(await screen.findByText('任务已排队，备份完成后可查看结果。')).toBeInTheDocument();
  });
  it('shows an unavailable control plane without an empty-job success message', async () => {
    server.use(http.get('/api/admin/backup-policy', () => HttpResponse.json({ message: 'disabled' }, { status: 503 })), http.get('/api/admin/backups', () => HttpResponse.json({ message: 'disabled' }, { status: 503 })));
    renderWithQuery(<BackupPanel />);
    expect(await screen.findByRole('alert')).toHaveTextContent('备份管理未就绪');
    expect(screen.queryByText('暂无备份任务')).not.toBeInTheDocument();
  });
});
