ALTER TABLE channel_routing_groups ADD COLUMN priority_override INTEGER NULL;
ALTER TABLE channel_routing_groups ADD COLUMN weight_override INTEGER NULL;
ALTER TABLE account_routing_groups ADD COLUMN priority_override INTEGER NULL;
ALTER TABLE account_routing_groups ADD COLUMN weight_override INTEGER NULL;
