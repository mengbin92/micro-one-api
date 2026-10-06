package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	dbtest "micro-one-api/platform/database/testutil"
)

func TestAdminResetCutoverGateDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := dbtest.RoutingContextDB(t, driver)
			ctx := context.Background()
			created, err := upsertPassword(ctx, db, "admin", "legacy-hash", "", 0, -1)
			require.NoError(t, err)
			require.True(t, created)
			created, err = upsertPassword(ctx, db, "admin", "rotated-hash", "", 0, -1)
			require.NoError(t, err)
			require.False(t, created)
			var epoch int64
			require.NoError(t, db.Table("users").Select("password_changed_at").Where("username = ?", "admin").Scan(&epoch).Error)
			require.Positive(t, epoch)
			for _, state := range []struct{ mode, cutover string }{{"legacy", "blocked"}, {"iam", "verified"}, {"iam", "complete"}} {
				updates := map[string]any{"authorization_mode": state.mode, "cutover_state": state.cutover, "cutover_batch_id": "reset-test", "cutover_verified_at": time.Now().UnixMilli()}
				if state.mode == "legacy" {
					updates["cutover_verified_at"] = nil
				}
				require.NoError(t, db.Table("iam_policy_state").Where("id = 1").Updates(updates).Error)
				created, err = upsertPassword(ctx, db, "admin", "forbidden", "", 0, 100)
				require.ErrorContains(t, err, "legacy admin-reset disabled")
				require.False(t, created)
				_, err = upsertPassword(ctx, db, "new-root", "forbidden", "", 0, 100)
				require.Error(t, err)
				var hash string
				require.NoError(t, db.Table("users").Select("password_hash").Where("username = ?", "admin").Scan(&hash).Error)
				require.Equal(t, "rotated-hash", hash)
				var count int64
				require.NoError(t, db.Table("users").Count(&count).Error)
				require.EqualValues(t, 1, count)
			}
		})
	}
}
