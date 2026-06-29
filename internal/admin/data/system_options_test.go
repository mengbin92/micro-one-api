package data

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openTestSQLite(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	dsn := "file:" + filepath.Join(dir, "sys.db")
	db, err := sql.Open("sqlite3", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`
		CREATE TABLE system_options (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			option_key TEXT NOT NULL UNIQUE,
			option_value TEXT NOT NULL,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	require.NoError(t, err)
	return db
}

func TestSystemOptionsRepo_SQLite3_RoundTrip(t *testing.T) {
	db := openTestSQLite(t)
	repo := NewSystemOptionsRepo(db)

	// Empty initially.
	v, err := repo.Get("site_title")
	require.NoError(t, err)
	assert.Equal(t, "", v, "empty table should return empty string")

	// Insert via Set.
	require.NoError(t, repo.Set("site_title", "Micro One API"))

	// Read back.
	v, err = repo.Get("site_title")
	require.NoError(t, err)
	assert.Equal(t, "Micro One API", v)

	// Upsert: Set again with a new value.
	require.NoError(t, repo.Set("site_title", "Updated"))
	v, err = repo.Get("site_title")
	require.NoError(t, err)
	assert.Equal(t, "Updated", v, "Set should upsert the existing row")
}

func TestSystemOptionsRepo_NewWithDriver_DriverFlag(t *testing.T) {
	// The pgBind flag drives which SQL is executed by Set. We can't run
	// against a real Postgres instance here, but we can confirm the
	// constructor flips the flag correctly for each supported driver.
	db := openTestSQLite(t)

	for _, c := range []struct {
		driver string
		want   bool
	}{
		{"postgres", true},
		{"postgresql", true},
		{"pgx", true},
		{"mysql", false},
		{"sqlite3", false},
		{"sqlite", false},
	} {
		repo := NewSystemOptionsRepoWithDriver(db, c.driver)
		assert.Equal(t, c.want, repo.pgBind,
			"driver %q: expected pgBind=%v, got %v", c.driver, c.want, repo.pgBind)
	}
}

func TestSystemOptionsRepo_DefaultIsSQLite3(t *testing.T) {
	// The no-arg NewSystemOptionsRepo helper must default to SQLite3
	// because the lite deployment is the dominant case for admin-api.
	// This guards against accidentally defaulting to MySQL, which
	// would fail with "near DUPLICATE" on SQLite.
	db := openTestSQLite(t)
	repo := NewSystemOptionsRepo(db)
	assert.Equal(t, "sqlite3", repo.driver)
	assert.False(t, repo.pgBind)
	require.NoError(t, repo.Set("k", "v"))
	got, err := repo.Get("k")
	require.NoError(t, err)
	assert.Equal(t, "v", got)
}
