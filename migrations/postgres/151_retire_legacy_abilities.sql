-- Retire legacy abilities; registry mappings are the sole routing source.
ALTER TABLE model_subscription_mapping ADD COLUMN IF NOT EXISTS config TEXT DEFAULT '';

INSERT INTO models (model_id, display_name, provider, model_type, status, is_public, created_at, updated_at)
SELECT LOWER(TRIM(x.model)), MIN(TRIM(x.model)), '', 'chat', 1, TRUE,
       (EXTRACT(EPOCH FROM CURRENT_TIMESTAMP)*1000)::BIGINT, (EXTRACT(EPOCH FROM CURRENT_TIMESTAMP)*1000)::BIGINT
FROM (
  SELECT model FROM abilities
  UNION ALL
  SELECT model FROM subscription_account_abilities
) x
WHERE TRIM(x.model) <> '' AND x.model NOT LIKE '%*%' AND x.model NOT LIKE '%?%'
  AND NOT EXISTS (SELECT 1 FROM models m WHERE LOWER(m.model_id) = LOWER(TRIM(x.model)))
GROUP BY LOWER(TRIM(x.model))
ON CONFLICT DO NOTHING;

INSERT INTO model_channel_mapping
  (channel_id, model_id, upstream_model_id, enabled, priority, config, created_at, updated_at)
SELECT a.channel_id, m.id, MAX(TRIM(a.model)), TRUE, COALESCE(MAX(a.priority), 0),
       '{"_managed_by":"channel_models"}', (EXTRACT(EPOCH FROM CURRENT_TIMESTAMP)*1000)::BIGINT, (EXTRACT(EPOCH FROM CURRENT_TIMESTAMP)*1000)::BIGINT
FROM abilities a
JOIN channels c ON c.id = a.channel_id
JOIN models m ON LOWER(m.model_id) = LOWER(TRIM(a.model))
WHERE TRIM(a.model) <> '' AND a.model NOT LIKE '%*%' AND a.model NOT LIKE '%?%'
GROUP BY a.channel_id, m.id
ON CONFLICT (channel_id, model_id) DO NOTHING;

INSERT INTO model_subscription_mapping
  (subscription_account_id, model_id, upstream_model_id, group_name, enabled, priority, config, created_at, updated_at)
SELECT saa.account_id, m.id, MAX(TRIM(saa.model)), saa."group", TRUE, COALESCE(MAX(saa.priority), 0),
       '{"_managed_by":"subscription_models"}', (EXTRACT(EPOCH FROM CURRENT_TIMESTAMP)*1000)::BIGINT, (EXTRACT(EPOCH FROM CURRENT_TIMESTAMP)*1000)::BIGINT
FROM subscription_account_abilities saa
JOIN subscription_accounts sa ON sa.id = saa.account_id
JOIN models m ON LOWER(m.model_id) = LOWER(TRIM(saa.model))
WHERE TRIM(saa.model) <> '' AND saa.model NOT LIKE '%*%' AND saa.model NOT LIKE '%?%'
GROUP BY saa.account_id, m.id, saa."group"
ON CONFLICT (subscription_account_id, model_id, group_name) DO NOTHING;

UPDATE model_subscription_mapping msm
SET routing_group_id = rg.id
FROM routing_groups rg
WHERE rg.key = msm.group_name AND msm.routing_group_id IS NULL;

DROP TABLE IF EXISTS abilities;
DROP TABLE IF EXISTS subscription_account_abilities;

ALTER TABLE channels DROP COLUMN IF EXISTS restrict_models;
