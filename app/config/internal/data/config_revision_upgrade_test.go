package data

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/config/internal/biz"
	"micro-one-api/domain/authorization"
	authztest "micro-one-api/domain/authorization/testutil"
	"micro-one-api/platform/database/migrate"
	dbtest "micro-one-api/platform/database/testutil"
)

func TestConfigLegacyRevisionUpgrade(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := dbtest.RoutingContextDB(t, driver)
			r := &Repository{db: db}
			for _, key := range []string{"notice", "current", "deleted", "invalid"} {
				require.NoError(t, r.Set(context.Background(), &biz.ConfigEntry{Namespace: "system", Key: key, Value: key}))
			}
			// Migration 105 left existing config keys at revision zero.
			require.NoError(t, db.Model(&configModel{}).Where("key = ?", "notice").Update("revision", 0).Error)
			require.NoError(t, db.Model(&configModel{}).Where("key IN ?", []string{"current", "deleted"}).Update("revision", 7).Error)
			require.NoError(t, db.Model(&configModel{}).Where("key = ?", "deleted").Update("deleted", 1).Error)
			require.NoError(t, db.Model(&configModel{}).Where("key = ?", "invalid").Update("revision", -1).Error)
			require.NoError(t, db.Exec("DELETE FROM schema_migrations WHERE version = ?", "125_backfill_config_revision").Error)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			dir := filepath.Join("../../../../migrations")
			if driver != "mysql" {
				dir = filepath.Join(dir, driver)
			}
			runner := migrate.NewWithDriver(sqlDB, dir, driver).WithOwnershipFilter("config")
			_, err = runner.Apply(context.Background())
			require.NoError(t, err)
			uc := biz.NewConfigUsecase(r)
			uc.SetAuthorization(&authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{"system.content.notice.update": authztest.All()}})
			stored, err := r.Get(context.Background(), "system", "notice")
			require.NoError(t, err)
			ctx := authorization.WithWriteReason(authztest.Context(), "remove expired notice")
			ctx = authorization.WithExpectedResourceRevision(ctx, uint64(stored.Revision))
			require.ErrorIs(t, uc.DeleteConfig(authorization.WithWriteReason(ctx, ""), "system", "notice"), biz.ErrConfigMutationRequired)
			require.NoError(t, uc.DeleteConfig(ctx, "system", "notice"))
			var deleted configModel
			require.NoError(t, db.Where("key = ?", "notice").First(&deleted).Error)
			require.EqualValues(t, 2, deleted.Revision)
			require.EqualValues(t, 1, deleted.Deleted)
			_, err = uc.SetConfigEntry(ctx, "system", "notice", "stale", "")
			require.ErrorIs(t, err, biz.ErrConfigRevisionConflict)
			for key, revision := range map[string]int64{"current": 7, "deleted": 7, "invalid": -1} {
				var row configModel
				require.NoError(t, db.Where("key = ?", key).First(&row).Error)
				require.Equal(t, revision, row.Revision)
			}
			applied, err := runner.Apply(context.Background())
			require.NoError(t, err)
			require.Empty(t, applied)
		})
	}
}
