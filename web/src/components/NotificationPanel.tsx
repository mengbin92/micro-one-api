/**
 * Notification Panel Component
 * Displays notification history with status filtering.
 * Raw backend English/technical text is parsed into structured Chinese
 * display metadata via @/lib/notification-display.
 * Note: Mark-as-read functionality is not available as the backend doesn't provide the HTTP endpoint
 */

import { useState } from 'react';
import { createPortal } from 'react-dom';
import { useQuery } from '@tanstack/react-query';
import {
  Bell,
  CheckCircle2,
  Clock,
  X,
  XCircle,
  RefreshCw,
  ChevronDown,
  Webhook,
  Mail,
  MessageSquare,
  Activity,
  ShieldAlert,
  TriangleAlert,
  CircleAlert,
} from 'lucide-react';
import { toast } from 'sonner';
import { Button } from '@/components/ui/button';
import { Card } from '@/components/ui/card';
import { cn } from '@/lib/utils';
import { adminApiClient } from '@/lib/api';
import type { components } from '@/types/api';
import {
  parseNotification,
  translateError,
  notifyTypeLabel,
} from '@/lib/notification-display';
import { locale, t } from '@/lib/i18n';

// Notification types based on backend API response (snake_case)
type GeneratedNotification = components['schemas']['api.notify.v1.NotificationItem'];

interface Notification extends Omit<
  GeneratedNotification,
  'id' | 'retryCount' | 'createdAt' | 'sentAt' | 'lastError'
