-- Exact upstream model identity is case-sensitive and supports 255 bytes of ID text.
ALTER TABLE usage_semantic_source_blocks MODIFY COLUMN upstream_model_id VARCHAR(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL;
