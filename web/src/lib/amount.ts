export const AMOUNT_SCALE = 10000;

export function quotaPerUnitFromOptions(options?: Record<string, string> | null) {
  void options;
  return AMOUNT_SCALE;
}

export function amountUnitsToCurrencyUnits(value: number | string | undefined) {
  const parsed = Number(value ?? 0);
  if (!Number.isFinite(parsed)) return 0;
  return parsed / AMOUNT_SCALE;
}

export function currencyUnitsToAmountUnits(value: number | string | undefined) {
  const parsed = Number(value ?? 0);
  if (!Number.isFinite(parsed)) return 0;
  return Math.round(parsed * AMOUNT_SCALE);
}

export function quotaToCurrencyUnits(value: number | string | undefined, quotaPerUnit = AMOUNT_SCALE) {
  void quotaPerUnit;
  return amountUnitsToCurrencyUnits(value);
}

export function formatAmountUnits(value: number | string | undefined, digits = 4) {
  return amountUnitsToCurrencyUnits(value).toFixed(digits);
}

export function formatUSD(value: number | string | undefined, digits = 4) {
  return `$${formatAmountUnits(value, digits)}`;
}

export function formatCNY(value: number) {
  return Number.isFinite(value) ? `¥${value.toFixed(2)}` : '—';
}

// Subscription usage is already USD and retains up to eight decimal places.
export function formatSubscriptionUSD(value: number) {
  if (!Number.isFinite(value)) return '—';
  const fixed = value.toFixed(8);
  const [whole, fraction = ''] = fixed.split('.');
  const trimmed = fraction.replace(/0+$/, '');
  if (trimmed.length <= 2) return `$${whole}.${fraction.slice(0, 2)}`;
  return `$${whole}.${trimmed}`;
}
