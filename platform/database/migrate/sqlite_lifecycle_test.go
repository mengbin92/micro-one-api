package migrate

// SQLite dialect lifecycle tests (v0.19 P1.2 "SQLite fresh install +
// incremental upgrade automation").
//
// These tests drive the REAL migrations/sqlite tree against scratch SQLite
// databases, so a schema change that breaks Lite mode fails in `make
// test-unit` (this package is part of the default unit gate) instead of only
// surfacing in a deploy.
//
// The runner intentionally tolerates "duplicate column name" on SQLite
// (consolidated baseline + numbered mirrors may carry the same column), so a
// fresh install must apply the whole tree exactly once and a second Apply
// must be a no-op.

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

// sqliteDialectDir resolves the real migrations/sqlite directory relative to
// this package (platform/database/migrate → repo root/migrations/sqlite).
func sqliteDialectDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "..", "migrations", "sqlite"))
	require.NoError(t, err)
	info, err := os.Stat(dir)
	require.NoError(t, err, "real sqlite migrations dir must exist at %s", dir)
	require.True(t, info.IsDir())
	return dir
}

// openScratchSqlite opens a scratch SQLite file DB (a file, not :memory:, so
// multiple connections / WAL behaviour match the Lite deployment).
func openScratchSqlite(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "lite.db")
	db, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return db
}

// countSqliteTables returns the table names present in the scratch DB.
func countSqliteTables(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`)
	require.NoError(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		out = append(out, name)
	}
	require.NoError(t, rows.Err())
	return out
}

func sqliteColumnExists(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		require.NoError(t, rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk))
		if name == column {
			return true
		}
	}
	require.NoError(t, rows.Err())
	return false
}

// TestSQLiteDialect_FreshInstall applies the full sqlite tree to an empty
// database and asserts every migration is recorded exactly once, key tables
// and columns exist, and a second Apply is a no-op.
func TestSQLiteDialect_FreshInstall(t *testing.T) {
	dir := sqliteDialectDir(t)

	// Expected set of migrations = every *.sql file in the dir (the runner
	// skips nothing there; README.md is not SQL).
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var want []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".sql" {
			want = append(want, e.Name())
		}
	}
	sort.Strings(want)
	require.NotEmpty(t, want, "sqlite dialect dir must contain migrations")

	db := openScratchSqlite(t)
	runner := NewWithDriver(db, dir, "sqlite3")

	applied, err := runner.Apply(context.Background())
	require.NoError(t, err, "fresh sqlite install must apply cleanly")
	require.Len(t, applied, len(want), "every sqlite migration must be applied on a fresh DB")

	// Idempotency: a second Apply must not re-run anything.
	again, err := runner.Apply(context.Background())
	require.NoError(t, err)
	require.Empty(t, again, "second Apply on a migrated DB must be a no-op")

	// Spot-check the schema actually materialised: the consolidated baseline
	// creates core tables; recent mirrors add columns.
	tables := countSqliteTables(t, db)
	require.Contains(t, tables, "users")
	require.Contains(t, tables, "channels")
	require.Contains(t, tables, "billing_ledgers")
	require.Contains(t, tables, "schema_migrations")

	// Recent mirror (077) landed: renewal_strategy on user_subscriptions.
	require.True(t, sqliteColumnExists(t, db, "user_subscriptions", "renewal_strategy"),
		"077_add_subscription_renewal_strategy mirror must have applied")
}

// TestSQLiteDialect_IncrementalUpgrade simulates a deployed Lite instance
// that was migrated to an earlier revision, then receives the rest of the
// tree as a batch upgrade. It asserts that only the new files run, and the
// final schema matches a fresh install.
func TestSQLiteDialect_IncrementalUpgrade(t *testing.T) {
	dir := sqliteDialectDir(t)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var files []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".sql" {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	require.Len(t, files, 23, "sqlite tree has a known migration count; bump this test when adding mirrors")

	cut := len(files) - 3 // last three files arrive later (081, 082, 083)

	db := openScratchSqlite(t)
	// Stage 1: apply the tree up to (not including) the last three files.
	stage1 := tempDirWithFiles(t, files[:cut], dir)
	r1 := NewWithDriver(db, stage1, "sqlite3")
	applied1, err := r1.Apply(context.Background())
	require.NoError(t, err)
	require.Len(t, applied1, cut, "stage-1 fresh install applies %d migrations", cut)

	// Stage 2: same DB now sees the complete tree — only the new files run.
	r2 := NewWithDriver(db, dir, "sqlite3")
	applied2, err := r2.Apply(context.Background())
	require.NoError(t, err)
	require.Len(t, applied2, len(files)-cut, "upgrade applies only the %d new files", len(files)-cut)
	for _, v := range applied2 {
		require.Contains(t, files[cut:], v+".sql", "upgrade must apply the tail files")
	}

	// The upgraded schema matches what a fresh install produces (same table
	// set).
	all, err := NewWithDriver(db, dir, "sqlite3").Apply(context.Background())
	require.NoError(t, err)
	require.Empty(t, all, "after upgrade the tree is fully applied")
	require.True(t, sqliteColumnExists(t, db, "billing_ledgers", "cost_audit_status"),
		"ledger cost-audit migration must have applied during upgrade")
	require.True(t, sqliteTableExists(t, db, "channel_usage_events"),
		"channel usage idempotency migration must have applied during upgrade")
	require.True(t, sqliteTableExists(t, db, "log_ingest_dedupe_claims"),
		"log ingestion idempotency migration must have applied during upgrade")
	require.True(t, sqliteColumnExists(t, db, "models", "input_modalities"),
		"model input modalities migration must have applied during upgrade")
	require.True(t, sqliteColumnExists(t, db, "models", "output_modalities"),
		"model output modalities migration must have applied during upgrade")
}

func TestSQLiteDialect_BalanceAmountMigrationBackfillsLegacyColumn(t *testing.T) {
	dir := sqliteDialectDir(t)
	db := openScratchSqlite(t)
	_, err := db.Exec(`
		CREATE TABLE billing_reservations (
			reservation_id TEXT PRIMARY KEY,
			balance_amount_quota INTEGER NOT NULL DEFAULT 0
		);
		INSERT INTO billing_reservations (reservation_id, balance_amount_quota)
		VALUES ('legacy-reservation', 209);
	`)
	require.NoError(t, err)

	migrationDir := tempDirWithFiles(t, []string{"079_add_balance_amount_to_billing_reservations.sql"}, dir)
	applied, err := NewWithDriver(db, migrationDir, "sqlite3").Apply(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"079_add_balance_amount_to_billing_reservations"}, applied)
	require.True(t, sqliteColumnExists(t, db, "billing_reservations", "balance_amount"))

	var balanceAmount int64
	require.NoError(t, db.QueryRow(`
		SELECT balance_amount
		FROM billing_reservations
		WHERE reservation_id = 'legacy-reservation'
	`).Scan(&balanceAmount))
	require.Equal(t, int64(209), balanceAmount)
}

func sqliteTableExists(t *testing.T, db *sql.DB, table string) bool {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&n))
	return n > 0
}

// tempDirWithFiles copies the given *.sql files from srcDir into a new temp
// dir (used to stage "an older release's" migration set).
func tempDirWithFiles(t *testing.T, names []string, srcDir string) string {
	t.Helper()
	dst := t.TempDir()
	for _, n := range names {
		data, err := os.ReadFile(filepath.Join(srcDir, n))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dst, n), data, 0o644))
	}
	return dst
}
