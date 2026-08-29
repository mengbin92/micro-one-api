import { describe, expect, it, vi } from "vitest";
import {
  parseImportFile,
  downloadExportDocument,
  MODEL_EXCHANGE_SCHEMA_VERSION,
  type ModelExportDocument,
} from './model-exchange';

describe('parseImportFile', () => {
  it('parses a valid document', () => {
    const doc: ModelExportDocument = {
      schema_version: MODEL_EXCHANGE_SCHEMA_VERSION,
      exported_at: '2026-01-01T00:00:00Z',
      content_hash: 'abc123',
      models: [
        {
          model_id: 'gpt-4o',
          display_name: 'GPT-4o',
          description: '',
          provider: 'openai',
          model_type: 'chat',
          context_window: 128000,
          pricing_input: 0,
          pricing_output: 0,
          pricing_cache_read: 0,
          status: 1,
          is_public: true,
          capabilities: ['vision'],
          input_modalities: ['text', 'image'],
          output_modalities: ['text'],
          tags: [],
          category: '',
          tier: 'premium',
          metadata: '',
          aliases: [{ alias: 'gpt4o', is_primary: false }],
          channel_mappings: [],
          subscription_mappings: [],
        },
      ],
    };
    const result = parseImportFile(JSON.stringify(doc));
    expect(result.models).toHaveLength(1);
    expect(result.models[0].model_id).toBe('gpt-4o');
    expect(result.models[0].aliases).toHaveLength(1);
  });

  it('rejects a schema version mismatch', () => {
    const bad = JSON.stringify({
      schema_version: '99.0.0',
      models: [],
    });
    expect(() => parseImportFile(bad)).toThrow(/Schema version mismatch/);
  });

  it('rejects invalid JSON', () => {
    expect(() => parseImportFile('not json')).toThrow();
  });

  it('rejects a document with non-array models', () => {
    const bad = JSON.stringify({
      schema_version: MODEL_EXCHANGE_SCHEMA_VERSION,
      models: 'not-an-array',
    });
    expect(() => parseImportFile(bad)).toThrow(/"models" must be an array/);
  });
});

describe('downloadExportDocument', () => {
  it('creates a download link and revokes the URL', () => {
    const doc: ModelExportDocument = {
      schema_version: MODEL_EXCHANGE_SCHEMA_VERSION,
      exported_at: '2026-01-01T00:00:00Z',
      content_hash: 'abc',
      models: [],
    };
    // Mock URL.createObjectURL and revokeObjectURL
    const createURL = vi.fn(() => 'blob:test');
    const revokeURL = vi.fn();
    Object.defineProperty(globalThis, 'URL', {
      value: { createObjectURL: createURL, revokeObjectURL: revokeURL },
      writable: true,
    });
    // Mock document.createElement and appendChild
    const clickFn = vi.fn();
    const fakeAnchor = {
      href: '',
      download: '',
      click: clickFn,
    };
    const createElementOrig = document.createElement.bind(document);
    vi.spyOn(document, 'createElement').mockImplementation((tag: string) => {
      if (tag === 'a') return fakeAnchor as unknown as HTMLAnchorElement;
      return createElementOrig(tag);
    });
    const appendSpy = vi.spyOn(document.body, 'appendChild').mockImplementation(() => null as never);
    const removeSpy = vi.spyOn(document.body, 'removeChild').mockImplementation(() => null as never);

    downloadExportDocument(doc);

    expect(createURL).toHaveBeenCalledTimes(1);
    expect(clickFn).toHaveBeenCalledTimes(1);
    expect(revokeURL).toHaveBeenCalledWith('blob:test');
    expect(fakeAnchor.download).toBe('model-export.json');

    appendSpy.mockRestore();
    removeSpy.mockRestore();
  });
});
