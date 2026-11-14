package migrate

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

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
		VALUES (1, 1, 'legacy-channel', 'default', 'legacy-a,legacy-b', 5);
		INSERT INTO subscription_accounts (id, name, platform, status, "group", models, priority)
		VALUES (1, 'legacy-account', 'codex', 1, 'default', 'legacy-a', 7);
		INSERT INTO abilities ("group", model, channel_id, enabled, priority)
		VALUES ('default', 'legacy-a', 1, 1, 5), ('default', 'legacy-b', 1, 1, 6),
		       ('default', 'legacy-orphan-channel', 999, 1, 9);
		INSERT INTO subscription_account_abilities ("group", model, platform, account_id, enabled, priority)
		VALUES ('default', 'legacy-a', 'codex', 1, 1, 7),
		       ('default', 'legacy-orphan-account', 'codex', 999, 1, 9);
	`)
	require.NoError(t, err)

	raw, err := os.ReadFile(filepath.Join(dir, "151_retire_legacy_abilities.sql"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(stage, "151_retire_legacy_abilities.sql"), raw, 0o600))
	applied, err := NewWithDriver(db, stage, "sqlite3").Apply(t.Context())
	require.NoError(t, err)
	require.Contains(t, applied, "151_retire_legacy_abilities")

	var models int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM models WHERE model_id IN ('legacy-a', 'legacy-b')`).Scan(&models))
	require.Equal(t, 2, models)
	var channelMappings int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM model_channel_mapping WHERE channel_id = 1 AND enabled = 1 AND config = '{"_managed_by":"channel_models"}'`).Scan(&channelMappings))
	require.Equal(t, 2, channelMappings)
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
