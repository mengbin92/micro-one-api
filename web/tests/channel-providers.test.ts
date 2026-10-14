import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { expect, it } from 'vitest';
import { CHANNEL_CREATABLE_PROVIDERS, CHANNEL_PROVIDER_NAMES } from '@/lib/channel-providers';

it('uses the authoritative backend ID for every displayed provider', () => {
  const providerDefinitions = readFileSync(resolve(process.cwd(), '../domain/upstream/provider/factory.go'), 'utf8');
  const ids = new Map([...providerDefinitions.matchAll(/ChannelType(\w+)\s+int32\s*=\s*(\d+)/g)].map(m => [m[1], Number(m[2])]));
  for (const [id, name] of Object.entries(CHANNEL_PROVIDER_NAMES)) expect(ids.get(name), name).toBe(Number(id));
  expect(CHANNEL_CREATABLE_PROVIDERS[6]).toBe('DeepSeek');
  expect(CHANNEL_CREATABLE_PROVIDERS).not.toHaveProperty('14');
});
