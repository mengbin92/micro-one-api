-- See migrations/080_add_billing_ledger_cost_audit_fields.sql.
-- The migrator treats duplicate-column errors as no-ops on replay.

ALTER TABLE billing_ledgers ADD COLUMN source_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE billing_ledgers ADD COLUMN upstream_model_id TEXT NOT NULL DEFAULT '';
ALTER TABLE billing_ledgers ADD COLUMN cost_audit_status TEXT NOT NULL DEFAULT 'legacy';

UPDATE billing_ledgers
SET source_kind = CASE
  WHEN subscription_account_id > 0 THEN 'subscription'
  WHEN channel_id > 0 THEN 'channel'
  ELSE ''
END
WHERE source_kind = '' AND type = 'consume';
