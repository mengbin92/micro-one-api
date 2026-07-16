/**
 * Notification display helpers
 *
 * The backend notify-worker stores alerts in raw English technical text
 * (e.g. "[recon] 7 discrepancies @ ...", "notification sender is not
 * configured"). These helpers parse that text into structured, Chinese-friendly
 * metadata so the UI can render something an operator actually understands.
 *
 * All parsing is defensive: if the subject/content does not match a known
 * pattern we fall back to the raw text so no notification is ever lost.
 */

// ---- Types ----

export type NotificationKind =
  | 'reconciliation'
  | 'channel_unavailable'
  | 'quota_alert'
  | 'unknown';

export interface ParsedNotification {
  /** Chinese category label, e.g. "对账告警" */
  categoryLabel: string;
  /** Short one-line Chinese summary */
  summary: string;
  /** Severity drives color: info / warning / error */
  severity: 'info' | 'warning' | 'error';
  /** Structured detail rows parsed from the content, each is {label, value} */
  details: { label: string; value: string }[];
}

// ---- Notification type (channel) labels ----

const TYPE_LABELS: Record<string, string> = {
  webhook: 'Webhook',
  email: '邮件',
  event: '系统事件',
  wecom: '企业微信',
  dingtalk: '钉钉',
  feishu: '飞书',
  slack: 'Slack',
};

export function notifyTypeLabel(type?: string): string {
  if (!type) return '未知渠道';
  return TYPE_LABELS[type] ?? type;
}

// ---- Error translation ----

const ERROR_TRANSLATIONS: { match: RegExp; zh: string }[] = [
  { match: /notification sender is not configured/i, zh: '通知发送器未配置(未设置 Webhook/邮件等发送渠道)' },
  { match: /webhook returned status (\d+)/i, zh: 'Webhook 返回非 2xx 状态码($1)' },
  { match: /unsupported notification type/i, zh: '不支持的通知类型' },
  { match: /connection refused|timeout|deadline exceeded/i, zh: '网络连接失败或超时' },
  { match: /auth/i, zh: '认证/鉴权失败' },
];

export function translateError(raw?: string): string | undefined {
  if (!raw) return undefined;
  for (const { match, zh } of ERROR_TRANSLATIONS) {
    if (match.test(raw)) {
      return zh.replace('$1', (raw.match(match)?.[1] ?? '') || '');
    }
  }
  return raw;
}

// ---- Subject/content parsers ----

/**
 * Parse a recon subject like "[recon] 7 discrepancies @ 2026-07-15T23:42:00Z"
 */
function parseReconSubject(subject: string): { count: number; runAt: string } | null {
  const m = subject.match(/\[recon\]\s*(\d+)\s*discrepanc(?:y|ies)\s*@\s*(.+)/i);
  if (!m) return null;
  return { count: parseInt(m[1], 10), runAt: m[2].trim() };
}

/**
 * Parse the multi-line recon content body into structured detail rows.
 */
