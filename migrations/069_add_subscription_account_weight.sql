-- v0.11.0 Phase 3 §3.1: explicit within-tier weight for subscription accounts.
--
-- priority has been overloaded: it both layers tiers (higher = preferred) AND
-- derived the within-tier smooth-WRR weight, so configured ratios collapsed to
-- the hard-coded 1 whenever an operator only set priority. This adds a
-- dedicated weight column so layering and intra-tier distribution are
-- independent. The selector (accountSelectorWeight) prefers weight, falls back
-- to priority-derived, then 1 — so existing deployments keep working.
--
-- Additive only: a new column defaulting to 0 (unset). Rollback = DROP COLUMN
-- (no destructive down migration).

ALTER TABLE `subscription_accounts`
  ADD COLUMN `weight` int NOT NULL DEFAULT 0 COMMENT 'within-tier WRR weight (0=unset, falls back to priority)';
