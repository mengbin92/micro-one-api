-- Subscription product plans. Groups remain the entitlement/quota container;
-- plans are the purchasable product layer that points to a group.

CREATE TABLE IF NOT EXISTS `subscription_plans` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `group_id` bigint NOT NULL,
  `name` varchar(100) NOT NULL,
  `description` text NOT NULL,
  `price_quota` bigint NOT NULL DEFAULT 0,
  `original_price` bigint DEFAULT NULL,
  `validity_days` int NOT NULL DEFAULT 30,
  `validity_unit` varchar(10) NOT NULL DEFAULT 'day',
  `features` text NOT NULL,
  `product_name` varchar(100) NOT NULL DEFAULT '',
  `for_sale` tinyint(1) NOT NULL DEFAULT 1,
  `sort_order` int NOT NULL DEFAULT 0,
  `created_at` bigint NOT NULL DEFAULT 0,
  `updated_at` bigint NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`),
  KEY `idx_subscription_plans_group_id` (`group_id`),
  KEY `idx_subscription_plans_for_sale` (`for_sale`),
  KEY `idx_subscription_plans_sort` (`sort_order`, `id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

ALTER TABLE `payment_orders`
  ADD COLUMN `plan_id` bigint NOT NULL DEFAULT 0,
  ADD KEY `idx_payment_orders_plan_id` (`plan_id`);
