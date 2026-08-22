-- See migrations/080_add_billing_ledger_cost_audit_fields.sql.

ALTER TABLE billing_ledgers
  ADD COLUMN IF NOT EXISTS source_kind varchar(32) NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS upstream_model_id varchar(191) NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS cost_audit_status varchar(16) NOT NULL DEFAULT 'legacy';

UPDATE billing_ledgers
SET source_kind = CASE
  WHEN subscription_account_id > 0 THEN 'subscription'
  WHEN channel_id > 0 THEN 'channel'
  ELSE ''
END
WHERE source_kind = '' AND type = 'consume';
