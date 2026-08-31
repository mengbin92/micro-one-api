-- token-usage-billing-semantics-remediation (2026-08-31) §6.1.
-- Separate upstream-reported usage from the canonical five mutually-exclusive
-- billing buckets, and persist the parse verdict so every ledger row stays
-- explainable (reported total vs billable total vs per-bucket costs).
--
-- Existing rows keep usage_semantics='' and usage_parse_status='legacy'.
-- Semantics MUST NOT be backfilled from token arithmetic (§6.1: the token
-- numeric relationship cannot prove the original protocol).

ALTER TABLE `billing_ledgers`
  ADD COLUMN `uncached_input_tokens` bigint NOT NULL DEFAULT 0 AFTER `cost_audit_status`,
  ADD COLUMN `reported_prompt_tokens` bigint NOT NULL DEFAULT 0 AFTER `uncached_input_tokens`,
  ADD COLUMN `reported_total_tokens` bigint NOT NULL DEFAULT 0 AFTER `reported_prompt_tokens`,
  ADD COLUMN `billable_total_tokens` bigint NOT NULL DEFAULT 0 AFTER `reported_total_tokens`,
  ADD COLUMN `usage_semantics` varchar(32) NOT NULL DEFAULT '' AFTER `billable_total_tokens`,
  ADD COLUMN `usage_protocol` varchar(32) NOT NULL DEFAULT '' AFTER `usage_semantics`,
  ADD COLUMN `usage_field_shape` varchar(64) NOT NULL DEFAULT '' AFTER `usage_protocol`,
  ADD COLUMN `usage_parse_status` varchar(16) NOT NULL DEFAULT 'legacy' AFTER `usage_field_shape`,
  ADD COLUMN `usage_contract_version` int NOT NULL DEFAULT 0 AFTER `usage_parse_status`,
  ADD COLUMN `canonical_present` tinyint(1) NOT NULL DEFAULT 0 AFTER `usage_contract_version`,
  ADD COLUMN `usage_decision_reason` varchar(64) NOT NULL DEFAULT '' AFTER `canonical_present`,
  ADD COLUMN `subset_candidate_cost` bigint NOT NULL DEFAULT 0 AFTER `usage_decision_reason`,
  ADD COLUMN `exclusive_candidate_cost` bigint NOT NULL DEFAULT 0 AFTER `subset_candidate_cost`;
