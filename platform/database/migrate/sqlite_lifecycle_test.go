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
	"strings"
	"testing"
	"time"

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
	require.Contains(t, tables, "model_health_states",
		"091_create_model_health_states mirror must have applied")
	require.True(t, sqliteColumnExists(t, db, "model_health_states", "total_latency_ms"),
		"091 model health snapshots must retain total latency for exact averages")
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
	require.Len(t, files, 32, "sqlite tree has a known migration count; bump this test when adding mirrors")

	cut := sort.SearchStrings(files, "084_add_model_pricing_cache_read.sql") // preserve the pre-price-normalization upgrade boundary

	db := openScratchSqlite(t)
	// Stage 1: apply the tree up to (not including) 084 and later.
	stage1 := tempDirWithFiles(t, files[:cut], dir)
	r1 := NewWithDriver(db, stage1, "sqlite3")
	applied1, err := r1.Apply(context.Background())
	require.NoError(t, err)
	require.Len(t, applied1, cut, "stage-1 fresh install applies %d migrations", cut)
	_, err = db.Exec(`
		INSERT INTO models (model_id, display_name, pricing_input, pricing_output)
		VALUES ('migration-price-model', 'Migration Price Model', 0.005, 0.015)
	`)
	require.NoError(t, err)

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
	require.True(t, sqliteColumnExists(t, db, "models", "pricing_cache_read"),
		"model cache-read price migration must have applied during upgrade")
	require.True(t, sqliteColumnExists(t, db, "billing_ledgers", "pricing_config_hash"),
		"pricing snapshot hash migration must have applied during upgrade")
	require.True(t, sqliteTableExists(t, db, "billing_pricing_snapshots"),
		"pricing snapshot table migration must have applied during upgrade")
	require.True(t, sqliteColumnExists(t, db, "model_health_states", "total_latency_ms"),
		"model health migration must have applied during upgrade")
	var inputPrice, outputPrice, cacheReadPrice float64
	err = db.QueryRow(`
		SELECT pricing_input, pricing_output, pricing_cache_read
		FROM models WHERE model_id = ?
	`, "migration-price-model").Scan(&inputPrice, &outputPrice, &cacheReadPrice)
	require.NoError(t, err)
	require.InDelta(t, 5, inputPrice, 1e-12, "input price must migrate from per-1K to per-1M")
	require.InDelta(t, 15, outputPrice, 1e-12, "output price must migrate from per-1K to per-1M")
	require.Zero(t, cacheReadPrice, "existing models receive the cache-read default")
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

// A deployed baseline is already recorded as applied, so changing only 000
// cannot repair timestamp scanning. Upgrade populated INTEGER columns, retain
// GORM timestamp strings verbatim, and preserve amounts, indexes and sequences.
func TestSQLiteDialect_TimestampUpgradePreservesLedger(t *testing.T) {
	dir := sqliteDialectDir(t)
	stage := t.TempDir()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".sql") || strings.HasPrefix(entry.Name(), "090_") || strings.HasPrefix(entry.Name(), "011_") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		require.NoError(t, err)
		if strings.HasPrefix(entry.Name(), "000_") {
			// Restore the previous INTEGER declarations for the affected tables.
			text := string(body)
			for _, table := range []string{"billing_reservations", "billing_ledgers", "billing_redeem_codes", "billing_redeem_records", "payment_orders", "account_receivables"} {
				start := strings.Index(text, "CREATE TABLE IF NOT EXISTS "+table+" (")
				end := start + strings.Index(text[start:], "\n);")
				part := strings.ReplaceAll(text[start:end], "DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP", "INTEGER DEFAULT 0")
				part = strings.ReplaceAll(part, "DATETIME DEFAULT NULL", "INTEGER DEFAULT 0")
				text = text[:start] + part + text[end:]
			}
			body = []byte(text)
		}
		require.NoError(t, os.WriteFile(filepath.Join(stage, entry.Name()), body, 0600))
	}
	db := openScratchSqlite(t)
	_, err = NewWithDriver(db, stage, "sqlite3").Apply(context.Background())
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO billing_ledgers (id, user_id, amount, balance_after, type, ledger_dedupe_key, created_at)
        VALUES (1, '1', 17, 983, 'consume', 'old-integer', 1700000000),
               (2, '1', 23, 960, 'consume', 'old-string', '2026-09-09 01:02:03.123456789+00:00')`)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE sqlite_sequence SET seq = 999 WHERE name = 'billing_ledgers'`)
	require.NoError(t, err)
	var indexesBefore int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND tbl_name='billing_ledgers'`).Scan(&indexesBefore))
	applied, err := NewWithDriver(db, dir, "sqlite3").Apply(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"011_add_channel_oneapi_fields", "090_fix_billing_timestamp_types"}, applied)
	require.True(t, sqliteColumnExists(t, db, "channels", "model_mapping"))
	var created time.Time
	require.NoError(t, db.QueryRow(`SELECT created_at FROM billing_ledgers WHERE id=1`).Scan(&created))
	require.Equal(t, int64(1700000000), created.Unix())
	require.NoError(t, db.QueryRow(`SELECT created_at FROM billing_ledgers WHERE id=2`).Scan(&created))
	require.Equal(t, 123456789, created.Nanosecond())
	var total, balance, seq, indexesAfter int
	require.NoError(t, db.QueryRow(`SELECT sum(amount), sum(balance_after) FROM billing_ledgers`).Scan(&total, &balance))
	require.Equal(t, 40, total)
	require.Equal(t, 1943, balance)
	require.NoError(t, db.QueryRow(`SELECT seq FROM sqlite_sequence WHERE name='billing_ledgers'`).Scan(&seq))
	require.Equal(t, 999, seq)
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND tbl_name='billing_ledgers'`).Scan(&indexesAfter))
	require.Equal(t, indexesBefore, indexesAfter)
	again, err := NewWithDriver(db, dir, "sqlite3").Apply(context.Background())
	require.NoError(t, err)
	require.Empty(t, again)
}
