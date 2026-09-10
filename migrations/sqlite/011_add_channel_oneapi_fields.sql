-- Add One API compatible channel fields missing from the consolidated
-- baseline (SQLite). Mirrors the channels part of MySQL migration
-- 018_add_channel_oneapi_fields.sql, which predates the 072 auto-mirror
-- boundary and should have been folded into 000_create_full_schema.sql.
-- Existing Lite databases created from the old baseline also lack these
-- columns, so this is an increment rather than a baseline edit.
ALTER TABLE channels
  ADD COLUMN model_mapping TEXT NOT NULL DEFAULT '';

ALTER TABLE channels
  ADD COLUMN system_prompt TEXT DEFAULT NULL;
