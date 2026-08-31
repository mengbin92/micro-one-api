import { describe, expect, it } from 'vitest';
import { t } from './i18n';

describe('t', () => {
  it('interpolates values in the Chinese source message', () => {
    expect(t('已处理 {count} 个模型', { count: 3 })).toBe('已处理 3 个模型');
  });

  it('interpolates values after translating the complete English sentence', () => {
    window.localStorage.setItem('web:language', JSON.stringify('en-US'));

    expect(t('已处理 {count} 个模型', { count: 3 })).toBe('Processed 3 models');
    expect(t('查看日志 {id} 的账务和中继元数据。', { id: 'log-7' }))
      .toBe('Inspect billing and relay metadata for log log-7.');
  });

  it('keeps unknown placeholders intact', () => {
    expect(t('已处理 {count} 个模型')).toBe('已处理 {count} 个模型');
  });
});
