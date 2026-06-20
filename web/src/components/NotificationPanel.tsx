/**
 * Notification Panel Component
 * Displays notification history with status filtering
 * Note: Mark-as-read functionality is not available as the backend doesn't provide the HTTP endpoint
 */

import { useState, useEffect, useCallback, useRef } from 'react';
import {
  Bell,
  CheckCircle2,
  Clock,
  X,
  XCircle,
  RefreshCw,
} from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import { cn } from '@/lib/utils';
import { adminApiClient } from '@/lib/api';

// Notification types based on backend API response (snake_case)
interface Notification {
  id?: number;
  type?: string;
  recipient?: string;
  subject?: string;
  content?: string;
  status?: string;
  retry_count?: number;
  created_at?: string;
  sent_at?: string;
}

interface NotificationListResponse {
  items?: Notification[];
  total?: number;
}

// Status types
type NotificationStatus = 'all' | 'pending' | 'sent' | 'failed';

// Status display mapping
const STATUS_CONFIG: Record<string, { label: string; icon: React.ElementType; color: string }> = {
  pending: {
    label: '发送中',
    icon: Clock,
    color: 'text-amber-600 bg-amber-50 dark:bg-amber-500/10 dark:text-amber-300',
  },
  sent: {
    label: '已发送',
    icon: CheckCircle2,
    color: 'text-emerald-600 bg-emerald-50 dark:bg-emerald-500/10 dark:text-emerald-300',
  },
  failed: {
    label: '发送失败',
    icon: XCircle,
    color: 'text-red-600 bg-red-50 dark:bg-red-500/10 dark:text-red-300',
  },
};

// Format timestamp
function formatTime(dateString?: string): string {
  if (!dateString) return '-';
  try {
    const date = new Date(dateString);
    const now = new Date();
    const diffMs = now.getTime() - date.getTime();
    const diffMins = Math.floor(diffMs / 60000);
    const diffHours = Math.floor(diffMs / 3600000);
    const diffDays = Math.floor(diffMs / 86400000);

    if (diffMins < 1) return '刚刚';
    if (diffMins < 60) return `${diffMins}分钟前`;
    if (diffHours < 24) return `${diffHours}小时前`;
    if (diffDays < 7) return `${diffDays}天前`;

    return date.toLocaleDateString('zh-CN');
  } catch {
    return '-';
  }
}

interface NotificationPanelProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function NotificationPanel({ open, onOpenChange }: NotificationPanelProps) {
  const [statusFilter, setStatusFilter] = useState<NotificationStatus>('all');
  const [notifications, setNotifications] = useState<Notification[]>([]);
  const [total, setTotal] = useState(0);
  const [isLoading, setIsLoading] = useState(false);
  const [unreadCount, setUnreadCount] = useState(0);

  // Use ref to track component mounted state
  const mountedRef = useRef(true);

  // Fetch unread count only - lightweight polling
  const fetchUnreadCount = useCallback(async () => {
    try {
      const params = new URLSearchParams({
        page: '1',
        page_size: '1', // Only need count, not actual data
        status: 'pending',
      });

      const response = await adminApiClient.get(`/admin/notifications?${params}`);
      // notify-worker returns {items, total} directly, not wrapped in {data}
      const data: NotificationListResponse = response.data;
      if (mountedRef.current) {
        setUnreadCount(data.total ?? 0);
      }
    } catch (error) {
      // Silently fail for unread count polling
      console.debug('Failed to fetch unread count:', error);
    }
  }, []);

  // Fetch notifications - full list
  const fetchNotifications = useCallback(async () => {
    setIsLoading(true);
    try {
      const params = new URLSearchParams({
        page: '1',
        page_size: '50',
      });
      if (statusFilter !== 'all') {
        params.append('status', statusFilter);
      }

      const response = await adminApiClient.get(`/admin/notifications?${params}`);
      // notify-worker returns {items, total} directly, not wrapped in {data}
      const data: NotificationListResponse = response.data;
      if (mountedRef.current) {
        setNotifications(data.items ?? []);
        setTotal(data.total ?? 0);
        // Note: unreadCount is maintained by fetchUnreadCount, not derived from filtered list
        // to ensure badge shows accurate pending count regardless of current filter
      }
    } catch (error) {
      console.error('Failed to fetch notifications:', error);
      if (mountedRef.current) {
        toast.error('获取通知列表失败');
        setNotifications([]);
        setTotal(0);
      }
    } finally {
      if (mountedRef.current) {
        setIsLoading(false);
      }
    }
  }, [statusFilter]);

  // Fetch unread count periodically - always running, even when closed
  useEffect(() => {
    const initialFetch = window.setTimeout(() => {
      fetchUnreadCount();
    }, 0);

    // Poll every 30 seconds
    const interval = setInterval(() => {
      fetchUnreadCount();
    }, 30000);

    return () => {
      mountedRef.current = false;
      window.clearTimeout(initialFetch);
      clearInterval(interval);
    };
  }, [fetchUnreadCount]);

  // Fetch full notifications when panel opens or filter changes
  useEffect(() => {
    if (!open) return;

    const timeout = window.setTimeout(() => {
      fetchNotifications();
    }, 0);

    return () => window.clearTimeout(timeout);
  }, [open, fetchNotifications]);

  // Auto-refresh every 30s when open
  useEffect(() => {
    if (!open) return;

    const interval = setInterval(() => {
      fetchNotifications();
    }, 30000);

    return () => clearInterval(interval);
  }, [open, fetchNotifications]);

  // Handle refresh
  const handleRefresh = () => {
    fetchNotifications();
    toast.success('通知列表已刷新');
  };

