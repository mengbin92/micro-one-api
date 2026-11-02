import { describe, expect, it } from 'vitest';
import { t } from '@/lib/i18n';
import { announcementState, fromBeijingInput, toBeijingInput, validAnnouncementSchedule } from './announcements';
describe('announcement schedule', () => {
  it('round trips Beijing time independently of browser timezone', () => {
    const epoch = Date.parse('2030-01-02T09:30:00+08:00') / 1000;
    expect(fromBeijingInput('2030-01-02T09:30')).toBe(epoch);
    expect(toBeijingInput(epoch)).toBe('2030-01-02T09:30');
    expect(fromBeijingInput('')).toBe(0);
  });
  it('treats publish as inclusive and expiration as exclusive', () => {
    const a = { id: '1', revision: 1, title: 'a', body: 'b', audience: 'all' as const, status: 'published' as const, publish_at: 10, expires_at: 20 };
    expect(announcementState(a, 9)).toBe('待发布');
    expect(announcementState(a, 10)).toBe('已发布');
    expect(announcementState(a, 20)).toBe('已下架');
  });

  it('rejects expiration at or before effective publication', () => {
    expect(validAnnouncementSchedule({ status: 'published', publish_at: 20, expires_at: 20 }, 10)).toBe(false);
    expect(validAnnouncementSchedule({ status: 'published', publish_at: 0, expires_at: 10 }, 10)).toBe(false);
    expect(validAnnouncementSchedule({ status: 'published', publish_at: 0, expires_at: 11 }, 10)).toBe(true);
    expect(validAnnouncementSchedule({ status: 'published', publish_at: 20, expires_at: 0 }, 10)).toBe(true);
    expect(validAnnouncementSchedule({ status: 'draft', publish_at: 0, expires_at: 9 }, 10)).toBe(true);
  });
  it('translates shared timestamp and reason labels in English', () => {
    localStorage.setItem('web:language', JSON.stringify('en-US'));
    expect(t('立即')).toBe('Immediately');
    expect(t('更新时间')).toBe('Updated at');
    expect(t('操作原因')).toBe('Reason for change');
  });
});
