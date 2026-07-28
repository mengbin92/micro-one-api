-- v0.11.0 Phase 2 §2.1: canonical model ID governance.
--
-- The public models.model_id MUST be unique after NormalizeModelID
-- (TRIM + LOWER). The legacy uk_model_id is case-sensitive (MySQL utf8mb4_bin
-- / Postgres COLLATE "C" / SQLite COLLATE BINARY), so it let case-only
-- duplicates (e.g. "GLM-5.2" and "glm-5.2") through. This migration:
--
--   1. Normalises every existing row's model_id to its canonical spelling.
--   2. Adds a case-insensitive functional unique index so the DB rejects any
--      future case-only duplicate at insert time.
--
-- Step 1 is only safe when no two rows normalise to the same id. If the
-- registry still contains case-only duplicates, the UPDATE would collide on
-- uk_model_id and the migration aborts with a clear error — the operator must
-- first run the read-only preflight (CanonicalModelPreflight RPC /
-- /api/admin/models/canonical/preflight) and the transactional merge
-- (MergeCanonicalModels) to collapse duplicates onto a survivor. See
-- docs/design/v0.11.0-roadmap.md §2.1 and §7.1 ("完成 canonical model 数据合并
-- 后再启用数据库唯一约束").
--
-- No INSERT IGNORE / ON DUPLICATE KEY UPDATE: a real collision is a data
-- problem the operator must resolve, never silently overwritten.
-- Rollback = DROP INDEX (no destructive down migration).

-- ── Step 1: normalise stored model_id to LOWER(TRIM(model_id)). ───────────
-- Uses a guarded UPDATE so a collision surfaces as a duplicate-key error
-- rather than silently picking a winner. The CASE expression is a no-op when
-- the value is already canonical (idempotent re-runs are safe).
UPDATE `models`
   SET `model_id` = LOWER(TRIM(`model_id`))
 WHERE `model_id` <> LOWER(TRIM(`model_id`));

-- ── Step 2: case-insensitive canonical unique index. ─────────────────────
-- MySQL 8 supports functional indexes (LOWER() expression). The generated
-- column alternative is avoided so the application never has to populate a
-- second column. If step 1 left any case-only duplicate behind (because the
-- operator ran this migration before the merge), CREATE UNIQUE INDEX fails
-- here with a clear duplicate-key error and the whole migration rolls back.
CREATE UNIQUE INDEX `uk_models_canonical_id`
  ON `models` ((LOWER(TRIM(`model_id`))));
