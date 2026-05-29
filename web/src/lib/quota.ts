const DEFAULT_QUOTA_PER_UNIT = 500000;

export function quotaPerUnitFromOptions(options?: Record<string, string> | null) {
  const parsed = Number(options?.QuotaPerUnit);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : DEFAULT_QUOTA_PER_UNIT;
}

export function quotaToCurrencyUnits(value: number | string | undefined, quotaPerUnit = DEFAULT_QUOTA_PER_UNIT) {
  const parsed = Number(value ?? 0);
  if (!Number.isFinite(parsed)) return 0;
  return parsed / quotaPerUnit;
}