  // Filter buttons
  const filterButtons: { key: NotificationStatus; label: string }[] = [
    { key: 'all', label: '全部' },
    { key: 'pending', label: '发送中' },
    { key: 'sent', label: '已发送' },
    { key: 'failed', label: '失败' },
  ];

  return (
    <>
      {/* Bell Icon Button - Always Visible */}
      <Button
        type="button"
        variant="ghost"
        size="icon-sm"
        aria-label="Notifications"
        onClick={() => onOpenChange(!open)}
        className="relative inline-flex"
      >
        <Bell className="size-4" />
        {unreadCount > 0 && (
          <span className="absolute -right-1 -top-1 flex h-4 w-4 items-center justify-center rounded-full bg-red-500 text-[10px] font-bold text-white">
            {unreadCount > 9 ? '9+' : unreadCount}
          </span>
        )}
      </Button>

      {/* Panel - Opens when clicked */}
      {open && (
        <>
          {/* Backdrop */}
          <div
            className="fixed inset-0 z-40 bg-black/20"
            onClick={() => onOpenChange(false)}
          />

          {/* Panel */}
          <Card className="fixed right-0 top-0 z-50 h-full w-full max-w-md border-l shadow-xl">
            <div className="flex h-full flex-col">
              {/* Header */}
              <div className="flex items-center justify-between border-b px-4 py-3">
                <div className="flex items-center gap-2">
                  <Bell className="size-5 text-blue-600" />
                  <h3 className="text-lg font-bold">通知中心</h3>
                  {unreadCount > 0 && (
                    <span className="rounded-full bg-red-500 px-2 py-0.5 text-xs font-bold text-white">
                      {unreadCount} 未读
                    </span>
                  )}
                </div>
                <div className="flex items-center gap-1">
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    onClick={handleRefresh}
                    disabled={isLoading}
                  >
                    <RefreshCw className={cn('size-4', isLoading && 'animate-spin')} />
                  </Button>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    onClick={() => onOpenChange(false)}
                  >
                    <X className="size-4" />
                  </Button>
                </div>
              </div>

              {/* Filter Tabs */}
              <div className="flex items-center gap-1 border-b px-4 py-2 overflow-x-auto">
                {filterButtons.map((filter) => (
                  <button
                    key={filter.key}
                    type="button"
                    onClick={() => setStatusFilter(filter.key)}
                    className={cn(
                      'rounded-full px-3 py-1 text-xs font-medium transition-colors whitespace-nowrap',
                      statusFilter === filter.key
                        ? 'bg-blue-600 text-white'
                        : 'text-slate-600 hover:bg-slate-100 dark:text-slate-400 dark:hover:bg-slate-800'
                    )}
                  >
                    {filter.label}
                  </button>
                ))}
              </div>

              {/* Content */}
              <div className="flex-1 overflow-y-auto">
                {isLoading ? (
                  <div className="space-y-3 p-4">
                    {[1, 2, 3].map((i) => (
                      <div key={i} className="h-24 animate-pulse rounded-lg bg-muted/50" />
                    ))}
                  </div>
                ) : notifications.length === 0 ? (
                  <div className="flex h-full items-center justify-center p-8 text-center">
                    <div className="space-y-2">
                      <Bell className="mx-auto size-12 text-slate-300" />
                      <p className="text-sm font-medium text-slate-500">暂无通知</p>
                      <p className="text-xs text-slate-400">
                        {statusFilter === 'all' ? '您还没有收到任何通知' : '该状态下没有通知'}
                      </p>
                    </div>
                  </div>
                ) : (
                  <div className="space-y-2 p-4">
                    {notifications.map((notification) => {
                      const status = notification.status || 'pending';
                      const config = STATUS_CONFIG[status] || STATUS_CONFIG.pending;
                      const StatusIcon = config.icon;
                      const isPending = status === 'pending';

                      return (
                        <div
                          key={notification.id}
                          className={cn(
                            'relative rounded-lg border p-3 transition-colors hover:bg-slate-50 dark:hover:bg-slate-800/50',
                            isPending && 'bg-blue-50/50 border-blue-200 dark:bg-blue-500/5 dark:border-blue-500/20'
                          )}
                        >
                          {/* Status Badge */}
                          <div className="mb-2 flex items-center justify-between">
                            <span className={cn(
                              'flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium',
                              config.color
                            )}>
                              <StatusIcon className="size-3" />
                              {config.label}
                            </span>
                            <span className="text-xs text-slate-400">
                              {formatTime(notification.created_at)}
                            </span>
                          </div>

                          {/* Subject */}
                          <h4 className="mb-1 text-sm font-semibold text-slate-900 dark:text-slate-100">
                            {notification.subject || '无主题'}
                          </h4>

                          {/* Content */}
                          <p className="mb-2 text-xs text-slate-600 dark:text-slate-400 line-clamp-2">
                            {notification.content || '无内容'}
                          </p>

                          {/* Footer */}
                          <div className="flex items-center justify-between text-xs text-slate-400">
                            <span>收件人: {notification.recipient || '未知'}</span>
                          </div>

                          {/* Retry Count for Failed */}
                          {status === 'failed' && (notification.retry_count ?? 0) > 0 && (
                            <div className="mt-2 text-xs text-red-600 dark:text-red-400">
                              已重试 {notification.retry_count} 次
                            </div>
                          )}
                        </div>
                      );
                    })}
                  </div>
                )}
              </div>

              {/* Footer */}
              <div className="border-t px-4 py-3">
                <div className="flex items-center justify-between text-xs text-slate-400">
                  <span>共 {total} 条通知</span>
                </div>
              </div>
            </div>
          </Card>
        </>
      )}
    </>
  );
}
