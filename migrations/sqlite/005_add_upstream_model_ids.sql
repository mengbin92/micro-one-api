ALTER TABLE model_channel_mapping
  ADD COLUMN upstream_model_id TEXT NOT NULL DEFAULT '';

ALTER TABLE model_subscription_mapping
  ADD COLUMN upstream_model_id TEXT NOT NULL DEFAULT '';

UPDATE model_channel_mapping
SET upstream_model_id = COALESCE((
  SELECT a.model
  FROM abilities a
  JOIN models m ON m.id = model_channel_mapping.model_id
  WHERE a.channel_id = model_channel_mapping.channel_id
    AND LOWER(a.model) = LOWER(m.model_id)
  LIMIT 1
), '')
WHERE upstream_model_id = '';

UPDATE model_subscription_mapping
SET upstream_model_id = COALESCE((
  SELECT saa.model
  FROM subscription_account_abilities saa
  JOIN models m ON m.id = model_subscription_mapping.model_id
  WHERE saa.account_id = model_subscription_mapping.subscription_account_id
    AND saa.`group` = model_subscription_mapping.group_name
    AND LOWER(saa.model) = LOWER(m.model_id)
  LIMIT 1
), '')
WHERE upstream_model_id = '';
