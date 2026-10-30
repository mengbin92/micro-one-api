import { t } from '@/lib/i18n';

export const SERVICE_TIER_PRICING_KEY = 'ServiceTierPricing';
export const SERVICE_TIERS = ['default', 'priority', 'flex'] as const;
export const LONG_CONTEXT_POLICY = 'whole_request_input_gt_v1';
const PRICE_FIELDS = ['input_price', 'output_price', 'cache_read_price', 'cache_creation_5m_price', 'cache_creation_1h_price'] as const;

// Null prices make the disabled example safe to copy: enabling requires the
// administrator to replace every required price with a reviewed number.
export const SERVICE_TIER_PRICING_EXAMPLE = JSON.stringify({
  version: 1,
  enabled: false,
  source_kind: 'channel',
  source_id: 0,
  model: 'gpt-5.1',
  prices: Object.fromEntries(SERVICE_TIERS.map(tier => [tier, {
    input_price: null, output_price: null, cache_read_price: null,
    cache_creation_5m_price: 0, cache_creation_1h_price: 0,
  }])),
}, null, 2);

export const LONG_CONTEXT_PRICING_EXAMPLE = JSON.stringify({
  ...JSON.parse(SERVICE_TIER_PRICING_EXAMPLE),
  version: 2,
  source_id: 11,
  model: 'deepseek-flash',
  long_context: {
    threshold_tokens: 128000,
    policy: LONG_CONTEXT_POLICY,
    prices: Object.fromEntries(SERVICE_TIERS.map(tier => [tier, Object.fromEntries(PRICE_FIELDS.map(field => [field, null]))])),
  },
}, null, 2);

export const TIME_PRICING_EXAMPLE = JSON.stringify({
  ...JSON.parse(SERVICE_TIER_PRICING_EXAMPLE),
  version: 3,
  source_id: 11,
  model: 'deepseek-flash',
  time_pricing: {
    timezone: 'Asia/Shanghai',
    calendar_valid_through: '2026-12-31',
    holidays: [],
    peak_windows: [
      { weekdays: [1, 2, 3, 4, 5], start_minute: 540, end_minute: 720 },
      { weekdays: [1, 2, 3, 4, 5], start_minute: 840, end_minute: 1080 },
    ],
    peak_prices: Object.fromEntries(SERVICE_TIERS.map(tier => [tier, Object.fromEntries(PRICE_FIELDS.map(field => [field, null]))])),
  },
}, null, 2);

function object(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === 'object' && !Array.isArray(value);
}

function validatePrices(prices: unknown, key: string, allBuckets: boolean): asserts prices is Record<string, Record<string, number>> {
  if (!object(prices) || Object.keys(prices).length !== 3 || !SERVICE_TIERS.every(tier => object(prices[tier]))) {
    throw new Error(t('每组价格必须完整包含 default、priority、flex 三档'));
  }
  for (const tier of SERVICE_TIERS) {
    const price = prices[tier] as Record<string, unknown>;
    for (const field of PRICE_FIELDS) {
      if (!allBuckets && field.startsWith('cache_creation') && price[field] == null) continue;
      if (typeof price[field] !== 'number' || !Number.isFinite(price[field]) || price[field] < 0) {
        throw new Error(`${key}.${tier}.${field}: ${t('价格必须为非负数')}`);
      }
    }
  }
}

function validateLongPrices(base: Record<string, Record<string, number>>, long: Record<string, Record<string, number>>) {
  for (const tier of SERVICE_TIERS) {
    for (const field of PRICE_FIELDS) {
      const minimum = base[tier][field] ?? (field === 'cache_read_price' ? base[tier].input_price : 0);
      if (long[tier][field] < minimum) throw new Error(`${tier}.${field}: ${t('长上下文售价不得低于对应基础单价')}`);
    }
  }
}

function validCalendarDate(value: unknown): value is string {
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}$/.test(value)) return false;
  const date = new Date(`${value}T00:00:00Z`);
  return Number.isFinite(date.getTime()) && date.toISOString().slice(0, 10) === value;
}

