import { describe, expect, it, vi } from 'vitest';
import openapiTS, { astToString } from 'openapi-typescript';
import { readFileFromUrl } from '@redocly/openapi-core/lib/utils.js';

describe('OpenAPI dependency compatibility', () => {
  it('matches resolver header globs with the patched minimatch dependency', async () => {
    const url = 'https://schemas.example.test/private/openapi.json';
    const customFetch = vi.fn(async () => new Response('{}', {
      headers: { 'content-type': 'application/json' },
    }));
    await readFileFromUrl(url, {
      headers: [
        { matches: 'https://schemas.example.test/private/**', name: 'X-Protocol', value: 'matched' },
        { matches: 'schemas.example.test/private/**', name: 'X-Host', value: 'matched' },
        { matches: 'other.example.test/**', name: 'X-Other', value: 'must-not-send' },
      ],
      customFetch,
    });
    expect(customFetch).toHaveBeenCalledExactlyOnceWith(url, {
      headers: { 'X-Protocol': 'matched', 'X-Host': 'matched' },
    });
  });

  it('generates response types and resolves component references', async () => {
    const ast = await openapiTS({
      openapi: '3.0.3',
      info: { title: 'Toolchain fixture', version: '1.0.0' },
      paths: {
        '/probe': {
          get: {
            responses: {
              '200': {
                description: 'OK',
                content: { 'application/json': { schema: { $ref: '#/components/schemas/Probe' } } },
              },
            },
          },
        },
      },
      components: { schemas: { Probe: { type: 'object', required: ['ok'], properties: { ok: { type: 'boolean' } } } } },
    }, { silent: true });
    const output = astToString(ast);
    expect(output).toContain('"/probe"');
    expect(output).toContain('ok: boolean');
    expect(output).toContain('components["schemas"]["Probe"]');
  });
});
