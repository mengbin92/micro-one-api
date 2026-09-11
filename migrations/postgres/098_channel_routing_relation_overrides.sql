ALTER TABLE channel_routing_groups ADD COLUMN priority_override BIGINT NULL;
ALTER TABLE channel_routing_groups ADD COLUMN weight_override BIGINT NULL;
ALTER TABLE account_routing_groups ADD COLUMN priority_override BIGINT NULL;
ALTER TABLE account_routing_groups ADD COLUMN weight_override BIGINT NULL;
