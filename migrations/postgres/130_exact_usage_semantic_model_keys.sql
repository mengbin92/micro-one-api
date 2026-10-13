-- Postgres already compares these IDs by exact spelling.
ALTER TABLE usage_semantic_source_blocks ALTER COLUMN upstream_model_id TYPE VARCHAR(255);
