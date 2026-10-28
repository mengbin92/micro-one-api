import { describe, expect, it } from 'vitest';
import { SERVICE_TIER_PRICING_EXAMPLE, validateServiceTierPricing } from './service-tier-pricing';

const price = { input_price: 0.000001, output_price: 0.000002, cache_read_price: 0 };
const config = { version: 1, enabled: true, source_kind: 'channel', source_id: 7, model: 'gpt-5.1', prices: { default: price, priority: price, flex: price } };

describe('service tier pricing validation', () => {
  it('keeps the example disabled and requires reviewed prices before enabling', () => {
    expect(JSON.parse(validateServiceTierPricing(SERVICE_TIER_PRICING_EXAMPLE)).enabled).toBe(false);
    expect(() => validateServiceTierPricing(JSON.stringify({ ...JSON.parse(SERVICE_TIER_PRICING_EXAMPLE), enabled: true, source_id: 7 }))).toThrow();
  });
  it('accepts complete tariffs and explicit zero cache price', () => {
    expect(JSON.parse(validateServiceTierPricing(JSON.stringify({ ...config, upstream_prices: config.prices })))).toEqual({ ...config, upstream_prices: config.prices });
  });
  it.each([null, [], { enabled: 'false' }, { ...config, source_id: 0 }, { ...config, prices: { default: price } }, { ...config, upstream_prices: {} }, { ...config, prices: { default: { input_price: 1 }, priority: price, flex: price } }])('rejects incomplete or ambiguous configuration %j', (value) => {
    expect(() => validateServiceTierPricing(JSON.stringify(value))).toThrow();
  });
});
