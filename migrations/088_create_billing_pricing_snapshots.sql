-- token-usage-billing-semantics-remediation (2026-08-31) §6.3 phase 2.
-- Immutable pricing evidence: the exact per-bucket unit prices, group ratio
-- and cache-creation mode a consume ledger row was charged with. dynamic
-- system_options.ModelPrice overwrites old values, so historical re-pricing
-- could previously only reverse-derive prices from bucket costs.
--
-- billing_ledgers.pricing_config_hash references the snapshot used for that
-- row; the snapshot is claimed inside the SAME transaction as the ledger
-- insert, and an identical hash silently reuses the existing snapshot. The
-- column is deliberately NOT indexed: billing_ledgers is RANGE-partitioned
-- and hash lookups are rare per-ledger audit reads, not a hot path.
--
-- Rows written before 088 (and ratio-priced models, which have no per-bucket
-- ModelPrice) keep pricing_config_hash=''; their prices stay unknowable and
-- MUST NOT be guessed from ledger amounts (§8.2).
--
-- Prices/ratio are decimal(32,17): enough to round-trip the float64 values
-- the pricing function actually consumed. Additive (table + column);
-- rollback = drop both, never rewrite history.

CREATE TABLE IF NOT EXISTS `billing_pricing_snapshots` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `config_hash` char(64) NOT NULL COMMENT 'sha256 of the snapshot payload; identical charge inputs collapse to one row',
  `model_name` varchar(191) NOT NULL COMMENT 'normalized pricing key the lookup used',
  `input_price` decimal(32,17) NOT NULL DEFAULT 0 COMMENT 'effective per-token price actually charged',
  `output_price` decimal(32,17) NOT NULL DEFAULT 0,
  `cache_read_price` decimal(32,17) NOT NULL DEFAULT 0 COMMENT 'InputPrice fallback already applied',
  `cache_creation_5m_price` decimal(32,17) NOT NULL DEFAULT 0 COMMENT '0 when the bucket was unpriced',
  `cache_creation_1h_price` decimal(32,17) NOT NULL DEFAULT 0,
  `group_ratio` decimal(32,17) NOT NULL DEFAULT 1,
  `cache_creation_mode` varchar(16) NOT NULL DEFAULT 'observe' COMMENT 'charge | observe; changes the settled cost so it participates in the hash',
  `snapshot_version` int NOT NULL DEFAULT 1 COMMENT 'hash payload format version',
  `created_at` datetime(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_billing_pricing_snapshots_hash` (`config_hash`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='Per-request pricing evidence referenced by billing_ledgers.pricing_config_hash';

ALTER TABLE `billing_ledgers`
  ADD COLUMN `pricing_config_hash` char(64) NOT NULL DEFAULT '' AFTER `exclusive_candidate_cost`;
