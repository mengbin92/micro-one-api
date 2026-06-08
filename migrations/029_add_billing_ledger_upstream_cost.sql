ALTER TABLE `billing_ledgers`
  ADD COLUMN `upstream_cost` bigint DEFAULT 0 AFTER `amount`,
  ADD KEY `idx_billing_ledgers_upstream_cost_created` (`upstream_cost`, `created_at`);
