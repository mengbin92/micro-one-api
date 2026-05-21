package migrate

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openSqlite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return db
}

// writeMigration drops a numbered .sql file in dir.
func writeMigration(t *testing.T, dir, name, sqlText string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(sqlText), 0o644))
}

func appliedVersions(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query("SELECT version FROM schema_migrations ORDER BY version")
	require.NoError(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		require.NoError(t, rows.Scan(&v))
		out = append(out, v)
	}
	require.NoError(t, rows.Err())
	return out
}

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var n int
	row := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name = ?", name)
	require.NoError(t, row.Scan(&n))
	return n > 0
}

func TestRunner_EmptyDB_AppliesAllInOrder(t *testing.T) {
	db := openSqlite(t)
	dir := t.TempDir()
	writeMigration(t, dir, "001_create_widgets.sql", `CREATE TABLE widgets (id INTEGER PRIMARY KEY, name TEXT);`)
	writeMigration(t, dir, "002_create_gadgets.sql", `CREATE TABLE gadgets (id INTEGER PRIMARY KEY);`)
	writeMigration(t, dir, "010_add_widget_color.sql", `ALTER TABLE widgets ADD COLUMN color TEXT;`)

	runner := New(db, dir)
	applied, err := runner.Apply(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"001_create_widgets", "002_create_gadgets", "010_add_widget_color"}, applied)

	assert.True(t, tableExists(t, db, "widgets"))
	assert.True(t, tableExists(t, db, "gadgets"))
	assert.Equal(t, []string{"001_create_widgets", "002_create_gadgets", "010_add_widget_color"}, appliedVersions(t, db))
}

func TestRunner_AlphabeticalNumericSort(t *testing.T) {
	// "100_xxx" must come after "020_xxx" — zero-padded ordering.
	db := openSqlite(t)
	dir := t.TempDir()
	writeMigration(t, dir, "020_a.sql", `CREATE TABLE first (id INTEGER);`)
	writeMigration(t, dir, "100_b.sql", `CREATE TABLE second (id INTEGER);`)

	runner := New(db, dir)
	applied, err := runner.Apply(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"020_a", "100_b"}, applied)
}

func TestRunner_Idempotent_SkipsAppliedMigrations(t *testing.T) {
	db := openSqlite(t)
	dir := t.TempDir()
	writeMigration(t, dir, "001_first.sql", `CREATE TABLE first_run (id INTEGER);`)

	runner := New(db, dir)
	applied1, err := runner.Apply(context.Background())
	require.NoError(t, err)
	require.Len(t, applied1, 1)

	// Second run on the same DB: no new migrations to apply.
	applied2, err := runner.Apply(context.Background())
	require.NoError(t, err)
	assert.Empty(t, applied2, "second run should apply nothing")
}

func TestRunner_Incremental_OnlyAppliesNew(t *testing.T) {
	db := openSqlite(t)
	dir := t.TempDir()
	writeMigration(t, dir, "001_a.sql", `CREATE TABLE a (id INTEGER);`)

	runner := New(db, dir)
	_, err := runner.Apply(context.Background())
	require.NoError(t, err)

	// Drop a new migration after baseline.
	writeMigration(t, dir, "002_b.sql", `CREATE TABLE b (id INTEGER);`)

	applied, err := runner.Apply(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"002_b"}, applied)
	assert.True(t, tableExists(t, db, "a"))
	assert.True(t, tableExists(t, db, "b"))
}

func TestRunner_BrownfieldBaseline_SkipsExistingMigrations(t *testing.T) {
	// Simulate a database where someone already ran docker-entrypoint-initdb.d:
	// `users` already exists, but `schema_migrations` does NOT.
	// runner should detect this and mark all pre-existing migration files as
	// applied without executing them.
	db := openSqlite(t)
	require.NoError(t, runExec(db, `CREATE TABLE users (id INTEGER PRIMARY KEY)`))

	dir := t.TempDir()
	// These were "already applied" by docker-entrypoint. If runner naively
	// re-runs them we'd get "table users already exists" errors.
	writeMigration(t, dir, "000_create_users.sql", `CREATE TABLE users (id INTEGER PRIMARY KEY);`)
	writeMigration(t, dir, "010_add_users_email.sql", `ALTER TABLE users ADD COLUMN email TEXT;`)

	runner := New(db, dir).WithBaseline("010_add_users_email")
	applied, err := runner.Apply(context.Background())
	require.NoError(t, err)

	assert.Empty(t, applied, "brownfield baseline should not actually execute existing migrations")
	assert.ElementsMatch(t, []string{"000_create_users", "010_add_users_email"}, appliedVersions(t, db),
		"existing migrations should be marked as applied without execution")
}

