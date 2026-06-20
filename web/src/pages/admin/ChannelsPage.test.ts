import { describe, expect, it } from 'vitest';
import { summarizeChannelHealth } from './channel-health-summary';

describe('summarizeChannelHealth', () => {
  it('counts unavailable and degraded active channel health states', () => {
    const summary = summarizeChannelHealth([
      { id: '1', name: 'ok', type: 1, status: 1, baseUrl: '', group: 'default', models: '', priority: '0', weight: 1, balance: 0, balanceUpdatedTime: '', usedQuota: '', healthStatus: 'healthy' },
      { id: '2', name: 'down', type: 1, status: 1, baseUrl: '', group: 'default', models: '', priority: '0', weight: 1, balance: 0, balanceUpdatedTime: '', usedQuota: '', health_status: 'unavailable' },
      { id: '3', name: 'slow', type: 1, status: 1, baseUrl: '', group: 'default', models: '', priority: '0', weight: 1, balance: 0, balanceUpdatedTime: '', usedQuota: '', healthStatus: 'degraded' },
    ]);

    expect(summary.unhealthy).toHaveLength(2);
    expect(summary.unavailable.map((channel) => channel.name)).toEqual(['down']);
    expect(summary.degraded.map((channel) => channel.name)).toEqual(['slow']);
    expect(summary.primary?.name).toBe('down');
  });

  it('uses degraded as the primary alert when no channel is unavailable', () => {
    const summary = summarizeChannelHealth([
      { id: '1', name: 'slow', type: 1, status: 1, baseUrl: '', group: 'default', models: '', priority: '0', weight: 1, balance: 0, balanceUpdatedTime: '', usedQuota: '', healthStatus: 'degraded' },
    ]);

    expect(summary.unavailable).toHaveLength(0);
    expect(summary.primary?.name).toBe('slow');
  });
});
