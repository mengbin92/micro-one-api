-- token-usage-billing-semantics-remediation (2026-08-31) §6.1.
-- logs gains the same reported/canonical usage split and parse verdict as
-- billing_ledgers (085), minus candidate costs and pricing hash: the log is
-- the display/audit surface, not the financial decision record.
--
-- logs.quota keeps its legacy reported-total meaning during the compatibility
-- window; consumers must migrate to reported_total_tokens /
-- billable_total_tokens (§9.1). Existing rows keep usage_semantics='' /
-- usage_parse_status='legacy'; no semantics backfill from token arithmetic.

ALTER TABLE `logs`
  ADD COLUMN `uncached_input_tokens` bigint NOT NULL DEFAULT 0 AFTER `cache_creation_1h_tokens`,
  ADD COLUMN `reported_prompt_tokens` bigint NOT NULL DEFAULT 0 AFTER `uncached_input_tokens`,
  ADD COLUMN `reported_total_tokens` bigint NOT NULL DEFAULT 0 AFTER `reported_prompt_tokens`,
  ADD COLUMN `billable_total_tokens` bigint NOT NULL DEFAULT 0 AFTER `reported_total_tokens`,
  ADD COLUMN `usage_semantics` varchar(32) NOT NULL DEFAULT '' AFTER `billable_total_tokens`,
  ADD COLUMN `usage_protocol` varchar(32) NOT NULL DEFAULT '' AFTER `usage_semantics`,
  ADD COLUMN `usage_field_shape` varchar(64) NOT NULL DEFAULT '' AFTER `usage_protocol`,
  ADD COLUMN `usage_parse_status` varchar(16) NOT NULL DEFAULT 'legacy' AFTER `usage_field_shape`,
  ADD COLUMN `usage_contract_version` int NOT NULL DEFAULT 0 AFTER `usage_parse_status`,
  ADD COLUMN `canonical_present` tinyint(1) NOT NULL DEFAULT 0 AFTER `usage_contract_version`,
  ADD COLUMN `usage_decision_reason` varchar(64) NOT NULL DEFAULT '' AFTER `canonical_present`;
