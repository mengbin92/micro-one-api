ALTER TABLE `billing_ledgers`
  ADD KEY `idx_billing_ledgers_type_created` (`type`, `created_at`),
  ADD KEY `idx_billing_ledgers_channel_created` (`channel_id`, `created_at`),
  ADD KEY `idx_billing_ledgers_model_created` (`model_name`, `created_at`);
