package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
)

// TestRetireLegacyAbilitiesMigrationPostgresWindowDeletion pins the
// v0.43.0 code-review #2 fix on PostgreSQL: routes removed from a parent
// resource during the deploy-then-migrate window must not be resurrected by
// the backfill, which validates the old projection against the parent's
// current models CSV and the account's current group CSV. DDL is
// transactional here, so the marker-failure replay hazard tested on MySQL
// cannot occur and is not exercised.
//
// The smoke database is disposable; never enable this against production.
func TestRetireLegacyAbilitiesMigrationPostgresWindowDeletion(t *testing.T) {
	dsn := os.Getenv("MIGRATIONS_DSN")
	if os.Getenv("MIGRATIONS_DRIVER") != "postgres" || dsn == "" {
		t.Skip("requires the disposable Postgres migration smoke database")
	}
	admin, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = admin.Close() })
	schema := fmt.Sprintf("moa151review%d", os.Getpid())
	_, err = admin.Exec(`CREATE SCHEMA "` + schema + `"`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = admin.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`)
	})
	db, err := sql.Open("pgx", dsn+"&search_path="+schema)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()

	dir, err := filepath.Abs(filepath.Join("..", "..", "..", "migrations", "postgres"))
	require.NoError(t, err)
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
	applied, err := NewWithDriver(db, stage, "postgres").Apply(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, applied)

	_, err = db.ExecContext(ctx, `INSERT INTO routing_groups (id, key, display_name, description, status, access_mode, model_access_mode, created_at, updated_at)
		VALUES (10, 'default', 'default', '', 'enabled', 'public', 'all_authorized', 0, 0);
		INSERT INTO channels (id, status, name, "group", models, priority)
		VALUES (1, 1, 'legacy-channel', 'default', 'legacy-a, legacy-b', 5);
		INSERT INTO subscription_accounts (id, name, platform, status, "group", models, priority)
		VALUES (1, 'legacy-account', 'codex', 1, 'default', 'legacy-a', 7);
		INSERT INTO abilities ("group", model, channel_id, enabled, priority)
		VALUES ('default', 'legacy-a', 1, 1, 5), ('default', 'legacy-b', 1, 1, 6),
		       ('default', 'removed-a', 1, 1, 7),
		       ('default', 'legacy-orphan-channel', 999, 1, 9);
		INSERT INTO subscription_account_abilities ("group", model, platform, account_id, enabled, priority)
		VALUES ('default', 'legacy-a', 'codex', 1, 1, 7),
		       ('default', 'removed-b', 'codex', 1, 1, 8),
		       ('stale-group', 'legacy-a', 'codex', 1, 1, 9),
		       ('default', 'legacy-orphan-account', 'codex', 999, 1, 9);`)
	require.NoError(t, err)

	seedRetirementCSVBoundaries(t, db, "postgres")
	raw, err := os.ReadFile(filepath.Join(dir, "151_retire_legacy_abilities.sql"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(stage, "151_retire_legacy_abilities.sql"), raw, 0o600))
	applied, err = NewWithDriver(db, stage, "postgres").Apply(ctx)
	require.NoError(t, err)
	require.Contains(t, applied, "151_retire_legacy_abilities")
	assertRetirementCSVBoundaries(t, db)

	var channelMappings int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM model_channel_mapping WHERE channel_id = 1 AND enabled = TRUE AND config = '{"_managed_by":"channel_models"}'`).Scan(&channelMappings))
	require.Equal(t, 2, channelMappings)
	var resurrectedChannel int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM model_channel_mapping mcm JOIN models m ON m.id = mcm.model_id WHERE mcm.channel_id = 1 AND m.model_id = 'removed-a'`).Scan(&resurrectedChannel))
	require.Zero(t, resurrectedChannel)
	var resurrectedSubscriptionModel int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM model_subscription_mapping msm JOIN models m ON m.id = msm.model_id WHERE msm.subscription_account_id = 1 AND m.model_id = 'removed-b'`).Scan(&resurrectedSubscriptionModel))
	require.Zero(t, resurrectedSubscriptionModel)
	var resurrectedSubscriptionGroup int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM model_subscription_mapping WHERE subscription_account_id = 1 AND group_name = 'stale-group'`).Scan(&resurrectedSubscriptionGroup))
	require.Zero(t, resurrectedSubscriptionGroup)
	var subscriptionMappings int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM model_subscription_mapping WHERE subscription_account_id = 1 AND group_name = 'default' AND enabled = TRUE AND routing_group_id = 10`).Scan(&subscriptionMappings))
	require.Equal(t, 1, subscriptionMappings)
}
