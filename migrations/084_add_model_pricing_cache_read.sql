-- Registry prices switch from per-1K to per-1M tokens, matching the unit
-- shown in admin pricing management. Values stored before this migration were
-- entered per 1K tokens, so scale them up. pricing_cache_read is introduced
-- directly in per-1M semantics. Widen the columns so scaled values keep their
-- original range (DECIMAL(16,6) leaves ten integer digits after scaling).

ALTER TABLE models ADD COLUMN pricing_cache_read DECIMAL(16,6) NOT NULL DEFAULT 0 COMMENT 'cache-read price per 1M tokens';
ALTER TABLE models MODIFY COLUMN pricing_input DECIMAL(16,6) NOT NULL DEFAULT 0 COMMENT 'input price per 1M tokens';
ALTER TABLE models MODIFY COLUMN pricing_output DECIMAL(16,6) NOT NULL DEFAULT 0 COMMENT 'output price per 1M tokens';

UPDATE models
SET pricing_input = pricing_input * 1000,
    pricing_output = pricing_output * 1000
WHERE pricing_input <> 0 OR pricing_output <> 0;
