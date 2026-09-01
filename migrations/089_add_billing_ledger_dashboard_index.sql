-- User dashboard filters consume ledgers by user and type, then groups by
-- created_at. The existing (user_id, created_at, model_name) index cannot
-- narrow the consume-only range before scanning it.
ALTER TABLE `billing_ledgers`
  ADD KEY `idx_billing_ledgers_user_type_created` (`user_id`, `type`, `created_at`);
