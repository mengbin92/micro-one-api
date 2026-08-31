-- token-usage-billing-semantics-remediation (2026-08-31) §5.2 / §6.1.
-- Usage-semantics quarantine state keyed by execution source + upstream
-- model + adapter protocol. When a source repeatedly yields ambiguous usage
-- (default: 3 consecutive within 5 minutes), the key is paused (default 15
-- minutes) so a broken adapter cannot keep producing unverifiable bills.
--
-- This is a usage control-plane signal, deliberately separate from transport
-- health: the upstream HTTP call succeeded, so the channel health/circuit
-- breaker path must not be fed a fake failure. The database row is the
-- authoritative cross-instance state; selectors may cache it but recovery
-- must clear the persisted block and invalidate the cache.

CREATE TABLE IF NOT EXISTS `usage_semantic_source_blocks` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `source_kind` varchar(32) NOT NULL COMMENT 'channel | subscription',
  `source_id` bigint NOT NULL COMMENT 'channel_id or subscription_account_id',
  `upstream_model_id` varchar(191) NOT NULL,
  `adapter_protocol` varchar(32) NOT NULL DEFAULT '' COMMENT 'protocol the parser saw, e.g. openai_chat / anthropic_messages',
  `status` varchar(16) NOT NULL DEFAULT 'active' COMMENT 'active | blocked | resolved',
  `reason` varchar(64) NOT NULL DEFAULT '' COMMENT 'invariant reason that triggered the block',
  `window_started_at` datetime(3) NULL DEFAULT NULL COMMENT 'start of the current consecutive-ambiguous window',
  `consecutive_ambiguous` int NOT NULL DEFAULT 0,
  `blocked_until` datetime(3) NULL DEFAULT NULL,
  `last_verified_at` datetime(3) NULL DEFAULT NULL,
  `created_at` datetime(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  `updated_at` datetime(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_usage_semantic_block_key` (`source_kind`, `source_id`, `upstream_model_id`, `adapter_protocol`),
  KEY `idx_usage_semantic_block_status_until` (`status`, `blocked_until`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='Usage-semantics quarantine keyed by execution source and upstream model';
