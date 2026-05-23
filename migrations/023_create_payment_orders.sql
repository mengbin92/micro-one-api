-- Persist online payment orders for billing-service.

CREATE TABLE IF NOT EXISTS `payment_orders` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `user_id` bigint NOT NULL,
  `trade_no` varchar(128) NOT NULL,
  `channel` varchar(32) NOT NULL,
  `asset_type` varchar(32) NOT NULL,
  `asset_amount` bigint NOT NULL,
  `money_cents` bigint NOT NULL,
  `currency` varchar(16) NOT NULL DEFAULT 'CNY',
  `status` varchar(32) NOT NULL,
  `provider_trade_no` varchar(128) DEFAULT '',
  `provider_payload` text,
  `pay_url` text,
  `paid_at` datetime DEFAULT NULL,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  `asset_issue_status` varchar(32) NOT NULL DEFAULT 'pending',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_payment_orders_trade_no` (`trade_no`),
  KEY `idx_payment_orders_user_id` (`user_id`),
  KEY `idx_payment_orders_channel` (`channel`),
  KEY `idx_payment_orders_asset_type` (`asset_type`),
  KEY `idx_payment_orders_status` (`status`),
  KEY `idx_payment_orders_provider_trade_no` (`provider_trade_no`),
  KEY `idx_payment_orders_paid_at` (`paid_at`),
  KEY `idx_payment_orders_asset_issue_status` (`asset_issue_status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
