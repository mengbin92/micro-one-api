package main

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/pkg/jsonx"
)

// The fixture deliberately has only routing columns, verifying that the audit
// can run on a snapshot from which credential columns have been removed.
func auditFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "snapshot.db")
	db, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	defer db.Close()
	statements := []string{
		"CREATE TABLE users (id INTEGER PRIMARY KEY, `group` TEXT)",
		"CREATE TABLE channels (id INTEGER PRIMARY KEY, status INTEGER, `group` TEXT, models TEXT, restrict_models BOOLEAN, priority INTEGER)",
		"CREATE TABLE subscription_accounts (id INTEGER PRIMARY KEY, status INTEGER, `group` TEXT, models TEXT, platform TEXT, priority INTEGER)",
		"CREATE TABLE abilities (`group` TEXT, model TEXT, channel_id INTEGER, enabled BOOLEAN, priority INTEGER)",
		"CREATE TABLE subscription_account_abilities (id INTEGER PRIMARY KEY, `group` TEXT, model TEXT, account_id INTEGER, platform TEXT, enabled BOOLEAN, priority INTEGER)",
		"CREATE TABLE models (id INTEGER PRIMARY KEY, model_id TEXT, status INTEGER, is_public BOOLEAN)",
		"CREATE TABLE model_channel_mapping (id INTEGER PRIMARY KEY, model_id INTEGER, channel_id INTEGER, enabled BOOLEAN, upstream_model_id TEXT, priority INTEGER)",
		"CREATE TABLE model_subscription_mapping (id INTEGER PRIMARY KEY, model_id INTEGER, subscription_account_id INTEGER, group_name TEXT, enabled BOOLEAN, upstream_model_id TEXT, priority INTEGER)",
		"CREATE TABLE model_routings (id INTEGER PRIMARY KEY, group_name TEXT, model TEXT, subscription_account_id INTEGER, platform TEXT, enabled BOOLEAN, priority INTEGER)",
		"CREATE TABLE system_options (option_key TEXT, option_value TEXT)",
		"INSERT INTO users VALUES (1, 'vip'), (2, 'VIP')",
		"INSERT INTO channels VALUES (1, 1, 'default', 'legacy-*', 1, 0)",
		"INSERT INTO abilities VALUES ('default', 'legacy-*', 1, 1, 0)",
		"INSERT INTO subscription_accounts VALUES (1, 1, 'default, default', 'managed', 'codex', 0)",
		"INSERT INTO models VALUES (1, 'managed', 1, 1)",
		"INSERT INTO model_subscription_mapping VALUES (1, 1, 1, 'vip', 1, 'upstream-managed', 0)",
		`INSERT INTO system_options VALUES ('GroupRatio', '{"vip":2,"orphan":3}')`,
	}
	for _, statement := range statements {
		_, err := db.Exec(statement)
		require.NoError(t, err)
	}
	return path
}

func TestAuditReadOnlyDeterministicAndModelScoped(t *testing.T) {
	path := auditFixture(t)
	t.Setenv("GROUP_AUDIT_DSN", path)
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	var output, diagnostic bytes.Buffer
	args := []string{"--driver=sqlite3", "--model=legacy-chat"}
	require.Equal(t, 1, run(args, &output, &diagnostic), diagnostic.String())
	var report biz.GroupAuditReport
	require.NoError(t, jsonx.Unmarshal(output.Bytes(), &report))
	require.Equal(t, 1, report.Version)
	require.Len(t, report.Grants, 2)
	require.Equal(t, "default", report.Grants[0].Group)
	require.Equal(t, "legacy-chat", report.Grants[0].Model)
	require.Equal(t, "channel", report.Grants[0].Source.Kind)
	require.Equal(t, "vip", report.Grants[1].Group)
	require.Equal(t, "managed", report.Grants[1].Model)
	require.Equal(t, "subscription", report.Grants[1].Source.Kind)
	require.Equal(t, "upstream-managed", report.Grants[1].UpstreamModelID)
	codes := map[string]bool{}
	for _, issue := range report.Issues {
		codes[issue.Code] = true
	}
	for _, code := range []string{"duplicate_membership", "group_whitespace", "case_or_whitespace_collision", "model_grant_outside_account_groups", "price_display_mismatch", "price_without_resource"} {
		require.True(t, codes[code], code)
	}
	var again bytes.Buffer
	require.Equal(t, 1, run(args, &again, &diagnostic))
	require.Equal(t, output.String(), again.String())
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after)
	dsn, err := readOnlySQLiteDSN(path)
	require.NoError(t, err)
	db, err := sql.Open("sqlite3", dsn)
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec("DELETE FROM users")
	require.Error(t, err, "connection must reject writes")
}

func TestAuditIncompleteProducesNoBaseline(t *testing.T) {
	t.Setenv("GROUP_AUDIT_DSN", auditFixture(t))
	for _, args := range [][]string{{"--driver=sqlite3", "--max-probes=1"}, {"--driver=postgres"}, {"--driver=sqlite3", "--identity-schema=identity"}} {
		var output, diagnostic bytes.Buffer
		require.Equal(t, 2, run(args, &output, &diagnostic))
		require.Empty(t, output.String())
	}
	path := filepath.Join(t.TempDir(), "empty.db")
	require.NoError(t, os.WriteFile(path, nil, 0600))
	t.Setenv("GROUP_AUDIT_DSN", path)
	var output, diagnostic bytes.Buffer
	require.Equal(t, 2, run([]string{"--driver=sqlite3"}, &output, &diagnostic))
	require.Empty(t, output.String())
	for _, invalid := range []string{":memory:", path + "?mode=rw", "file:" + path + "?_journal_mode=WAL"} {
		_, err := readOnlySQLiteDSN(invalid)
		require.Error(t, err)
	}
}

func TestAuditOutputDoesNotOverwriteBaseline(t *testing.T) {
	t.Setenv("GROUP_AUDIT_DSN", auditFixture(t))
	path := filepath.Join(t.TempDir(), "report.json")
	var output, diagnostic bytes.Buffer
	args := []string{"--driver=sqlite3", "--output=" + path}
	require.Equal(t, 1, run(args, &output, &diagnostic))
	require.Empty(t, output.String())
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, 2, run(args, &output, &diagnostic))
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after)
}
