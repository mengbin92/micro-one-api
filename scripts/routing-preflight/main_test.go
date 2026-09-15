package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPreflightDoesNotInitializeMigrationMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.db")
	db, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	_, err = db.Exec("CREATE TABLE sentinel (id INTEGER)")
	require.NoError(t, err)
	db.Close()
	dsn, err := sqliteDSN(path)
	require.NoError(t, err)
	db, err = sql.Open("sqlite3", dsn)
	require.NoError(t, err)
	defer db.Close()
	r := &report{Stage: "f", Counts: map[string]int64{}}
	databaseChecks(context.Background(), r, db, "channel", "sqlite3")
	require.False(t, r.Checks[0].OK)
	var n int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name='schema_migrations'").Scan(&n))
	require.Zero(t, n)
	_, err = db.Exec("INSERT INTO sentinel VALUES (1)")
	require.Error(t, err, "probe connection must be read-only")
}

func TestPreflightBackfillAndExistingObjects(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.db")
	db, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	defer db.Close()
	for _, query := range []string{
		"CREATE TABLE schema_migrations (version TEXT)",
		"CREATE TABLE users (default_routing_group_id INTEGER, routing_access_revision INTEGER)",
		"CREATE TABLE tokens (routing_mode TEXT)",
		"INSERT INTO users VALUES (NULL, 0)",
		"INSERT INTO tokens VALUES ('fixed'), ('ordered')",
	} {
		_, err = db.Exec(query)
		require.NoError(t, err)
	}
	for _, versions := range migrations["identity"] {
		for _, v := range versions {
			_, err = db.Exec("INSERT INTO schema_migrations VALUES (?)", v)
			require.NoError(t, err)
		}
	}
	r := &report{Stage: "f", Counts: map[string]int64{}}
	databaseChecks(context.Background(), r, db, "identity", "sqlite3")
	require.EqualValues(t, 1, r.Counts["identity.unmapped_users"])
	require.EqualValues(t, 1, r.Counts["identity.fixed_keys"])
	require.EqualValues(t, 1, r.Counts["identity.ordered_keys"])
	found := false
	for _, c := range r.Checks {
		if c.Name == "identity.backfill" {
			found = true
			require.False(t, c.OK)
		}
	}
	require.True(t, found)
	_, err = db.Exec("UPDATE users SET default_routing_group_id=1,routing_access_revision=1")
	require.NoError(t, err)
	r = &report{Stage: "f", Counts: map[string]int64{}}
	databaseChecks(context.Background(), r, db, "identity", "sqlite3")
	for _, c := range r.Checks {
		require.True(t, c.OK, c.Name)
	}
}

func TestSQLiteProbeRejectsWritingDSNs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.db")
	require.NoError(t, os.WriteFile(path, nil, 0600))
	for _, dsn := range []string{path + "?mode=rw", path + "?_journal_mode=WAL", ":memory:", path + "-missing"} {
		_, err := sqliteDSN(dsn)
		require.Error(t, err)
	}
	_, err := sqliteDSN(path)
	require.NoError(t, err)
}

func TestConfigMismatchAndMissingGatesFailClosed(t *testing.T) {
	d := deployment{}
	d.Services = make(map[string]struct {
		Environment map[string]string `json:"environment"`
	})
	d.Services["admin-api"] = struct {
		Environment map[string]string `json:"environment"`
	}{map[string]string{"SUBSCRIPTION_ENTITLEMENTS_V2": "true"}}
	r := &report{Stage: "f"}
	configChecks(r, d)
	for _, c := range r.Checks {
		if c.Name == "entitlements.consistent" {
			require.False(t, c.OK)
		}
	}
	failures := 0
	for _, c := range r.Checks {
		if !c.OK {
			failures++
		}
	}
	require.GreaterOrEqual(t, failures, 8)
}
