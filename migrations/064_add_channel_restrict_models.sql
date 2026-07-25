-- Add restrict_models flag to channels.
--
-- When false, the channel acts as a catch-all: any model not registered in
-- the abilities table is still routable to this channel (sub2api-style
-- "RestrictModels=false"). When true (the legacy default), only models
-- explicitly listed in abilities may be selected for this channel.
--
-- This gives administrators the option to "pass through unregistered models"
-- without pre-registering every upstream model. See
-- docs/model-management-design.md §9.3 #2 / §9.4 P1.
ALTER TABLE `channels`
  ADD COLUMN `restrict_models` tinyint NOT NULL DEFAULT 1 COMMENT 'restrict to abilities list: 0=allow all models (catch-all), 1=require registered model';
