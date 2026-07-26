-- Keep the public/canonical model id separate from the exact identifier a
-- selected upstream requires (for example glm-5.2 -> z-ai/glm-5.2 on NVIDIA).
ALTER TABLE `model_channel_mapping`
  ADD COLUMN `upstream_model_id` varchar(255) NOT NULL DEFAULT '' COMMENT 'exact model identifier required by the channel' AFTER `model_id`;

ALTER TABLE `model_subscription_mapping`
  ADD COLUMN `upstream_model_id` varchar(255) NOT NULL DEFAULT '' COMMENT 'exact model identifier required by the subscription account' AFTER `model_id`;

-- Preserve the spelling already configured on legacy abilities for existing
-- canonical mappings. Newly discovered routes are populated by channel-service.
UPDATE `model_channel_mapping` mcm
JOIN `models` m ON m.id = mcm.model_id
JOIN `abilities` a ON a.channel_id = mcm.channel_id AND LOWER(a.model) = LOWER(m.model_id)
SET mcm.upstream_model_id = a.model
WHERE mcm.upstream_model_id = '';

UPDATE `model_subscription_mapping` msm
JOIN `models` m ON m.id = msm.model_id
JOIN `subscription_account_abilities` saa
  ON saa.account_id = msm.subscription_account_id
  AND saa.`group` = msm.group_name
  AND LOWER(saa.model) = LOWER(m.model_id)
SET msm.upstream_model_id = saa.model
WHERE msm.upstream_model_id = '';