> {
  id?: number;
  retry_count?: number;
  last_error?: string;
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

// Channel icon by notify type
const TYPE_ICON: Record<string, React.ElementType> = {
  webhook: Webhook,
  email: Mail,
  event: Activity,
  wecom: MessageSquare,
  dingtalk: MessageSquare,
  feishu: MessageSquare,
  slack: MessageSquare,
};

// Severity icon/color
const SEVERITY_CONFIG: Record<
  string,
  { icon: React.ElementType; color: string; ring: string }
> = {
  info: {
    icon: Activity,
    color: 'text-blue-600 dark:text-blue-400',
    ring: 'ring-blue-200/60 dark:ring-blue-500/20',
  },
  warning: {
    icon: TriangleAlert,
    color: 'text-amber-600 dark:text-amber-400',
    ring: 'ring-amber-200/60 dark:ring-amber-500/20',
  },
  error: {
    icon: CircleAlert,
    color: 'text-red-600 dark:text-red-400',
    ring: 'ring-red-200/60 dark:ring-red-500/20',
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

    if (diffMins < 1) return t("刚刚");
    if (diffMins < 60) return t(`${diffMins} 分钟前`);
    if (diffHours < 24) return t(`${diffHours} 小时前`);
    if (diffDays < 7) return t(`${diffDays} 天前`);

    return date.toLocaleString(locale(), { hour12: false });
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
  const [expandedIds, setExpandedIds] = useState<Set<number>>(new Set());
  const canUsePortal = typeof document !== 'undefined';

  const unreadQuery = useQuery<NotificationListResponse>({
    queryKey: ['admin', 'notifications', 'pending-count'],
    queryFn: async () => {
      const params = new URLSearchParams({ page: '1', page_size: '1', status: 'pending' });
      const response = await adminApiClient.get(`/admin/notifications?${params}`);
      return response.data as NotificationListResponse;
    },
    refetchInterval: 30000,
    meta: { suppressErrorToast: true },
  });

  const notificationsQuery = useQuery<NotificationListResponse>({
    queryKey: ['admin', 'notifications', statusFilter],
    queryFn: async () => {
      const params = new URLSearchParams({ page: '1', page_size: '50' });
      if (statusFilter !== 'all') params.append('status', statusFilter);
      const response = await adminApiClient.get(`/admin/notifications?${params}`);
      return response.data as NotificationListResponse;
    },
    enabled: open,
    refetchInterval: open ? 30000 : false,
  });

  const notifications = notificationsQuery.data?.items ?? [];
  const total = notificationsQuery.data?.total ?? 0;
  const unreadCount = unreadQuery.data?.total ?? 0;
  const isLoading = notificationsQuery.isPending;
  const isRefreshing = notificationsQuery.isFetching;
  const hasInitialError = notificationsQuery.isError && notificationsQuery.data === undefined;
  const resetExpansion = () => setExpandedIds(new Set());

  // Handle refresh
  const handleRefresh = () => {
    void notificationsQuery.refetch().then(({ error }) => {
      if (!error) toast.success(t("通知列表已刷新"));
    });
  };

  const toggleExpand = (id: number) => {
    setExpandedIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  };

  // Filter buttons
  const filterButtons: { key: NotificationStatus; label: string }[] = [
    { key: 'all', label: t("全部") },
    { key: 'pending', label: t("发送中") },
    { key: 'sent', label: t("已发送") },
    { key: 'failed', label: t("失败") },
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
          <span className="absolute -right-1.5 -top-1.5 flex size-5 items-center justify-center rounded-full bg-red-500 text-xs font-bold text-white">
            {unreadCount > 9 ? '9+' : unreadCount}
          </span>
        )}
      </Button>

      {/* Panel - Opens when clicked */}
      {open && canUsePortal && createPortal(
        <>
          {/* Backdrop */}
          <div
            className="fixed inset-0 z-40 bg-black/20"
            onClick={() => onOpenChange(false)}
          />

          {/* Panel */}
          <Card className="fixed right-0 top-0 z-50 h-[100dvh] w-full max-w-md rounded-none border-l py-0 shadow-xl">
            <div className="flex h-full flex-col">
              {/* Header */}
              <div className="flex items-center justify-between border-b px-4 py-3">
                <div className="flex items-center gap-2">
                  <Bell className="size-5 text-blue-600" />
                  <h3 className="text-lg font-bold">{t("通知中心")}</h3>
                  {unreadCount > 0 && (
                    <span className="rounded-full bg-red-500 px-2 py-0.5 text-xs font-bold text-white">
                      {unreadCount}{t("未读")}</span>
                  )}
                </div>
                <div className="flex items-center gap-1">
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    aria-label={t("刷新通知")}
                    onClick={handleRefresh}
                    disabled={isRefreshing}
                  >
                    <RefreshCw className={cn('size-4', isRefreshing && 'animate-spin')} />
                  </Button>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    aria-label={t("关闭通知")}
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
                    onClick={() => {
                      resetExpansion();
                      setStatusFilter(filter.key);
                    }}
                    className={cn(
                      'rounded-full px-3 py-1 text-xs font-medium transition-colors whitespace-nowrap',
                      statusFilter === filter.key
                        ? 'bg-primary text-primary-foreground'
                        : 'text-muted-foreground hover:bg-muted'
                    )}
                  >
                    {filter.label}
                  </button>
                ))}
              </div>

              {/* Content */}
              <div className="flex-1 overflow-y-auto">
                {hasInitialError ? (
                  <div className="flex h-full items-center justify-center p-8 text-center">
                    <div className="space-y-3">
                      <CircleAlert className="mx-auto size-12 text-red-300" />
                      <p className="text-sm font-medium text-muted-foreground">{t("通知加载失败")}</p>
                      <Button type="button" variant="outline" size="sm" onClick={handleRefresh}>{t("重试")}</Button>
                    </div>
                  </div>
                ) : isLoading ? (
                  <div className="space-y-3 p-4">
                    {[1, 2, 3].map((i) => (
                      <div key={i} className="h-24 animate-pulse rounded-lg bg-muted/50" />
                    ))}
                  </div>
                ) : notifications.length === 0 ? (
                  <div className="flex h-full items-center justify-center p-8 text-center">
                    <div className="space-y-2">
                      <Bell className="mx-auto size-12 text-muted-foreground" />
                      <p className="text-sm font-medium text-muted-foreground">{t("暂无通知")}</p>
                      <p className="text-xs text-muted-foreground">
                        {statusFilter === 'all' ? t("您还没有收到任何通知") : t("该状态下没有通知")}
                      </p>
                    </div>
                  </div>
                ) : (
                  <div className="space-y-2 p-3">
                    {notifications.map((notification) => {
                      const status = notification.status || 'pending';
                      const config = STATUS_CONFIG[status] || STATUS_CONFIG.pending;
                      const StatusIcon = config.icon;
                      const isPending = status === 'pending';
                      const isFailed = status === 'failed';

                      // Parse the raw English/technical text into structured display data
                      const parsed = parseNotification({
                        subject: notification.subject,
                        content: notification.content,
                        status: notification.status,
                        last_error: notification.last_error,
                      });
                      const sev = SEVERITY_CONFIG[parsed.severity] ?? SEVERITY_CONFIG.info;
                      const SevIcon = sev.icon;
                      const TypeIcon = TYPE_ICON[notification.type ?? ''] ?? Activity;
                      const id = notification.id ?? 0;
                      const isExpanded = expandedIds.has(id);
                      const hasDetails = parsed.details.length > 0 || isFailed;
                      const translatedErr = translateError(notification.last_error);

                      return (
                        <div
                          key={id}
                          className={cn(
                            'relative rounded-xl border border-border bg-card p-3 transition-colors',
                            isFailed
                              ? 'border-red-200 bg-red-50/40 dark:border-red-500/30 dark:bg-red-500/5'
                              : isPending
                                ? 'border-blue-200 bg-blue-50/40 dark:border-blue-500/20 dark:bg-blue-500/5'
                                : 'hover:bg-muted'
                          )}
                        >
                          {/* Top row: category badge + severity + time */}
                          <div className="mb-2 flex items-center justify-between gap-2">
                            <div className="flex items-center gap-1.5">
                              <span className={cn('flex items-center gap-1', sev.color)}>
                                <SevIcon className="size-3.5" />
                              </span>
                              <span className="text-xs font-semibold text-foreground">
                                {parsed.categoryLabel}
                              </span>
                            </div>
                            <span className="text-xs text-muted-foreground">
                              {formatTime(notification.created_at)}
                            </span>
                          </div>

                          {/* Summary (the headline an operator understands) */}
                          <p className="mb-1.5 text-sm font-medium text-foreground">
                            {parsed.summary}
                          </p>

                          {/* Meta chips: status + channel type + recipient */}
                          <div className="mb-2 flex flex-wrap items-center gap-1.5">
                            <span className={cn(
                              'flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium',
                              config.color
                            )}>
                              <StatusIcon className="size-3" />
                              {t(config.label)}
                            </span>
                            <span className="flex items-center gap-1 rounded-full bg-muted px-2 py-0.5 text-xs font-medium text-muted-foreground">
                              <TypeIcon className="size-3" />
                              {notifyTypeLabel(notification.type)}
                            </span>
                          </div>

                          {/* Failed reason: translated, highlighted */}
                          {isFailed && translatedErr && (
                            <div className="mb-2 flex items-start gap-1.5 rounded-md border border-red-200 bg-red-50 px-2 py-1.5 text-xs text-red-700 dark:border-red-500/30 dark:bg-red-500/10 dark:text-red-300">
                              <ShieldAlert className="mt-0.5 size-3.5 shrink-0" />
                              <div>
                                <span className="font-medium">{t("失败原因:")}</span>{' '}
                                <span className="break-words">{translatedErr}</span>
                              </div>
                            </div>
                          )}

                          {/* Retry count for failed */}
                          {isFailed && (notification.retry_count ?? 0) > 0 && (
                            <div className="mb-2 text-xs text-red-600 dark:text-red-400">{t("已重试")}{notification.retry_count}{t("次")}</div>
                          )}

                          {/* Expandable structured details */}
                          {hasDetails && (
                            <button
                              type="button"
                              onClick={() => toggleExpand(id)}
                              className="flex w-full items-center gap-1 rounded-md px-1 py-1 text-xs font-medium text-muted-foreground transition-colors hover:text-foreground"
                            >
                              <ChevronDown className={cn('size-3.5 transition-transform', isExpanded && 'rotate-180')} />
                              {isExpanded ? t("收起详情") : t("查看详情")}
                            </button>
                          )}

                          {hasDetails && isExpanded && (
                            <div className="mt-1.5 space-y-1 rounded-md bg-muted p-2">
                              {parsed.details.map((detail, idx) => (
                                <div
                                  key={idx}
                                  className="flex items-start justify-between gap-2 text-xs"
                                >
                                  {detail.label ? (
                                    <>
                                      <span className="shrink-0 text-muted-foreground">
                                        {detail.label}
                                      </span>
                                      {detail.value && (
                                        <span className="break-all text-right font-medium text-foreground">
                                          {detail.value}
                                        </span>
                                      )}
                                    </>
                                  ) : (
                                    <span className="break-all text-muted-foreground">
                                      {detail.value}
                                    </span>
                                  )}
                                </div>
                              ))}
                            </div>
                          )}

                          {/* Footer */}
                          <div className="mt-2 flex items-center justify-between text-xs text-muted-foreground">
                            <span className="truncate">{t("收件人:")}{notification.recipient || t("未知")}</span>
                          </div>
                        </div>
                      );
                    })}
                  </div>
                )}
              </div>

              {/* Footer */}
              <div className="border-t px-4 py-3">
                <div className="flex items-center justify-between text-xs text-muted-foreground">
                  <span>{t(`共 ${total} 条通知`)}</span>
                </div>
              </div>
            </div>
          </Card>
        </>,
        document.body
      )}
    </>
  );
}