function parseReconContent(content: string): { label: string; value: string }[] {
  const details: { label: string; value: string }[] = [];
  const lines = content.split('\n');

  for (const line of lines) {
    const trimmed = line.trim();
    if (!trimmed) continue;

    // Top-level summary lines
    let m =
      trimmed.match(/^Reconciliation run at\s*(.+?)\s*found\s*(\d+)\s*discrepanc(?:y|ies)\.?$/i);
    if (m) {
      details.push({ label: '对账时间(UTC)', value: m[1] });
      details.push({ label: '差异总数', value: `${m[2]} 项` });
      continue;
    }

    m = trimmed.match(/^Expired reservations cleaned:\s*(\d+)/i);
    if (m) {
      details.push({ label: '已清理过期预留', value: `${m[1]} 个` });
      continue;
    }

    m = trimmed.match(/^Accounts checked:\s*(\d+)\s*\(mismatches:\s*(\d+)\)/i);
    if (m) {
      details.push({ label: '账户核查', value: `检查 ${m[1]} 个,不一致 ${m[2]} 个` });
      continue;
    }

    m = trimmed.match(/^Channels checked:\s*(\d+)\s*\(mismatches:\s*(\d+)\)/i);
    if (m) {
      details.push({ label: '渠道核查', value: `检查 ${m[1]} 个,不一致 ${m[2]} 个` });
      continue;
    }

    m = trimmed.match(/^Ledger\/log consume groups drifted:\s*(\d+)/i);
    if (m) {
      details.push({ label: '账本/日志偏移', value: `${m[1]} 组` });
      continue;
    }

    // Section headers — keep as context rows
    if (/^Account quota mismatches/i.test(trimmed)) {
      details.push({ label: '账户配额不一致', value: '' });
      continue;
    }
    if (/^Channel usage mismatches/i.test(trimmed)) {
      details.push({ label: '渠道用量不一致', value: '' });
      continue;
    }
    if (/^Ledger\/log consume drift/i.test(trimmed)) {
      details.push({ label: '账本/日志消耗漂移', value: '' });
      continue;
    }

    // Indented detail bullets
    const bullet = trimmed.match(/^- user=(\S+)\s+expected=(-?\d+)\s+actual=(-?\d+)\s+frozen=(-?\d+)/i);
    if (bullet) {
      details.push({ label: `  用户 ${bullet[1]}`, value: `预期 ${bullet[2]} / 实际 ${bullet[3]} / 冻结 ${bullet[4]}` });
      continue;
    }
    const chBullet = trimmed.match(
      /^- channel=(\S+)\s+expected=(-?\d+)\s+actual=(-?\d+)\s+diff=(-?\d+)\s+upstream_cost=(-?\d+)/i
    );
    if (chBullet) {
      details.push({
        label: `  渠道 ${chBullet[1]}`,
        value: `预期 ${chBullet[2]} / 实际 ${chBullet[3]} / 差异 ${chBullet[4]} / 上游成本 ${chBullet[5]}`,
      });
      continue;
    }
    const logBullet = trimmed.match(
      /^- ledger_count=(\d+)\s+log_count=(\d+)\s+count_diff=(-?\d+)\s+quota_diff=(-?\d+)/i
    );
    if (logBullet) {
      details.push({
        label: '  账本/日志',
        value: `账本 ${logBullet[1]} / 日志 ${logBullet[2]} / 条数差 ${logBullet[3]} / 配额差 ${logBullet[4]}`,
      });
      continue;
    }
  }
  return details;
}

/**
 * Parse a quota-alert subject like "Subscription account acct1: exhausted"
 */
function parseQuotaAlertSubject(subject: string): { account: string; kind: string } | null {
  const m = subject.match(/Subscription account\s*(.+?)\s*:\s*(.+)/i);
  if (!m) return null;
  return { account: m[1].trim(), kind: m[2].trim() };
}

const QUOTA_KIND_LABELS: Record<string, string> = {
  exhausted: '配额已耗尽',
  near_exhausted: '配额接近上限',
  writeback_down: '配额回写异常',
  idle: '长时间无使用',
};

function parseQuotaAlertContent(content: string): { label: string; value: string }[] {
  const details: { label: string; value: string }[] = [];
  const lines = content.split('\n');

  const kindLine = lines.find((l) => l.startsWith('Subscription account alert:'));
  if (kindLine) {
    const kind = kindLine.replace(/.*alert:\s*/i, '').trim();
    details.push({ label: '告警类型', value: QUOTA_KIND_LABELS[kind] ?? kind });
  }

  for (const line of lines) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith('Subscription account alert:')) continue;

    let m = trimmed.match(/^Account:\s*(.+?)\s*\(ID:\s*(\d+)\)/i);
    if (m) {
      details.push({ label: '订阅账户', value: `${m[1]} (ID: ${m[2]})` });
      continue;
    }
    m = trimmed.match(/^Platform:\s*(.+)/i);
    if (m) {
      details.push({ label: '平台', value: m[1] });
      continue;
    }
    m = trimmed.match(/^Group:\s*(.+)/i);
    if (m) {
      details.push({ label: '渠道组', value: m[1] });
      continue;
    }
    m = trimmed.match(/^Primary quota used:\s*([\d.]+)%/i);
    if (m) {
      details.push({ label: '主配额使用率', value: `${m[1]}%` });
      continue;
    }
    m = trimmed.match(/^Secondary quota used:\s*([\d.]+)%/i);
    if (m) {
      details.push({ label: '副配额使用率', value: `${m[1]}%` });
      continue;
    }
    m = trimmed.match(/^Local quota used USD:\s*([\d.]+)\s*\/\s*limit\s*([\d.]+)/i);
    if (m) {
      details.push({ label: '本地配额(USD)', value: `已用 ${m[1]} / 上限 ${m[2]}` });
      continue;
    }
    m = trimmed.match(/^Daily used USD:\s*([\d.]+)\s*\/\s*limit\s*([\d.]+)/i);
    if (m) {
      details.push({ label: '今日用量(USD)', value: `已用 ${m[1]} / 上限 ${m[2]}` });
      continue;
    }
    m = trimmed.match(/^Last used:\s*(.+)/i);
    if (m) {
      details.push({ label: '最后使用时间', value: m[1] });
      continue;
    }
    m = trimmed.match(/^Evaluated at:\s*(.+)/i);
    if (m) {
      details.push({ label: '评估时间(UTC)', value: m[1] });
      continue;
    }
    if (/Quota snapshot recording is paused/i.test(trimmed)) {
      details.push({ label: '配额快照', value: '记录已暂停' });
      continue;
    }
  }
  return details;
}

