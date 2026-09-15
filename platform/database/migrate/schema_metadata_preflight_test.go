package migrate

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunner_StatusDoesNotCreateSchemaMigrations(t *testing.T) {
	db := openSqlite(t)
	dir := t.TempDir()
	writeMigration(t, dir, "001_pending.sql", `CREATE TABLE pending (id INTEGER);`)

	statuses, err := NewWithDriver(db, dir, "sqlite3").Status(context.Background())
	require.NoError(t, err)
	require.Len(t, statuses, 1)
	assert.Equal(t, "001_pending", statuses[0].Version)
	assert.False(t, statuses[0].Applied)
	assert.False(t, tableExists(t, db, "schema_migrations"), "status must not initialize migration metadata")
}

func TestRunner_MetadataPreflightBlocksBeforeBusinessDDL(t *testing.T) {
	db := openSqlite(t)
	_, err := db.Exec(`CREATE TABLE schema_migrations (
		version VARCHAR(255) NOT NULL PRIMARY KEY,
		applied_at INTEGER NOT NULL
	)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES ('001_recorded', 1)`)
	require.NoError(t, err)

	dir := t.TempDir()
	writeMigration(t, dir, "001_recorded.sql", `SELECT 1;`)
	writeMigration(t, dir, "002_create_widgets.sql", `CREATE TABLE widgets (id INTEGER PRIMARY KEY);`)

	_, err = NewWithDriver(db, dir, "sqlite3").Apply(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "preflight schema_migrations")
	assert.Contains(t, err.Error(), "applied_at must be nullable or have a default value")
	assert.False(t, tableExists(t, db, "widgets"), "incompatible metadata must block before business DDL")
	assert.Equal(t, []string{"001_recorded"}, appliedVersions(t, db), "probe must roll back")
}

func TestRunner_MetadataPreflightAllowsUpgradeAfterRepair(t *testing.T) {
	db := openSqlite(t)
	_, err := db.Exec(`CREATE TABLE schema_migrations (
		version VARCHAR(255) NOT NULL PRIMARY KEY,
		applied_at INTEGER NOT NULL
	)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES ('001_recorded', 1)`)
	require.NoError(t, err)

	replaceSQLiteSchemaMigrations(t, db, true)

	dir := t.TempDir()
	writeMigration(t, dir, "001_recorded.sql", `SELECT 1;`)
	writeMigration(t, dir, "002_create_widgets.sql", `CREATE TABLE widgets (id INTEGER PRIMARY KEY);`)

	runner := NewWithDriver(db, dir, "sqlite3")
	applied, err := runner.Apply(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"002_create_widgets"}, applied)
	assert.True(t, tableExists(t, db, "widgets"))

	again, err := runner.Apply(context.Background())
	require.NoError(t, err)
	assert.Empty(t, again)
}

