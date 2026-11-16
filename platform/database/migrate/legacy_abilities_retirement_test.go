package migrate

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

func seedRetirementCSVBoundaries(t *testing.T, db *sql.DB, driver string) {
	t.Helper()
	key, group := `"key"`, `"group"`
	if driver == "mysql" {
		key, group = "`key`", "`group`"
	}
	_, err := db.Exec(fmt.Sprintf(`
		INSERT INTO routing_groups (id, %s, display_name, description, status, access_mode, model_access_mode, created_at, updated_at)
		VALUES (20, 'vip users', 'spaced group', '', 'enabled', 'public', 'all_authorized', 0, 0),
		       (21, 'VIP', 'upper group', '', 'enabled', 'public', 'all_authorized', 0, 0);
		INSERT INTO channels (id, status, name, %s, models, priority)
		VALUES (20, 1, 'spaced-models', 'default', ' 	MODEL ONE 	, model two 	 ', 5),
		       (21, 1, 'removed-unspaced-model', 'default', 'model one', 5);
		INSERT INTO subscription_accounts (id, name, platform, status, %s, models, priority)
		VALUES (20, 'spaced-routes', 'codex', 1, ' 	vip users 	, VIP 	 ', ' 	MODEL ONE 	, model two 	 ', 7),
		       (21, 'removed-unspaced-group', 'codex', 1, 'vip users', 'legacy-a', 7),
		       (22, 'removed-lowercase-group', 'codex', 1, 'VIP', 'legacy-a', 7),
		       (23, 'removed-unspaced-model', 'codex', 1, 'default', 'model one', 7);
		INSERT INTO abilities (%s, model, channel_id, enabled, priority)
		VALUES ('default', 'model one', 20, 1, 5), ('default', 'model two', 20, 1, 5),
		       ('default', 'modelone', 21, 1, 5);
		INSERT INTO subscription_account_abilities (%s, model, platform, account_id, enabled, priority)
		VALUES ('vip users', 'model one', 'codex', 20, 1, 7), ('VIP', 'model two', 'codex', 20, 1, 7),
		       ('vipusers', 'legacy-a', 'codex', 21, 1, 7),
		       ('vip', 'legacy-a', 'codex', 22, 1, 7),
		       ('default', 'modelone', 'codex', 23, 1, 7);
	`, key, group, group, group, group))
	require.NoError(t, err)
}

func assertRetirementCSVBoundaries(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, test := range []struct {
		name, query string
		want        int
	}{
		{"spaced_channel_tokens", `SELECT COUNT(*) FROM model_channel_mapping WHERE channel_id = 20`, 2},
		{"removed_unspaced_channel_model", `SELECT COUNT(*) FROM model_channel_mapping WHERE channel_id = 21`, 0},
		{"spaced_subscription_tokens", `SELECT COUNT(*) FROM model_subscription_mapping WHERE subscription_account_id = 20`, 2},
		{"removed_unspaced_group", `SELECT COUNT(*) FROM model_subscription_mapping WHERE subscription_account_id = 21`, 0},
		{"removed_lowercase_group", `SELECT COUNT(*) FROM model_subscription_mapping WHERE subscription_account_id = 22`, 0},
		{"removed_unspaced_subscription_model", `SELECT COUNT(*) FROM model_subscription_mapping WHERE subscription_account_id = 23`, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			var count int
			require.NoError(t, db.QueryRow(test.query).Scan(&count))
			require.Equal(t, test.want, count)
		})
	}
}

