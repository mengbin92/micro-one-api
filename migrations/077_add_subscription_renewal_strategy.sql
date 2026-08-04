-- code-review M2: record the renewal strategy on user_subscriptions so the
-- "expired but not revoked" policy is explicit and observable instead of
-- drifting with the hourly expiry scan.
--
-- Behaviour (fixed in domain, not by this column): an unexpired active
-- subscription is extended in place (renewal_strategy='extend'); a user with
-- no active subscription — including one whose expires_at has passed and is
-- therefore filtered out by the expires_at > now guard (domain-C1) — gets a
-- brand-new subscription ('new'). This column lets operators and the
-- reconciliation see which of the two happened for any active row.
--
-- Additive (column only); rollback = DROP COLUMN renewal_strategy.

ALTER TABLE `user_subscriptions`
  ADD COLUMN `renewal_strategy` varchar(16) NOT NULL DEFAULT '' COMMENT 'extend|new; how the active row was granted';