func TestRunner_MetadataPreflightProtectsHalfCompletedRecovery(t *testing.T) {
	db := openSqlite(t)
	_, err := db.Exec(`CREATE TABLE schema_migrations (
		version VARCHAR(255) NOT NULL PRIMARY KEY,
		applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	require.NoError(t, err)
	dir := t.TempDir()
	writeMigration(t, dir, "001_recorded.sql", `SELECT 1;`)
	writeMigration(t, dir, "002_create_widgets.sql", `CREATE TABLE widgets (id INTEGER PRIMARY KEY);`)

	runner := NewWithDriver(db, dir, "sqlite3")
	_, err = runner.Apply(context.Background())
	require.NoError(t, err)

	// Simulate a MySQL-style implicit DDL commit followed by a failed metadata
	// insert: the object exists, but its version is absent.
	_, err = db.Exec(`DELETE FROM schema_migrations WHERE version = '002_create_widgets'`)
	require.NoError(t, err)
	replaceSQLiteSchemaMigrations(t, db, false)

	_, err = runner.Apply(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "preflight schema_migrations", "retry must fail before re-running DDL")
	assert.True(t, tableExists(t, db, "widgets"))

	// Recovery remains explicit: verify the object, repair metadata, record
	// the version, and only then retry. The runner never infers completion
	// merely from the table name.
	replaceSQLiteSchemaMigrations(t, db, true)
	_, err = db.Exec(`INSERT INTO schema_migrations (version) VALUES ('002_create_widgets')`)
	require.NoError(t, err)
	again, err := runner.Apply(context.Background())
	require.NoError(t, err)
	assert.Empty(t, again)
}

func replaceSQLiteSchemaMigrations(t *testing.T, db *sql.DB, withDefault bool) {
	t.Helper()
	defaultSpec := ""
	if withDefault {
		defaultSpec = " DEFAULT CURRENT_TIMESTAMP"
	}
	statements := []string{
		`CREATE TABLE schema_migrations_repaired (
			version VARCHAR(255) NOT NULL PRIMARY KEY,
			applied_at TIMESTAMP NOT NULL` + defaultSpec + `
		)`,
		`INSERT INTO schema_migrations_repaired (version, applied_at)
		 SELECT version, applied_at FROM schema_migrations`,
		`DROP TABLE schema_migrations`,
		`ALTER TABLE schema_migrations_repaired RENAME TO schema_migrations`,
	}
	for _, statement := range statements {
		_, err := db.Exec(statement)
		require.NoError(t, err)
	}
}

// TestMigrationMetadataPreflightIntegration is enabled by migration smoke for
// real MySQL and PostgreSQL services. It recreates the historical metadata
// shape (applied_at NOT NULL with no default), verifies the DDL-first failure
// mode is blocked, then exercises repair, upgrade, repeat, and explicit
// half-completed recovery.
func TestMigrationMetadataPreflightIntegration(t *testing.T) {
	if os.Getenv("MIGRATIONS_PREFLIGHT_INTEGRATION") != "1" {
		t.Skip("set MIGRATIONS_PREFLIGHT_INTEGRATION=1 with MIGRATIONS_DSN and MIGRATIONS_DRIVER to run")
	}

	driver := NormalizeDriverName(os.Getenv("MIGRATIONS_DRIVER"))
	if driver != "mysql" && driver != "postgres" {
		t.Fatalf("unsupported integration driver %q; want mysql or postgres", driver)
	}

	sqlDriver := "pgx"
	if driver == "mysql" {
		sqlDriver = "mysql"
	}
	db, err := sql.Open(sqlDriver, os.Getenv("MIGRATIONS_DSN"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, db.Ping())

	_, err = db.Exec(`DROP TABLE IF EXISTS schema_migrations`)
	require.NoError(t, err)
	_, err = db.Exec(`DROP TABLE IF EXISTS metadata_preflight_probe`)
	require.NoError(t, err)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "001_recorded.sql"), []byte(`SELECT 1;`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "002_create_metadata_preflight_probe.sql"), []byte(`CREATE TABLE metadata_preflight_probe (id INTEGER PRIMARY KEY);`), 0o600))

	ctx := context.Background()
	runner := NewWithDriver(db, dir, driver)
	statuses, err := runner.Status(ctx)
	require.NoError(t, err)
	require.Len(t, statuses, 2)
	assert.False(t, statuses[0].Applied)
	assert.False(t, statuses[1].Applied)
	metadataExists, err := runner.tableExists(ctx, "schema_migrations")
	require.NoError(t, err)
	assert.False(t, metadataExists, "status must not create metadata on MySQL or PostgreSQL")

	_, err = db.Exec(`CREATE TABLE schema_migrations (
		version VARCHAR(255) NOT NULL PRIMARY KEY,
		applied_at BIGINT NOT NULL
	)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES ('001_recorded', 1)`)
	require.NoError(t, err)

	_, err = runner.Apply(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "preflight schema_migrations")
	exists, err := runner.tableExists(ctx, "metadata_preflight_probe")
	require.NoError(t, err)
	assert.False(t, exists, "preflight must run before business DDL")

	setMetadataDefault(t, db, driver, true)
	applied, err := runner.Apply(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"002_create_metadata_preflight_probe"}, applied)
	exists, err = runner.tableExists(ctx, "metadata_preflight_probe")
	require.NoError(t, err)
	assert.True(t, exists)

	again, err := runner.Apply(ctx)
	require.NoError(t, err)
	assert.Empty(t, again)

	_, err = db.Exec(`DELETE FROM schema_migrations WHERE version = '002_create_metadata_preflight_probe'`)
	require.NoError(t, err)
	setMetadataDefault(t, db, driver, false)
	_, err = runner.Apply(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "preflight schema_migrations", "half-completed retry must not re-run DDL")
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM schema_migrations WHERE version = '002_create_metadata_preflight_probe'`).Scan(&count))
	assert.Zero(t, count, "the runner must not auto-record a half-completed migration")

	setMetadataDefault(t, db, driver, true)
	_, err = db.Exec(`INSERT INTO schema_migrations (version) VALUES ('002_create_metadata_preflight_probe')`)
	require.NoError(t, err)
	again, err = runner.Apply(ctx)
	require.NoError(t, err)
	assert.Empty(t, again)
}

func setMetadataDefault(t *testing.T, db *sql.DB, driver string, enabled bool) {
	t.Helper()
	var query string
	switch driver {
	case "mysql":
		if enabled {
			query = `ALTER TABLE schema_migrations MODIFY applied_at BIGINT NOT NULL DEFAULT (UNIX_TIMESTAMP())`
		} else {
			query = `ALTER TABLE schema_migrations MODIFY applied_at BIGINT NOT NULL`
		}
	case "postgres":
		if enabled {
			query = `ALTER TABLE schema_migrations ALTER COLUMN applied_at SET DEFAULT EXTRACT(EPOCH FROM NOW())::BIGINT`
		} else {
			query = `ALTER TABLE schema_migrations ALTER COLUMN applied_at DROP DEFAULT`
		}
	default:
		t.Fatalf("unsupported integration driver %q", driver)
	}
	_, err := db.Exec(query)
	require.NoError(t, err)
}