func TestRetireLegacyAbilitiesMigrationSQLite(t *testing.T) {
	dir := sqliteDialectDir(t)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".sql" && entry.Name() < "151_" {
			files = append(files, entry.Name())
		}
	}
	sort.Strings(files)
	stage := tempDirWithFiles(t, files, dir)
	db := openScratchSqlite(t)
	_, err = NewWithDriver(db, stage, "sqlite3").Apply(t.Context())
	require.NoError(t, err)

	_, err = db.Exec(`
		INSERT INTO routing_groups (id, key, display_name, description, status, access_mode, model_access_mode, created_at, updated_at)
		VALUES (10, 'default', 'default', '', 'enabled', 'public', 'all_authorized', 0, 0);
		INSERT INTO channels (id, status, name, "group", models, priority)
		VALUES (1, 1, 'legacy-channel', 'default', 'legacy-a, legacy-b', 5),
		       (2, 1, 'removed-literal-models', 'default', 'modelXa,otherXa', 5),
		       (3, 1, 'retained-literal-models', 'default', 'model_a,other%a', 5);
		INSERT INTO subscription_accounts (id, name, platform, status, "group", models, priority)
		VALUES (1, 'legacy-account', 'codex', 1, 'default', 'legacy-a', 7),
		       (2, 'removed-literal-models', 'codex', 1, 'default', 'modelXa,otherXa', 7),
		       (3, 'removed-literal-groups', 'codex', 1, 'groupXb,otherXb', 'legacy-a', 7),
		       (4, 'retained-literal-routes', 'codex', 1, 'group_b,other%b', 'model_a,other%a', 7);
		INSERT INTO abilities ("group", model, channel_id, enabled, priority)
		VALUES ('default', 'legacy-a', 1, 1, 5), ('default', 'legacy-b', 1, 1, 6),
		       ('default', 'LEGACY-B', 1, 1, 6),
		       ('default', 'removed-a', 1, 1, 7),
		       ('default', 'model_a', 2, 1, 5), ('default', 'other%a', 2, 1, 5),
		       ('default', 'model_a', 3, 1, 5), ('default', 'other%a', 3, 1, 5),
		       ('default', 'legacy-orphan-channel', 999, 1, 9);
		INSERT INTO subscription_account_abilities ("group", model, platform, account_id, enabled, priority)
		VALUES ('default', 'legacy-a', 'codex', 1, 1, 7),
		       ('default', 'removed-b', 'codex', 1, 1, 8),
		       ('stale-group', 'legacy-a', 'codex', 1, 1, 9),
		       ('default', 'model_a', 'codex', 2, 1, 7), ('default', 'other%a', 'codex', 2, 1, 7),
		       ('group_b', 'legacy-a', 'codex', 3, 1, 7), ('other%b', 'legacy-a', 'codex', 3, 1, 7),
		       ('group_b', 'model_a', 'codex', 4, 1, 7), ('other%b', 'other%a', 'codex', 4, 1, 7),
		       ('default', 'legacy-orphan-account', 'codex', 999, 1, 9);
	`)
	require.NoError(t, err)

	seedRetirementCSVBoundaries(t, db, "sqlite")
	raw, err := os.ReadFile(filepath.Join(dir, "151_retire_legacy_abilities.sql"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(stage, "151_retire_legacy_abilities.sql"), raw, 0o600))
	applied, err := NewWithDriver(db, stage, "sqlite3").Apply(t.Context())
	require.NoError(t, err)
	require.Contains(t, applied, "151_retire_legacy_abilities")
	assertRetirementCSVBoundaries(t, db)

	for _, test := range []struct {
		name  string
		query string
		want  int
	}{
		{"removed_literal_channel_models", `SELECT COUNT(*) FROM model_channel_mapping WHERE channel_id = 2`, 0},
		{"removed_literal_subscription_models", `SELECT COUNT(*) FROM model_subscription_mapping WHERE subscription_account_id = 2`, 0},
		{"removed_literal_subscription_groups", `SELECT COUNT(*) FROM model_subscription_mapping WHERE subscription_account_id = 3`, 0},
		{"retained_literal_channel_models", `SELECT COUNT(*) FROM model_channel_mapping WHERE channel_id = 3`, 2},
		{"retained_literal_subscription_routes", `SELECT COUNT(*) FROM model_subscription_mapping WHERE subscription_account_id = 4`, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			var mappings int
			require.NoError(t, db.QueryRow(test.query).Scan(&mappings))
			require.Equal(t, test.want, mappings)
		})
	}

	var models int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM models WHERE model_id IN ('legacy-a', 'legacy-b')`).Scan(&models))
	require.Equal(t, 2, models)
	var channelMappings int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM model_channel_mapping WHERE channel_id = 1 AND enabled = 1 AND config = '{"_managed_by":"channel_models"}'`).Scan(&channelMappings))
	require.Equal(t, 2, channelMappings)
	// Routes removed from the parent during the deploy-then-migrate window must
	// stay removed: the backfill validates the old projection against the
	// parent's current models/group CSV, so stale abilities rows (including
	// differently-cased duplicates of a listed model) must not resurrect.
	var resurrectedChannel int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM model_channel_mapping mcm JOIN models m ON m.id = mcm.model_id WHERE mcm.channel_id = 1 AND m.model_id = 'removed-a'`).Scan(&resurrectedChannel))
	require.Zero(t, resurrectedChannel)
	var resurrectedSubscriptionModel int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM model_subscription_mapping msm JOIN models m ON m.id = msm.model_id WHERE msm.subscription_account_id = 1 AND m.model_id = 'removed-b'`).Scan(&resurrectedSubscriptionModel))
	require.Zero(t, resurrectedSubscriptionModel)
	var resurrectedSubscriptionGroup int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM model_subscription_mapping WHERE subscription_account_id = 1 AND group_name = 'stale-group'`).Scan(&resurrectedSubscriptionGroup))
	require.Zero(t, resurrectedSubscriptionGroup)
	// Dangling legacy rows must not be projected: the mapping tables carry
	// parent FKs in MySQL/Postgres, and SQLite keeps them implicit.
	var orphanChannelMappings int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM model_channel_mapping mcm LEFT JOIN channels c ON c.id = mcm.channel_id WHERE c.id IS NULL`).Scan(&orphanChannelMappings))
	require.Zero(t, orphanChannelMappings)
	var orphanSubscriptionMappings int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM model_subscription_mapping msm LEFT JOIN subscription_accounts sa ON sa.id = msm.subscription_account_id WHERE sa.id IS NULL`).Scan(&orphanSubscriptionMappings))
	require.Zero(t, orphanSubscriptionMappings)
	var routingGroupID int64
	var subscriptionConfig string
	require.NoError(t, db.QueryRow(`SELECT routing_group_id, config FROM model_subscription_mapping WHERE subscription_account_id = 1 AND group_name = 'default' AND enabled = 1`).Scan(&routingGroupID, &subscriptionConfig))
	require.EqualValues(t, 10, routingGroupID)
	require.JSONEq(t, `{"_managed_by":"subscription_models"}`, subscriptionConfig)
	var legacyTables int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name IN ('abilities', 'subscription_account_abilities')`).Scan(&legacyTables))
	require.Zero(t, legacyTables)
	var restrictColumns int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('channels') WHERE name = 'restrict_models'`).Scan(&restrictColumns))
	require.Zero(t, restrictColumns)
}