/**
 * Parse channel-unavailable content.
 */
function parseChannelUnavailableContent(content: string): { label: string; value: string }[] {
  const details: { label: string; value: string }[] = [];
  const lines = content.split('\n');

  for (const line of lines) {
    const trimmed = line.trim();
    if (!trimmed) continue;

    let m = trimmed.match(/^Channel:\s*(.+?)\s*\(ID:\s*(\d+)\)/i);
    if (m) {
      details.push({ label: '渠道', value: `${m[1]} (ID: ${m[2]})` });
      continue;
    }
    m = trimmed.match(/^Group:\s*(.+)/i);
    if (m) {
      details.push({ label: '渠道组', value: m[1] });
      continue;
    }
    m = trimmed.match(/^Models:\s*(.+)/i);
    if (m) {
      details.push({ label: '模型', value: m[1] });
      continue;
    }
    m = trimmed.match(/^Consecutive failures:\s*(\d+)/i);
    if (m) {
      details.push({ label: '连续失败次数', value: `${m[1]} 次` });
      continue;
    }
    m = trimmed.match(/^Circuit opened until:\s*(.+)/i);
    if (m) {
      details.push({ label: '熔断至', value: m[1] });
      continue;
    }
    m = trimmed.match(/^Response time:\s*(\d+)ms/i);
    if (m) {
      details.push({ label: '响应时间', value: `${m[1]} 毫秒` });
      continue;
    }
    m = trimmed.match(/^Last error:\s*(.+)/i);
    if (m) {
      details.push({ label: '最近错误', value: m[1] });
      continue;
    }
  }
  return details;
}

// ---- Main entry ----

/**
 * Parse a raw notification (subject + content + status + last_error) into
 * structured Chinese display metadata.
 */
export function parseNotification(input: {
  subject?: string;
  content?: string;
  status?: string;
  last_error?: string;
}): ParsedNotification {
  const subject = input.subject ?? '';
  const content = input.content ?? '';
  const isError = input.status === 'failed';

  // Reconciliation
  const reconSubject = parseReconSubject(subject);
  if (reconSubject) {
    return {
      categoryLabel: '对账告警',
      summary: `对账发现 ${reconSubject.count} 项差异`,
      severity: isError ? 'error' : 'info',
      details: parseReconContent(content),
    };
  }

  // Quota alert
  const quotaSubject = parseQuotaAlertSubject(subject);
  if (quotaSubject) {
    const kindLabel = QUOTA_KIND_LABELS[quotaSubject.kind] ?? quotaSubject.kind;
    return {
      categoryLabel: '配额告警',
      summary: `订阅账户「${quotaSubject.account}」${kindLabel}`,
      severity: quotaSubject.kind === 'exhausted' ? 'error' : 'warning',
      details: parseQuotaAlertContent(content),
    };
  }

  // Channel unavailable
  if (/^Channel unavailable:/i.test(subject)) {
    const channelName = subject.replace(/^Channel unavailable:\s*/i, '').trim();
    return {
      categoryLabel: '渠道异常',
      summary: `渠道「${channelName}」不可用`,
      severity: 'error',
      details: parseChannelUnavailableContent(content),
    };
  }

  // Fallback: unknown notification shape — show raw text, still useful
  return {
    categoryLabel: '系统通知',
    summary: subject || '无主题',
    severity: isError ? 'error' : 'info',
    details: content
      ? content.split('\n').filter((l) => l.trim()).map((l) => ({ label: '', value: l.trim() }))
      : [],
  };
}
