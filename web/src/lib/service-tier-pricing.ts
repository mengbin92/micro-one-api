import { t } from '@/lib/i18n';

export const SERVICE_TIER_PRICING_KEY = 'ServiceTierPricing';
export const SERVICE_TIERS = ['default', 'priority', 'flex'] as const;

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

function object(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === 'object' && !Array.isArray(value);
}

export function validateServiceTierPricing(raw: string): string {
  const value: unknown = JSON.parse(raw);
  if (!object(value) || (value.enabled !== undefined && typeof value.enabled !== 'boolean')) {
    throw new Error(t('档位配置必须是 JSON 对象，enabled 必须为布尔值'));
  }
  if (!value.enabled) return JSON.stringify(value);
  if (value.version !== 1 || value.source_kind !== 'channel' || !Number.isSafeInteger(value.source_id) || Number(value.source_id) <= 0 || typeof value.model !== 'string' || !value.model.trim() || value.model !== value.model.trim()) {
    throw new Error(t('启用档位计价需 version=1、单一 channel 来源及有效模型 ID'));
  }
  for (const key of ['prices', 'upstream_prices']) {
    if (key === 'upstream_prices' && value[key] === undefined) continue;
    const prices = value[key];
    if (!object(prices) || Object.keys(prices).length !== 3 || !SERVICE_TIERS.every(tier => object(prices[tier]))) {
      throw new Error(t('每组价格必须完整包含 default、priority、flex 三档'));
    }
    for (const tier of SERVICE_TIERS) {
      const price = prices[tier] as Record<string, unknown>;
      for (const field of ['input_price', 'output_price', 'cache_read_price', 'cache_creation_5m_price', 'cache_creation_1h_price']) {
        if (field.startsWith('cache_creation') && price[field] == null) continue;
        if (typeof price[field] !== 'number' || !Number.isFinite(price[field]) || price[field] < 0) {
          throw new Error(`${key}.${tier}.${field}: ${t('价格必须为非负数')}`);
        }
      }
    }
  }
  return JSON.stringify(value);
}

export function prettyServiceTierPricing(raw: string) {
  try { return JSON.stringify(JSON.parse(raw), null, 2); } catch { return raw; }
}
