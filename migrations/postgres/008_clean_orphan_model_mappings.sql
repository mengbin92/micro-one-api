-- v0.11.0 review L4: remove orphaned model mappings whose parent channel or
-- subscription account no longer exists, then add ON DELETE CASCADE foreign
-- keys so future deletions keep the mapping tables consistent.
--
-- Mirrors MySQL migration 070_clean_orphan_model_mappings.sql.

DELETE FROM model_channel_mapping
WHERE channel_id NOT IN (SELECT id FROM channels);

DELETE FROM model_subscription_mapping
WHERE subscription_account_id NOT IN (SELECT id FROM subscription_accounts);

ALTER TABLE model_channel_mapping
  ADD CONSTRAINT fk_mcm_channel
  FOREIGN KEY (channel_id) REFERENCES channels(id) ON DELETE CASCADE;

ALTER TABLE model_subscription_mapping
  ADD CONSTRAINT fk_msm_account
  FOREIGN KEY (subscription_account_id) REFERENCES subscription_accounts(id) ON DELETE CASCADE;
