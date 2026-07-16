import { describe, expect, it } from 'vitest';
import {
  parseNotification,
  translateError,
  notifyTypeLabel,
} from './notification-display';

describe('notifyTypeLabel', () => {
  it('translates known types', () => {
    expect(notifyTypeLabel('event')).toBe('系统事件');
    expect(notifyTypeLabel('email')).toBe('邮件');
    expect(notifyTypeLabel('webhook')).toBe('Webhook');
  });

  it('returns raw for unknown types', () => {
    expect(notifyTypeLabel('foobar')).toBe('foobar');
    expect(notifyTypeLabel(undefined)).toBe('未知渠道');
  });
});

describe('translateError', () => {
  it('translates known sender errors', () => {
    expect(translateError('notification sender is not configured')).toBe(
      '通知发送器未配置(未设置 Webhook/邮件等发送渠道)'
    );
  });

  it('passes through unknown errors', () => {
    expect(translateError('something weird')).toBe('something weird');
    expect(translateError(undefined)).toBeUndefined();
  });
});

describe('parseNotification — reconciliation', () => {
  const subject = '[recon] 7 discrepancies @ 2026-07-15T23:42:00Z';
  const content = `Reconciliation run at 2026-07-15T23:42:00Z found 7 discrepancies.
Expired reservations cleaned: 0
Accounts checked: 4 (mismatches: 3)
Channels checked: 3 (mismatches: 2)
Ledger/log consume groups drifted: 1

Account quota mismatches (showing up to 5):
  - user=1 expected=-5199897 actual=2105509 frozen=0

Channel usage mismatches (showing up to 5):
  - channel=1 expected=429433141 actual=426585279 diff=-2847862 upstream_cost=0`;

  it('classifies as reconciliation', () => {
    const parsed = parseNotification({ subject, content, status: 'failed' });
    expect(parsed.categoryLabel).toBe('对账告警');
    expect(parsed.summary).toContain('7 项差异');
    expect(parsed.severity).toBe('error');
  });

  it('extracts structured detail rows', () => {
    const parsed = parseNotification({ subject, content, status: 'failed' });
    const labels = parsed.details.map((d) => d.label);
    expect(labels).toContain('差异总数');
    expect(labels).toContain('账户核查');
    expect(labels).toContain('渠道核查');
    expect(labels.some((l) => l.includes('用户 1'))).toBe(true);
    expect(labels.some((l) => l.includes('渠道 1'))).toBe(true);
  });
});

describe('parseNotification — quota alert', () => {
  it('classifies exhausted quota', () => {
    const parsed = parseNotification({
      subject: 'Subscription account spent: exhausted',
      content:
        'Subscription account alert: exhausted\nAccount: spent (ID: 5)\nPlatform: openai\nLocal quota used USD: 10.0000 / limit 10.0000',
      status: 'sent',
    });
    expect(parsed.categoryLabel).toBe('配额告警');
    expect(parsed.summary).toContain('配额已耗尽');
    expect(parsed.severity).toBe('error');
    expect(parsed.details.some((d) => d.label === '订阅账户')).toBe(true);
  });
});

describe('parseNotification — channel unavailable', () => {
  it('classifies channel down', () => {
    const parsed = parseNotification({
      subject: 'Channel unavailable: primary-openai',
      content:
        'A channel has become unavailable.\nChannel: primary-openai (ID: 1)\nConsecutive failures: 5\nLast error: status=502',
      status: 'failed',
    });
    expect(parsed.categoryLabel).toBe('渠道异常');
    expect(parsed.summary).toContain('primary-openai');
    expect(parsed.details.some((d) => d.label === '连续失败次数')).toBe(true);
  });
});

describe('parseNotification — fallback', () => {
  it('falls back to raw text for unknown shapes', () => {
    const parsed = parseNotification({
      subject: 'Some random subject',
      content: 'line one\nline two',
      status: 'sent',
    });
    expect(parsed.categoryLabel).toBe('系统通知');
    expect(parsed.summary).toBe('Some random subject');
    expect(parsed.details).toHaveLength(2);
  });
});
