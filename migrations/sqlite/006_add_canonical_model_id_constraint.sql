-- v0.11.0 Phase 2 §2.1: canonical model ID governance (SQLite flavour).
-- Sibling of migrations/068_add_canonical_model_id_constraint.sql.
-- See that file for the full rationale.
--
-- SQLite supports expression indexes, so LOWER(TRIM(model_id)) can be indexed
-- directly. SQLite's default UNIQUE constraint on model_id is COLLATE BINARY
-- (case-sensitive), which is what let "GLM-5.2" and "glm-5.2" coexist.
--
--   1. Normalise existing model_id rows to LOWER(TRIM(model_id)).
--   2. Add a case-insensitive expression unique index.
--
-- If the registry still contains case-only duplicates, step 1 raises a
-- UNIQUE constraint failed error (on the legacy uk_model_id) and the whole
-- migration rolls back. The operator must first run the read-only preflight
-- (CanonicalModelPreflight) and the transactional merge (MergeCanonicalModels)
-- to collapse duplicates onto a survivor. No INSERT OR IGNORE.
-- Rollback = DROP INDEX (no destructive down migration).

UPDATE models
   SET model_id = LOWER(TRIM(model_id))
 WHERE model_id <> LOWER(TRIM(model_id));

CREATE UNIQUE INDEX IF NOT EXISTS uk_models_canonical_id
    ON models (LOWER(TRIM(model_id)));
