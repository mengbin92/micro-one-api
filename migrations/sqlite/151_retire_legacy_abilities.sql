-- Retire legacy abilities; registry mappings are the sole routing source.
ALTER TABLE model_subscription_mapping ADD COLUMN config TEXT DEFAULT '';

INSERT OR IGNORE INTO models (model_id, display_name, provider, model_type, status, is_public, created_at, updated_at)
SELECT LOWER(TRIM(a.model)), MIN(TRIM(a.model)), '', 'chat', 1, 1, CAST(strftime('%s','now') AS INTEGER)*1000, CAST(strftime('%s','now') AS INTEGER)*1000
FROM abilities a
WHERE TRIM(a.model) <> '' AND a.model NOT LIKE '%*%' AND a.model NOT LIKE '%?%'
GROUP BY LOWER(TRIM(a.model));

INSERT OR IGNORE INTO models (model_id, display_name, provider, model_type, status, is_public, created_at, updated_at)
SELECT LOWER(TRIM(saa.model)), MIN(TRIM(saa.model)), '', 'chat', 1, 1, CAST(strftime('%s','now') AS INTEGER)*1000, CAST(strftime('%s','now') AS INTEGER)*1000
FROM subscription_account_abilities saa
WHERE TRIM(saa.model) <> '' AND saa.model NOT LIKE '%*%' AND saa.model NOT LIKE '%?%'
GROUP BY LOWER(TRIM(saa.model));

INSERT OR IGNORE INTO model_channel_mapping
  (channel_id, model_id, upstream_model_id, enabled, priority, config, created_at, updated_at)
SELECT a.channel_id, m.id, MAX(TRIM(a.model)), 1, COALESCE(MAX(a.priority), 0),
       '{"_managed_by":"channel_models"}', CAST(strftime('%s','now') AS INTEGER)*1000, CAST(strftime('%s','now') AS INTEGER)*1000
FROM abilities a
JOIN channels c ON c.id = a.channel_id
JOIN models m ON LOWER(m.model_id) = LOWER(TRIM(a.model))
WHERE TRIM(a.model) <> '' AND a.model NOT LIKE '%*%' AND a.model NOT LIKE '%?%'
GROUP BY a.channel_id, m.id;

INSERT OR IGNORE INTO model_subscription_mapping
  (subscription_account_id, model_id, upstream_model_id, group_name, enabled, priority, config, created_at, updated_at)
SELECT saa.account_id, m.id, MAX(TRIM(saa.model)), saa."group", 1, COALESCE(MAX(saa.priority), 0),
       '{"_managed_by":"subscription_models"}', CAST(strftime('%s','now') AS INTEGER)*1000, CAST(strftime('%s','now') AS INTEGER)*1000
FROM subscription_account_abilities saa
JOIN subscription_accounts sa ON sa.id = saa.account_id
JOIN models m ON LOWER(m.model_id) = LOWER(TRIM(saa.model))
WHERE TRIM(saa.model) <> '' AND saa.model NOT LIKE '%*%' AND saa.model NOT LIKE '%?%'
GROUP BY saa.account_id, m.id, saa."group";

UPDATE model_subscription_mapping
SET routing_group_id = (SELECT id FROM routing_groups WHERE key = model_subscription_mapping.group_name)
WHERE routing_group_id IS NULL
  AND EXISTS (SELECT 1 FROM routing_groups WHERE key = model_subscription_mapping.group_name);

DROP TABLE IF EXISTS abilities;
DROP TABLE IF EXISTS subscription_account_abilities;

ALTER TABLE channels DROP COLUMN restrict_models;
