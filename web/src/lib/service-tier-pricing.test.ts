import { describe, expect, it } from 'vitest';
import { LONG_CONTEXT_POLICY, LONG_CONTEXT_PRICING_EXAMPLE, SERVICE_TIER_PRICING_EXAMPLE, TIME_PRICING_EXAMPLE, validateServiceTierPricing } from './service-tier-pricing';

const price = { input_price: 0.000001, output_price: 0.000002, cache_read_price: 0 };
const config = { version: 1, enabled: true, source_kind: 'channel', source_id: 7, model: 'gpt-5.1', prices: { default: price, priority: price, flex: price } };
const longPrice = { ...price, cache_creation_5m_price: 0, cache_creation_1h_price: 0 };
const longContext = { policy: LONG_CONTEXT_POLICY, threshold_tokens: 128000, prices: { default: longPrice, priority: longPrice, flex: longPrice } };
const longConfig = { ...config, version: 2, long_context: longContext };
const timePricing = {
  timezone: 'Asia/Shanghai', calendar_valid_through: '2027-12-31', holidays: ['2027-01-01'],
  peak_windows: [{ weekdays: [1, 2, 3, 4, 5], start_minute: 540, end_minute: 720 }],
  peak_prices: { default: longPrice, priority: longPrice, flex: longPrice },
};
const timeConfig = { ...config, version: 3, time_pricing: timePricing };

