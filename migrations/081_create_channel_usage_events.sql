-- Exactly-once channel counter claims. The claim and used_quota update are
-- committed in one transaction, keyed by the billing reservation ID.

CREATE TABLE IF NOT EXISTS `channel_usage_events` (
  `reservation_id` varchar(64) NOT NULL,
  `channel_id` bigint NOT NULL,
  `quota` bigint NOT NULL,
  `created_at` datetime(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`reservation_id`),
  KEY `idx_channel_usage_events_channel_created` (`channel_id`, `created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='Idempotency claims for channel usage counters';
