-- Registry prices switch from per-1K to per-1M tokens, matching the unit
-- shown in admin pricing management. Values stored before this migration were
-- entered per 1K tokens, so scale them up. pricing_cache_read is introduced
-- directly in per-1M semantics. SQLite REAL columns need no type change.

ALTER TABLE models ADD COLUMN pricing_cache_read REAL NOT NULL DEFAULT 0;

UPDATE models
SET pricing_input = pricing_input * 1000,
    pricing_output = pricing_output * 1000
WHERE pricing_input <> 0 OR pricing_output <> 0;
