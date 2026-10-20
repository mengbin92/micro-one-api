export const vendorReport = {
  id: 'report-' + 'a'.repeat(64),
  manifest: {
    version: 'v2', evidence_id: 'invoice-' + 'b'.repeat(128), supersedes: 'previous-report',
    scope: { provider: 'supplier', account: 'account', project: 'project', currency: 'USD', unit: 'currency', start: '2026-10-01T00:00:00Z', end: '2026-10-02T00:00:00Z', timezone: 'UTC', granularity: 'account_period' },
    complete: false, pages_complete: true,
    vendor_watermark: '2026-10-02T00:00:00Z', local_watermark: '2026-10-01T23:00:00Z', coverage_evidence_id: 'coverage-proof',
    mappings: [{ source_kind: 'channel', source_id: 7, provider: 'supplier', account: 'account', project: 'project', start: '2026-10-01T00:00:00Z', end: '2026-10-02T00:00:00Z', revision: 'mapping-v1', evidence_id: 'mapping-proof-' + 'c'.repeat(128) }],
    absolute_tolerance: '0', relative_tolerance: '0.000001', rounding_evidence_id: 'rounding-proof',
  },
  status: 'pending', cost_basis: 'provider_reported', verification_scope: 'account_period',
  unknown_time_attempts: 1, unmapped_attempts: 2, internal_attempts: 3, user_reservations: 4, correction_delta: '-0.000000000000000001',
  comparisons: [{ key: 'period', status: 'pending', usage_status: 'unknown', amount_status: 'variance', vendor_amount: '0', estimated_amount: '0.000000000000000001', difference: '-0.000000000000000001', unexplained: null, unknown_amounts: 2,
    purpose_counts: { user: 4, canary: 3 },
    attributions: [{ category: 'rounding', status: 'hypothesis', amount: '-0.000000000000000001', evidence_id: 'attribution-proof', verification_scope: 'account_period' }],
  }],
};
export const vendorPayload = {
  reports: [vendorReport], daily_coverage: [{ date: '2026-10-01', status: 'unknown' }],
  weekly_review: { pending: 1, attention: 0, unknown_amounts: 2, items: [{ report_id: vendorReport.id, status: 'pending', age_seconds: 3600, unknown_amounts: 2, next_action: 'complete_watermarks_or_late_evidence' }] },
};
