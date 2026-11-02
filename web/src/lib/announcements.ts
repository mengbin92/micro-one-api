import { locale, t } from '@/lib/i18n';

export interface Announcement {
  id: string;
  revision: number;
  title: string;
  body: string;
  audience: 'all' | 'groups' | 'users';
  targets?: string[];
  status: 'draft' | 'published' | 'archived';
  publish_at: number;
  expires_at?: number;
}
export interface UserAnnouncement { id: string; revision: number; title: string; body: string; publish_at: number; expires_at?: number; read?: boolean }
export interface AnnouncementList<T> { items?: T[]; total?: number }
export interface ModelAvailability { models?: string[]; status: 'available' | 'no_available_route'; checked_at: number }
export function announcementTime(value?: number) {
  return value ? new Date(value * 1000).toLocaleString(locale(), { timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }) : t('立即');
}
export function toBeijingInput(value?: number) { return value ? new Date(value * 1000 + 8 * 3600000).toISOString().slice(0, 16) : ''; }
export function fromBeijingInput(value: string) { return value ? Math.floor(Date.parse(`${value}:00+08:00`) / 1000) : 0; }
export function announcementState(a: Announcement, now = Date.now() / 1000) {
  if (a.status === 'draft') return t('草稿');
  if (a.status === 'archived' || (a.expires_at && a.expires_at <= now)) return t('已下架');
  return a.publish_at > now ? t('待发布') : t('已发布');
}

export function validAnnouncementSchedule(a: Pick<Announcement, 'publish_at' | 'expires_at' | 'status'>, now = Date.now() / 1000) {
  return !a.expires_at || a.expires_at > (a.publish_at || (a.status === 'published' ? now : 0));
}
