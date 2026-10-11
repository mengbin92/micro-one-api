import { describe, expect, it } from 'vitest';
import { AMOUNT_SCALE, amountUnitsToCurrencyUnits, currencyUnitsToAmountUnits, formatAmountUnits, formatCNY, formatSubscriptionUSD, formatUSD, quotaPerUnitFromOptions, quotaToCurrencyUnits } from './amount';

describe('ledger amount display boundaries', () => {
  it('uses the fixed ledger scale even when old quota options are present', () => {
    expect(AMOUNT_SCALE).toBe(10000);
    expect(quotaPerUnitFromOptions({ QuotaPerUnit: '500000' })).toBe(10000);
    expect(quotaPerUnitFromOptions()).toBe(10000);
    expect(quotaToCurrencyUnits('10000', 500000)).toBe(1);
    expect(quotaToCurrencyUnits(10000)).toBe(1);
  });
  it.each([[0, 0], [1, 0.0001], ['-12345', -1.2345], [undefined, 0], ['invalid', 0], [Infinity, 0]] as const)('displays %s ledger units as %s', (input, output) => {
    expect(amountUnitsToCurrencyUnits(input)).toBe(output);
  });
  it.each([[0, 0], ['0.0001', 1], [-1.2345, -12345], ['1.23456', 12346], [undefined, 0], ['invalid', 0], [Infinity, 0]] as const)('converts %s currency units to %s ledger units', (input, output) => {
    expect(currencyUnitsToAmountUnits(input)).toBe(output);
  });
  it('preserves zero, tiny amounts, and negative refunds in the displayed precision', () => {
    expect(formatAmountUnits(0)).toBe('0.0000');
    expect(formatUSD(1)).toBe('$0.0001');
    expect(formatUSD(-1, 4)).toBe('$-0.0001');
    expect(formatAmountUnits('12345', 2)).toBe('1.23');
  });
});

describe('subscription USD display', () => {
  it.each([
    [0, '$0.00'],
    [0.00000001, '$0.00000001'],
    [1.0249, '$1.0249'],
    [499.999998, '$499.999998'],
    [500, '$500.00'],
    [-1.0249, '$-1.0249'],
  ] as const)('preserves the stored precision of %s USD', (value, expected) => {
    expect(formatSubscriptionUSD(value)).toBe(expected);
  });

  it.each([NaN, Infinity, -Infinity])('does not show an unknown amount %s as zero', value => {
    expect(formatSubscriptionUSD(value)).toBe('—');
  });
});

describe('CNY price display', () => {
  it.each([[0, '¥0.00'], [100, '¥100.00'], [1.005, '¥1.00'], [-2.5, '¥-2.50']] as const)('rounds %s CNY to fen', (value, expected) => {
    expect(formatCNY(value)).toBe(expected);
  });

  it.each([NaN, Infinity, -Infinity])('does not show an unknown price %s as zero', value => {
    expect(formatCNY(value)).toBe('—');
  });
});
