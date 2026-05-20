import { describe, expect, it } from 'vitest';
import { ensureApiSuccess, unwrapApiData } from './api-response';

describe('api response helpers', () => {
  it('returns data from one-api response envelopes', () => {
    expect(unwrapApiData({ success: true, data: [{ id: 1 }] })).toEqual([{ id: 1 }]);
  });

  it('throws backend message when success is false', () => {
    expect(() => ensureApiSuccess({ success: false, message: 'bad option' })).toThrow('bad option');
  });

  it('throws fallback when response has no message', () => {
    expect(() => ensureApiSuccess({ success: false })).toThrow('Request failed');
  });
});
