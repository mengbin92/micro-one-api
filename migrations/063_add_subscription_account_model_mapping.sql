-- Add per-account model mapping for subscription accounts.
--
-- Mirrors channels.model_mapping (migration 018) so that OAuth-backed
-- subscription accounts can remap client-facing model names to upstream
-- real model names on a per-account basis. Different upstream providers
-- may expose the same client model name (e.g. "claude-sonnet-4-5") but
-- require different upstream model identifiers — this column stores a JSON
-- {"src":"dst"} map applied after the global ModelMapper resolves the
-- request model and before the relay forwards to the upstream.
--
-- See docs/model-management-design.md §10.1.
ALTER TABLE `subscription_accounts`
  ADD COLUMN `model_mapping` varchar(1024) NOT NULL DEFAULT '' COMMENT 'JSON {"src":"dst"} per-account model name remap';