func TestRunner_BrownfieldBaseline_AppliesNewer(t *testing.T) {
	// Same brownfield setup, but a brand-new migration appears beyond the baseline.
	// runner should baseline old files AND apply the new one.
	db := openSqlite(t)
	require.NoError(t, runExec(db, `CREATE TABLE users (id INTEGER PRIMARY KEY)`))

	dir := t.TempDir()
	writeMigration(t, dir, "000_create_users.sql", `CREATE TABLE users (id INTEGER PRIMARY KEY);`)
	writeMigration(t, dir, "023_brand_new.sql", `CREATE TABLE freshly_added (id INTEGER PRIMARY KEY);`)

	runner := New(db, dir).WithBaseline("022_create_schema_migrations")
	applied, err := runner.Apply(context.Background())
	require.NoError(t, err)

	// Baseline marks 000_create_users (≤ 022); 023_brand_new is genuinely applied.
	assert.Equal(t, []string{"023_brand_new"}, applied)
	assert.True(t, tableExists(t, db, "freshly_added"))
	assert.ElementsMatch(t, []string{"000_create_users", "023_brand_new"}, appliedVersions(t, db))
}

func TestRunner_BrownfieldDetected_NoBaselineSet_Errors(t *testing.T) {
	// Brownfield detected but BaselineVersion not provided: the caller forgot
	// to set it, and we MUST NOT silently re-run migrations against an existing
	// schema. Fail loudly so the operator can decide.
	db := openSqlite(t)
	require.NoError(t, runExec(db, `CREATE TABLE users (id INTEGER PRIMARY KEY)`))

	dir := t.TempDir()
	writeMigration(t, dir, "000_create_users.sql", `CREATE TABLE users (id INTEGER PRIMARY KEY);`)

	runner := New(db, dir) // BaselineVersion intentionally not set
	_, err := runner.Apply(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "brownfield",
		"error message must hint at the brownfield situation")
}

func TestRunner_NoBrownfield_NoBaselineRequired(t *testing.T) {
	// Empty DB + no BaselineVersion = normal greenfield install; should
	// apply all migrations without complaint.
	db := openSqlite(t)
	dir := t.TempDir()
	writeMigration(t, dir, "001_a.sql", `CREATE TABLE a (id INTEGER);`)

	runner := New(db, dir)
	applied, err := runner.Apply(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"001_a"}, applied)
}

func TestRunner_FailedMigration_RollsBack(t *testing.T) {
	db := openSqlite(t)
	dir := t.TempDir()
	writeMigration(t, dir, "001_ok.sql", `CREATE TABLE ok (id INTEGER);`)
	writeMigration(t, dir, "002_bad.sql", `THIS IS NOT VALID SQL;`)

	runner := New(db, dir)
	applied, err := runner.Apply(context.Background())
	require.Error(t, err, "expected migration to surface error")
	// 001 should have succeeded and been recorded.
	assert.Equal(t, []string{"001_ok"}, applied)
	assert.Equal(t, []string{"001_ok"}, appliedVersions(t, db),
		"002_bad must NOT be in schema_migrations")
	assert.False(t, tableExists(t, db, "bad"), "no partial state from 002")
}

func TestRunner_Status_ReturnsAppliedAndPending(t *testing.T) {
	db := openSqlite(t)
	dir := t.TempDir()
	writeMigration(t, dir, "001_done.sql", `CREATE TABLE done (id INTEGER);`)
	writeMigration(t, dir, "002_pending.sql", `CREATE TABLE pending (id INTEGER);`)

	runner := New(db, dir)
	_, err := runner.Apply(context.Background())
	require.NoError(t, err)

	// Drop a 3rd file that isn't applied yet.
	writeMigration(t, dir, "003_future.sql", `CREATE TABLE future (id INTEGER);`)

	statuses, err := runner.Status(context.Background())
	require.NoError(t, err)

	// Expect 3 statuses, 2 applied, 1 pending. Order = filename order.
	require.Len(t, statuses, 3)
	assert.Equal(t, "001_done", statuses[0].Version)
	assert.True(t, statuses[0].Applied)
	assert.Equal(t, "002_pending", statuses[1].Version)
	assert.True(t, statuses[1].Applied)
	assert.Equal(t, "003_future", statuses[2].Version)
	assert.False(t, statuses[2].Applied)
}

func TestRunner_HandlesMultipleStatementsPerFile(t *testing.T) {
	// Real migrations contain multiple statements per file.
	db := openSqlite(t)
	dir := t.TempDir()
	writeMigration(t, dir, "001_multi.sql", `
		CREATE TABLE one (id INTEGER);
		CREATE TABLE two (id INTEGER);
	`)

	runner := New(db, dir)
	_, err := runner.Apply(context.Background())
	require.NoError(t, err)

	assert.True(t, tableExists(t, db, "one"))
	assert.True(t, tableExists(t, db, "two"))
}

func TestRunner_IgnoresNonSqlFiles(t *testing.T) {
	db := openSqlite(t)
	dir := t.TempDir()
	writeMigration(t, dir, "001_ok.sql", `CREATE TABLE ok (id INTEGER);`)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".DS_Store"), []byte("x"), 0o644))

	runner := New(db, dir)
	applied, err := runner.Apply(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"001_ok"}, applied)
}

// runExec is a tiny helper that just runs a statement and fails the test on
// error; saves on repetitive code in setup blocks.
func runExec(db *sql.DB, query string, args ...any) error {
	_, err := db.Exec(query, args...)
	if err != nil {
		return err
	}
	// touch strings so the import-checker is happy if we ever drop strings.
	_ = strings.TrimSpace
	return nil
}