describe('service tier pricing validation', () => {
  it('keeps the example disabled and requires reviewed prices before enabling', () => {
    expect(JSON.parse(validateServiceTierPricing(SERVICE_TIER_PRICING_EXAMPLE)).enabled).toBe(false);
    expect(() => validateServiceTierPricing(JSON.stringify({ ...JSON.parse(SERVICE_TIER_PRICING_EXAMPLE), enabled: true, source_id: 7 }))).toThrow();
  });
  it('accepts complete tariffs and explicit zero cache price', () => {
    expect(JSON.parse(validateServiceTierPricing(JSON.stringify({ ...config, upstream_prices: config.prices })))).toEqual({ ...config, upstream_prices: config.prices });
  });
  it('keeps the selected-model long-context example disabled without inventing real prices', () => {
    const example = JSON.parse(validateServiceTierPricing(LONG_CONTEXT_PRICING_EXAMPLE));
    expect(example).toMatchObject({ version: 2, enabled: false, source_id: 11, model: 'deepseek-flash', long_context: { threshold_tokens: 128000 } });
    expect(example.long_context.prices.default.input_price).toBeNull();
    expect(() => validateServiceTierPricing(JSON.stringify({ ...example, enabled: true }))).toThrow();
  });
  it('accepts complete v2 tariffs, including explicit zero creation prices', () => {
    expect(JSON.parse(validateServiceTierPricing(JSON.stringify(longConfig)))).toEqual(longConfig);
    const withProcurement = { ...longConfig, long_context: { ...longContext, upstream_prices: longContext.prices } };
    expect(JSON.parse(validateServiceTierPricing(JSON.stringify(withProcurement)))).toEqual(withProcurement);
  });
  it('keeps the time-pricing example disabled with blank prices and a finite calendar', () => {
    const example = JSON.parse(validateServiceTierPricing(TIME_PRICING_EXAMPLE));
    expect(example).toMatchObject({ version: 3, enabled: false, source_id: 11, model: 'deepseek-flash', time_pricing: { timezone: 'Asia/Shanghai', calendar_valid_through: '2026-12-31' } });
    expect(example.time_pricing.peak_prices.default.input_price).toBeNull();
    expect(() => validateServiceTierPricing(JSON.stringify({ ...example, enabled: true }))).toThrow();
  });
  it('accepts standalone time pricing, adjacent windows and complete procurement prices', () => {
    const time_pricing = { ...timePricing, peak_upstream_prices: timePricing.peak_prices, peak_windows: [...timePricing.peak_windows, { weekdays: [1], start_minute: 720, end_minute: 1440 }] };
    const value = { ...timeConfig, time_pricing };
    expect(JSON.parse(validateServiceTierPricing(JSON.stringify(value)))).toEqual(value);
  });
  it('accepts combined context/time pricing with peak prices below off-peak and long prices above peak base', () => {
    const zero = Object.fromEntries(Object.keys(longPrice).map(field => [field, 0]));
    const value = { ...longConfig, version: 3, time_pricing: { ...timePricing, peak_prices: { default: zero, priority: zero, flex: zero }, peak_long_context_prices: longContext.prices, peak_long_context_upstream_prices: longContext.prices } };
    expect(JSON.parse(validateServiceTierPricing(JSON.stringify(value)))).toEqual(value);
  });
  it.each([
    { ...timeConfig, version: 1 },
    { ...config, version: 3 },
    { ...timeConfig, time_pricing: { ...timePricing, timezone: 'Unknown/Zone' } },
    { ...timeConfig, time_pricing: { ...timePricing, calendar_valid_through: '2027-02-30' } },
    { ...timeConfig, time_pricing: { ...timePricing, holidays: ['2028-01-01'] } },
    { ...timeConfig, time_pricing: { ...timePricing, holidays: ['2027-01-01', '2027-01-01'] } },
    { ...timeConfig, time_pricing: { ...timePricing, peak_prices: config.prices } },
    { ...timeConfig, time_pricing: { ...timePricing, peak_upstream_prices: config.prices } },
    { ...timeConfig, time_pricing: { ...timePricing, peak_long_context_prices: longContext.prices } },
    { ...longConfig, version: 3, time_pricing: timePricing },
    { ...longConfig, version: 3, time_pricing: { ...timePricing, peak_long_context_prices: { ...longContext.prices, default: { ...longPrice, input_price: 0 } } } },
  ])('rejects incomplete or mismatched time/context configuration %j', value => {
    expect(() => validateServiceTierPricing(JSON.stringify(value))).toThrow();
  });
  it.each([
    [],
    [{ weekdays: [0], start_minute: 540, end_minute: 720 }],
    [{ weekdays: [1, 1], start_minute: 540, end_minute: 720 }],
    [{ weekdays: [1], start_minute: -1, end_minute: 720 }],
    [{ weekdays: [1], start_minute: 540, end_minute: 1441 }],
    [{ weekdays: [1], start_minute: 1080, end_minute: 540 }],
    [{ weekdays: [1], start_minute: 540, end_minute: 540 }],
    [...timePricing.peak_windows, { weekdays: [1], start_minute: 600, end_minute: 800 }],
  ].map(peak_windows => ({ peak_windows })))('rejects invalid or overlapping time windows %j', ({ peak_windows }) => {
    expect(() => validateServiceTierPricing(JSON.stringify({ ...timeConfig, time_pricing: { ...timePricing, peak_windows } }))).toThrow();
  });
  it.each([0, -1, 1.5, 2147483648, null, '128000'])('rejects invalid thresholds %s', threshold_tokens => {
    expect(() => validateServiceTierPricing(JSON.stringify({ ...longConfig, long_context: { ...longContext, threshold_tokens } }))).toThrow();
  });
  it.each([
    { ...config, version: 2 },
    { ...config, long_context: longContext },
    { ...longConfig, long_context: { ...longContext, policy: 'overage_only' } },
    { ...longConfig, long_context: { ...longContext, prices: config.prices } },
    { ...longConfig, long_context: { ...longContext, prices: { ...longContext.prices, flex: { ...longPrice, input_price: null } } } },
    { ...longConfig, long_context: { ...longContext, upstream_prices: config.prices } },
    { ...longConfig, long_context: { ...longContext, upstream_prices: null } },
  ])('rejects incomplete long-context rule %j', value => {
    expect(() => validateServiceTierPricing(JSON.stringify(value))).toThrow();
  });
  it.each(['input_price', 'output_price', 'cache_read_price', 'cache_creation_5m_price', 'cache_creation_1h_price'])('rejects a lower long-context sale price in %s', field => {
    const base = { ...longPrice, [field]: 1 };
    const value = { ...longConfig, prices: { default: base, priority: base, flex: base }, long_context: { ...longContext, prices: { ...longContext.prices, default: { ...base, [field]: 0 } } } };
    expect(() => validateServiceTierPricing(JSON.stringify(value))).toThrow(/长上下文售价/);
  });
  it.each([null, [], { enabled: 'false' }, { ...config, source_id: 0 }, { ...config, prices: { default: price } }, { ...config, upstream_prices: {} }, { ...config, prices: { default: { input_price: 1 }, priority: price, flex: price } }])('rejects incomplete or ambiguous configuration %j', (value) => {
    expect(() => validateServiceTierPricing(JSON.stringify(value))).toThrow();
  });
});
