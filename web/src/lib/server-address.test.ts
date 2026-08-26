import { describe, expect, it, vi } from 'vitest';
import { normalizeRelayBaseUrl, resolveRelayBaseUrl, validateRelayAddressForPage } from './server-address';

describe('relay address validation', () => {
  it('normalizes valid HTTP(S) addresses', () => {
    expect(normalizeRelayBaseUrl(' https://relay.example.test/// ')).toBe('https://relay.example.test');
    expect(() => normalizeRelayBaseUrl('ftp://relay.example.test')).toThrow();
    expect(() => normalizeRelayBaseUrl('https://user:pass@relay.example.test')).toThrow();
  });

  it('blocks mixed-content browser requests', () => {
    expect(() =>
      validateRelayAddressForPage({ url: 'http://relay.example.test', source: 'build-env' }, 'https:'),
    ).toThrow(/HTTPS 控制台不能调用 HTTP Relay/);
    expect(() =>
      validateRelayAddressForPage({ url: 'https://relay.example.test', source: 'build-env' }, 'https:'),
    ).not.toThrow();
  });

  it('uses a valid status address before local defaults', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ data: { server_address: 'https://relay.example.test/' } }), {
        headers: { 'Content-Type': 'application/json' },
      }),
    );

    await expect(resolveRelayBaseUrl()).resolves.toEqual({
      url: 'https://relay.example.test',
      source: 'server-status',
    });
  });

  it('does not hide an invalid configured status address behind fallback', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ data: { server_address: 'ftp://relay.example.test' } }), {
        headers: { 'Content-Type': 'application/json' },
      }),
    );

    await expect(resolveRelayBaseUrl()).rejects.toThrow(/HTTP 或 HTTPS/);
  });

  it('uses the marked same-origin fallback when status discovery fails', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new TypeError('network down'));

    await expect(resolveRelayBaseUrl()).resolves.toMatchObject({
      url: window.location.origin,
      source: 'same-origin-fallback',
      warning: expect.any(String),
    });
  });
});
