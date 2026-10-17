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

describe('payload response contracts', () => {
  it.each([
    { items: [], next_page_token: '' },
    { success: true, models: [], total: 0 },
    { success: true },
    null,
    '<html>upstream error</html>',
  ])('rejects a non-payload response instead of returning undefined: %j', response => {
    expect(() => unwrapApiData(response)).toThrow('Unexpected API response format');
  });
  it.each([null, false, 0, '', []])('keeps an explicitly present payload: %j', data => {
    expect(unwrapApiData({ success: true, data })).toEqual(data);
  });
  it('allows acknowledgement-only operations without data', () => {
    expect(() => ensureApiSuccess({ success: true })).not.toThrow();
    expect(() => ensureApiSuccess({})).not.toThrow();
  });
});