function validateTimePricing(value: unknown, hasLongContext: boolean) {
  if (!object(value) || typeof value.timezone !== 'string' || !value.timezone.trim() || !validCalendarDate(value.calendar_valid_through) || !Array.isArray(value.holidays) || !value.holidays.every(day => validCalendarDate(day) && day <= String(value.calendar_valid_through)) || new Set(value.holidays).size !== value.holidays.length) {
    throw new Error(t('分时计价需有效 IANA 时区、日历有效期及不晚于有效期的不重复节假日日期'));
  }
  try { new Intl.DateTimeFormat('en-US', { timeZone: value.timezone }); }
  catch { throw new Error(t('分时计价时区无效')); }
  if (!Array.isArray(value.peak_windows) || !value.peak_windows.length) throw new Error(t('忙时窗口需 ISO 星期 1 至 7 及起止分钟；起点包含、终点不包含，跨日须拆开，窗口不可重叠'));
  const windows: { day: number; start: number; end: number }[] = [];
  for (const window of value.peak_windows) {
    if (!object(window) || !Array.isArray(window.weekdays) || !window.weekdays.length || !window.weekdays.every(day => Number.isInteger(day) && day >= 1 && day <= 7) || new Set(window.weekdays).size !== window.weekdays.length || !Number.isInteger(window.start_minute) || !Number.isInteger(window.end_minute) || Number(window.start_minute) < 0 || Number(window.end_minute) > 1440 || Number(window.start_minute) >= Number(window.end_minute)) {
      throw new Error(t('忙时窗口需 ISO 星期 1 至 7 及起止分钟；起点包含、终点不包含，跨日须拆开，窗口不可重叠'));
    }
    for (const day of window.weekdays) {
      const start = Number(window.start_minute), end = Number(window.end_minute);
      if (windows.some(previous => previous.day === day && start < previous.end && previous.start < end)) throw new Error(t('忙时窗口需 ISO 星期 1 至 7 及起止分钟；起点包含、终点不包含，跨日须拆开，窗口不可重叠'));
      windows.push({ day, start, end });
    }
  }
  validatePrices(value.peak_prices, 'time_pricing.peak_prices', true);
  if (value.peak_upstream_prices !== undefined) validatePrices(value.peak_upstream_prices, 'time_pricing.peak_upstream_prices', true);
  if (hasLongContext) {
    validatePrices(value.peak_long_context_prices, 'time_pricing.peak_long_context_prices', true);
    validateLongPrices(value.peak_prices, value.peak_long_context_prices);
    if (value.peak_long_context_upstream_prices !== undefined) validatePrices(value.peak_long_context_upstream_prices, 'time_pricing.peak_long_context_upstream_prices', true);
  } else if (value.peak_long_context_prices !== undefined || value.peak_long_context_upstream_prices !== undefined) {
    throw new Error(t('忙时长上下文价格仅可在配置 long_context 后填写'));
  }
}

export function validateServiceTierPricing(raw: string): string {
  const value: unknown = JSON.parse(raw);
  if (!object(value) || (value.enabled !== undefined && typeof value.enabled !== 'boolean')) {
    throw new Error(t('档位配置必须是 JSON 对象，enabled 必须为布尔值'));
  }
  if (!value.enabled) return JSON.stringify(value);
  const longContext = value.long_context;
  const timePricing = value.time_pricing;
  if (value.version !== (timePricing != null ? 3 : longContext == null ? 1 : 2) || value.source_kind !== 'channel' || !Number.isSafeInteger(value.source_id) || Number(value.source_id) <= 0 || typeof value.model !== 'string' || !value.model.trim() || value.model !== value.model.trim()) {
    throw new Error(t('启用档位计价需单一 channel 来源及有效模型 ID；基础配置 version=1，长上下文 version=2，含分时计价时 version=3'));
  }
  for (const key of ['prices', 'upstream_prices']) {
    if (key === 'upstream_prices' && value[key] === undefined) continue;
    validatePrices(value[key], key, false);
  }
  if (longContext != null) {
    if (!object(longContext) || longContext.policy !== LONG_CONTEXT_POLICY || !Number.isSafeInteger(longContext.threshold_tokens) || Number(longContext.threshold_tokens) <= 0 || Number(longContext.threshold_tokens) > 2147483647) {
      throw new Error(t('长上下文规则需 policy=whole_request_input_gt_v1，threshold_tokens 为 1 至 2147483647 的整数'));
    }
    validatePrices(longContext.prices, 'long_context.prices', true);
    if (longContext.upstream_prices !== undefined) validatePrices(longContext.upstream_prices, 'long_context.upstream_prices', true);
    validateLongPrices(value.prices as Record<string, Record<string, number>>, longContext.prices);
  }
  if (timePricing != null) validateTimePricing(timePricing, longContext != null);
  return JSON.stringify(value);
}

export function prettyServiceTierPricing(raw: string) {
  try { return JSON.stringify(JSON.parse(raw), null, 2); } catch { return raw; }
}
