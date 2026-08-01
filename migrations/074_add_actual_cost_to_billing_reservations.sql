-- v0.11.x billing-L1: persist the real settlement cost on a reservation.
--
-- The reservation's `amount`/`balance_amount_quota` columns store the
-- pre-deduction ESTIMATE captured at reserve time. An idempotent retry of
-- CommitQuota on an already-committed reservation previously returned that
-- estimate as the committed amount, misreporting usage whenever actual !=
-- estimate. This adds a dedicated `actual_cost` column written at commit time
-- (immediately before the final CAS to "committed") so the retry path returns
-- the authoritative settled cost. Additive only; rollback = DROP COLUMN.

ALTER TABLE `billing_reservations`
  ADD COLUMN `actual_cost` BIGINT NOT NULL DEFAULT 0;
