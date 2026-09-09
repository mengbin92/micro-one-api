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

	for _, statement := range auditStatements() {
		_, err := db.Exec(statement)
		require.NoError(t, err)
	}
	return path
}

func auditStatements() []string {
	return []string{
		"CREATE TABLE users (id INTEGER PRIMARY KEY, `group` TEXT)",
		"CREATE TABLE tokens (id INTEGER PRIMARY KEY, user_id INTEGER)",
		"CREATE TABLE subscription_groups (id INTEGER PRIMARY KEY, status INTEGER, daily_limit_usd REAL, weekly_limit_usd REAL, monthly_limit_usd REAL, rate_multiplier REAL)",
		"CREATE TABLE subscription_plans (id INTEGER PRIMARY KEY, group_id INTEGER, for_sale BOOLEAN)",
		"CREATE TABLE user_subscriptions (id INTEGER PRIMARY KEY, user_id INTEGER, group_id INTEGER, status TEXT, starts_at BIGINT, expires_at BIGINT)",
		"CREATE TABLE payment_orders (id INTEGER PRIMARY KEY, user_id TEXT, asset_type TEXT, group_id INTEGER, plan_id INTEGER, subscription_id INTEGER, status TEXT, asset_issue_status TEXT, plan_snapshot TEXT)",
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
		"INSERT INTO tokens VALUES (1, 1)",
		"INSERT INTO subscription_groups VALUES (100, 1, 5, 20, 50, 2)",
		"INSERT INTO subscription_plans VALUES (101, 100, true)",
		"INSERT INTO user_subscriptions VALUES (102, 1, 100, 'active', 1700000000, 1900000000)",
		`INSERT INTO payment_orders VALUES (103, '1', 'subscription', 100, 101, 0, 'pending', 'pending', '{"plan_id":101,"group_id":100,"validity_days":30,"price_quota":100,"name":"PRIVATE_PLAN_NAME","metadata":"SECRET_SENTINEL"}')`,
		"INSERT INTO channels VALUES (1, 1, 'default', 'legacy-*', true, 0)",
		"INSERT INTO abilities VALUES ('default', 'legacy-*', 1, true, 0)",
		"INSERT INTO subscription_accounts VALUES (1, 1, 'default, default', 'managed', 'codex', 0)",
		"INSERT INTO models VALUES (1, 'managed', 1, true)",
		"INSERT INTO model_subscription_mapping VALUES (1, 1, 1, 'vip', true, 'upstream-managed', 0)",
		`INSERT INTO system_options VALUES ('GroupRatio', '{"vip":2,"orphan":3}')`,
	}
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
	require.Equal(t, 2, report.Version)
	require.Len(t, report.QuotaPolicies, 1)
	require.Len(t, report.Plans, 1)
	require.Len(t, report.Subscriptions, 1)
	require.Len(t, report.Orders, 1)
	require.Equal(t, int64(100), report.Orders[0].SnapshotQuotaPolicyID)
	require.Equal(t, "legacy_all_authorized", report.Migration.SubscriptionCoverage)
	require.False(t, report.Migration.SubscriptionGrantsAccess)
	require.False(t, report.Migration.ReadyForBackfill)
	require.NotContains(t, report.Groups, "100")
	require.NotContains(t, output.String(), "PRIVATE_PLAN_NAME")
	require.NotContains(t, output.String(), "SECRET_SENTINEL")
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
	for _, args := range [][]string{{"--driver=sqlite3", "--max-probes=1"}, {"--driver=unsupported"}, {"--driver=sqlite3", "--identity-schema=identity"}} {
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

func TestAuditInvalidContractIsReportedWithoutRawSnapshot(t *testing.T) {
	path := auditFixture(t)
	db, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	_, err = db.Exec("UPDATE payment_orders SET plan_snapshot = ?", `{"plan_id": "SECRET_INVALID_PAYLOAD"`)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	t.Setenv("GROUP_AUDIT_DSN", path)
	var output, diagnostic bytes.Buffer
	require.Equal(t, 1, run([]string{"--driver=sqlite3"}, &output, &diagnostic))
	require.NotContains(t, output.String(), "SECRET_INVALID_PAYLOAD")
	require.NotContains(t, diagnostic.String(), "SECRET_INVALID_PAYLOAD")
	var report biz.GroupAuditReport
	require.NoError(t, jsonx.Unmarshal(output.Bytes(), &report))
	require.Equal(t, "invalid", report.Orders[0].SnapshotState)
	require.False(t, report.Migration.ReadyForBackfill)
}

func TestAuditMissingSubscriptionTableProducesNoPartialBaseline(t *testing.T) {
	path := auditFixture(t)
	db, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	_, err = db.Exec("DROP TABLE subscription_plans")
	require.NoError(t, err)
	require.NoError(t, db.Close())
	t.Setenv("GROUP_AUDIT_DSN", path)
	var output, diagnostic bytes.Buffer
	require.Equal(t, 2, run([]string{"--driver=sqlite3"}, &output, &diagnostic))
	require.Empty(t, output.String())
}
