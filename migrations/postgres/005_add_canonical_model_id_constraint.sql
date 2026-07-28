-- v0.11.0 Phase 2 §2.1: canonical model ID governance (Postgres flavour).
-- Sibling of migrations/068_add_canonical_model_id_constraint.sql.
-- See that file for the full rationale.
--
--   1. Normalise existing model_id rows to LOWER(TRIM(model_id)).
--   2. Add a case-insensitive expression unique index so the DB rejects any
--      future case-only duplicate at insert time.
--
-- If the registry still contains case-only duplicates, step 1 raises a
-- duplicate-key error and the whole migration rolls back. The operator must
-- first run the read-only preflight (CanonicalModelPreflight) and the
-- transactional merge (MergeCanonicalModels) to collapse duplicates onto a
-- survivor. No ON CONFLICT DO NOTHING: a real collision is a data problem the
-- operator must resolve.
-- Rollback = DROP INDEX (no destructive down migration).

UPDATE models
   SET model_id = LOWER(BTRIM(model_id))
 WHERE model_id <> LOWER(BTRIM(model_id));

CREATE UNIQUE INDEX IF NOT EXISTS uk_models_canonical_id
    ON models (LOWER(BTRIM(model_id)));
