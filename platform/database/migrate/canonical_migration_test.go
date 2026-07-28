package migrate

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// canonicalModelSchema is the minimal models-table shape the v0.11.0 Phase 2
// canonical-id migration operates on, mirroring migration 062 + the SQLite
// baseline (model_id TEXT NOT NULL UNIQUE COLLATE BINARY).
const canonicalModelSchema = `
CREATE TABLE models (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  model_id TEXT NOT NULL UNIQUE COLLATE BINARY,
  display_name TEXT NOT NULL DEFAULT ''
);
`

// canonicalMigrationSQL mirrors migrations/sqlite/006_add_canonical_model_id_constraint.sql.
const canonicalMigrationSQL = `
UPDATE models
   SET model_id = LOWER(TRIM(model_id))
 WHERE model_id <> LOWER(TRIM(model_id));

CREATE UNIQUE INDEX IF NOT EXISTS uk_models_canonical_id
    ON models (LOWER(TRIM(model_id)));
`

// TestCanonicalMigration_NormalisesExistingRows verifies that on a clean
// (no-duplicate) registry the migration lowercases stored model_ids and adds
// the canonical unique index. Idempotent re-runs must be a no-op.
func TestCanonicalMigration_NormalisesExistingRows(t *testing.T) {
	db := openSqlite(t)
	_, err := db.Exec(canonicalModelSchema)
	require.NoError(t, err)

	// Seed two non-canonical spellings that don't collide after normalisation.
	_, err = db.Exec(`INSERT INTO models (model_id) VALUES ('  GPT-4o  '), ('Claude-3 ')`)
	require.NoError(t, err)

	dir := t.TempDir()
	writeMigration(t, dir, "001_add_canonical_model_id_constraint.sql", canonicalMigrationSQL)

	runner := New(db, dir)
	applied, err := runner.Apply(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"001_add_canonical_model_id_constraint"}, applied)

	// Stored values are normalised.
	var ids []string
	rows, err := db.Query(`SELECT model_id FROM models ORDER BY model_id`)
	require.NoError(t, err)
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		ids = append(ids, id)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []string{"claude-3", "gpt-4o"}, ids)

	// Idempotent re-run: nothing to apply.
	applied2, err := runner.Apply(context.Background())
	require.NoError(t, err)
	assert.Empty(t, applied2)

	// A new case-only duplicate insert is now rejected by the canonical index.
	_, err = db.Exec(`INSERT INTO models (model_id) VALUES ('GPT-4O')`)
	assert.Error(t, err, "canonical unique index should reject case-only duplicate")
}

// TestCanonicalMigration_AbortsOnCaseOnlyDuplicate verifies that if the
// registry still contains case-only duplicates, the migration's UPDATE
// collides on the legacy uk_model_id and the whole migration rolls back
// (operator must run preflight + merge first). No silent winner-picking.
func TestCanonicalMigration_AbortsOnCaseOnlyDuplicate(t *testing.T) {
	db := openSqlite(t)
	_, err := db.Exec(canonicalModelSchema)
	require.NoError(t, err)

	// Seed a case-only collision the legacy BINARY unique key allowed.
	_, err = db.Exec(`INSERT INTO models (model_id) VALUES ('GLM-5.2'), ('glm-5.2')`)
	require.NoError(t, err)

	dir := t.TempDir()
	writeMigration(t, dir, "001_add_canonical_model_id_constraint.sql", canonicalMigrationSQL)

	runner := New(db, dir)
	_, err = runner.Apply(context.Background())
	require.Error(t, err, "migration must abort when case-only duplicates exist")

	// Migration rolled back: not recorded as applied.
	assert.Empty(t, appliedVersions(t, db))

	// Original rows untouched (no silent overwrite).
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM models WHERE model_id IN ('GLM-5.2','glm-5.2')`).Scan(&count))
	assert.Equal(t, 2, count, "both case variants must survive the rollback")
}

// TestCanonicalMigration_PassesAfterMerge verifies the documented flow: after
// the operator merges duplicates (simulated here by deleting the loser), the
// migration applies cleanly and enforces the canonical constraint.
func TestCanonicalMigration_PassesAfterMerge(t *testing.T) {
	db := openSqlite(t)
	_, err := db.Exec(canonicalModelSchema)
	require.NoError(t, err)

	_, err = db.Exec(`INSERT INTO models (model_id) VALUES ('GLM-5.2'), ('glm-5.2')`)
	require.NoError(t, err)

	// Simulate the operator's merge: delete the loser, normalise the survivor.
	_, err = db.Exec(`DELETE FROM models WHERE model_id = 'GLM-5.2'`)
	require.NoError(t, err)

	dir := t.TempDir()
	writeMigration(t, dir, "001_add_canonical_model_id_constraint.sql", canonicalMigrationSQL)

	runner := New(db, dir)
	applied, err := runner.Apply(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"001_add_canonical_model_id_constraint"}, applied)

	// Survivor is normalised and a future case variant is rejected.
	var id string
	require.NoError(t, db.QueryRow(`SELECT model_id FROM models`).Scan(&id))
	assert.Equal(t, "glm-5.2", id)

	_, err = db.Exec(`INSERT INTO models (model_id) VALUES ('GLM-5.2')`)
	require.Error(t, err, "post-migration canonical index should reject case-only duplicate")
}
