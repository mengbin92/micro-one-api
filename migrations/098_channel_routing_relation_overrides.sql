-- Phase F: intra-group priority/weight overrides on resource relations.
-- NULL = inherit the resource's own priority/weight; all-NULL keeps the
-- pre-F scheduling distribution unchanged.
ALTER TABLE channel_routing_groups ADD COLUMN priority_override BIGINT NULL;
ALTER TABLE channel_routing_groups ADD COLUMN weight_override BIGINT NULL;
ALTER TABLE account_routing_groups ADD COLUMN priority_override BIGINT NULL;
ALTER TABLE account_routing_groups ADD COLUMN weight_override BIGINT NULL;
