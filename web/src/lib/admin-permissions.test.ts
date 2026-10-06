import { describe, expect, it } from 'vitest';
import { configuredMenuItems } from './admin-permissions';
import type { IAMMenu } from './iam-types';

describe('configured menu hierarchy', () => {
  const menus: IAMMenu[] = [
    { id: '1', parent_id: '0', name: 'Parent', route_key: 'settings', icon_key: 'settings', enabled: true, sort: 0, required_all: ['system.option.read'] },
    { id: '2', parent_id: '1', name: 'Channels', route_key: 'channels', icon_key: 'server', enabled: true, sort: 10 },
    { id: '3', parent_id: '1', name: 'Users', route_key: 'users', icon_key: 'users', enabled: true, sort: 2 },
  ];
  it('shows parents as labels, sorts children numerically and does not grant sibling routes', () => {
    const items = configuredMenuItems(menus, op => op === 'channel.channel.list');
    expect(items).toEqual([
      { id: '1', label: 'Parent', iconKey: 'settings', depth: 0, to: undefined },
      { id: '2', label: 'Channels', iconKey: 'server', depth: 1, to: '/admin/channels' },
    ]);
    expect(configuredMenuItems(menus, () => true).map(item => item.id)).toEqual(['1', '3', '2']);
  });
  it('hides disabled subtrees and rejects unregistered route keys', () => {
    expect(configuredMenuItems([{ ...menus[0], enabled: false }, menus[1]], () => true)).toEqual([]);
    expect(configuredMenuItems([{ ...menus[0], route_key: 'https://unregistered.example' }], () => true)).toEqual([]);
  });
});
