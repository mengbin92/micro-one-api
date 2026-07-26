ALTER TABLE model_channel_mapping
  ADD COLUMN IF NOT EXISTS upstream_model_id VARCHAR(255) NOT NULL DEFAULT '';

ALTER TABLE model_subscription_mapping
  ADD COLUMN IF NOT EXISTS upstream_model_id VARCHAR(255) NOT NULL DEFAULT '';

UPDATE model_channel_mapping mcm
SET upstream_model_id = a.model
FROM models m, abilities a
WHERE m.id = mcm.model_id
  AND a.channel_id = mcm.channel_id
  AND LOWER(a.model) = LOWER(m.model_id)
  AND mcm.upstream_model_id = '';

UPDATE model_subscription_mapping msm
SET upstream_model_id = saa.model
FROM models m, subscription_account_abilities saa
WHERE m.id = msm.model_id
  AND saa.account_id = msm.subscription_account_id
  AND saa."group" = msm.group_name
  AND LOWER(saa.model) = LOWER(m.model_id)
  AND msm.upstream_model_id = '';
