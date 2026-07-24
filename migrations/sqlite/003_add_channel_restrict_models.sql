-- Add restrict_models flag to channels (SQLite).
-- Mirrors the MySQL migration 064. See docs/model-management-design.md §9.3 #2.
ALTER TABLE channels
  ADD COLUMN restrict_models INTEGER NOT NULL DEFAULT 1;
