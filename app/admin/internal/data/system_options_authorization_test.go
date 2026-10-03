package data

import (
	"context"
	"github.com/stretchr/testify/require"
	"micro-one-api/app/admin/internal/biz"
	"micro-one-api/domain/authorization"
	authztest "micro-one-api/domain/authorization/testutil"
	"micro-one-api/pkg/jsonx"
	dbtest "micro-one-api/platform/database/testutil"
	"strconv"
	"testing"
)

func TestIAMB3SystemOptionsOwnerDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := dbtest.RoutingContextDB(t, driver)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			repo := NewSystemOptionsRepoWithDriver(sqlDB, driver)
			repo.gdb = db
			require.NoError(t, db.Exec("INSERT INTO models (id,model_id,display_name) VALUES (1001,'a','A'),(1002,'b','B')").Error)
			legacy := context.Background()
			require.NoError(t, repo.Set(legacy, "UpstreamModelPrice", `{"a":{"input_price":1},"b":{"input_price":9}}`))
			require.NoError(t, repo.Set(legacy, "ModelPrice", `{"a":{"input_price":1},"b":{"input_price":9}}`))
			costA, err := repo.CostResourceID(legacy, "a")
			require.NoError(t, err)
			policy := &authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{"system.option.read": authztest.All(), "system.option.update": authztest.All(), "system.option.pricing.update": authztest.All(), "billing.pricing.read": authztest.Resources(1001), "billing.pricing.update": authztest.Resources(1001), "billing.upstream_cost.read": authztest.Resources(costA), "billing.upstream_cost.create": authztest.All(), "billing.upstream_cost.update": authztest.Resources(costA), "billing.upstream_cost.delete": authztest.Resources(costA)}}
			uc := biz.NewSystemOptionsUsecase(repo)
			uc.SetAuthorization(policy)
			ctx := biz.WithExpectedOptionRevision(authorization.WithWriteReason(authztest.Context(), "verification"), 1)
			value, err := uc.Get(ctx, "ModelPrice")
			require.NoError(t, err)
			require.JSONEq(t, `{"a":{"input_price":1}}`, value)
			value, err = uc.Get(ctx, "UpstreamModelPrice")
			require.NoError(t, err)
			require.JSONEq(t, `{"a":{"input_price":1}}`, value)
			require.ErrorIs(t, uc.Set(ctx, "ModelPrice", `{"a":{"input_price":2}}`), authorization.ErrDenied, "whole-map replacement cannot delete a hidden price")
			delete(policy.Scopes, "system.option.read")
			require.NoError(t, uc.Mutate(ctx, "UpstreamModelPrice", func(raw string) (string, error) {
				var m map[string]any
				if err := jsonx.Unmarshal([]byte(raw), &m); err != nil {
					return "", err
				}
				m["a"] = map[string]any{"input_price": 2}
				out, err := jsonx.Marshal(m)
				return string(out), err
			}))
			value, err = repo.Get(legacy, "UpstreamModelPrice")
			require.NoError(t, err)
			require.JSONEq(t, `{"a":{"input_price":2},"b":{"input_price":9}}`, value)
			require.ErrorIs(t, uc.Set(biz.WithExpectedOptionRevision(ctx, 2), "UpstreamModelPrice", `{"a":{"input_price":2},"b":{"input_price":8}}`), authorization.ErrDenied)
			var audits int64
			require.NoError(t, db.Table("resource_write_audits").Count(&audits).Error)
			require.GreaterOrEqual(t, audits, int64(3))
		})
	}
}

// The cost identity is independent of the channel/account numeric ID, and a
// multi-key failure must roll back both values and successful audit records.
func TestIAMB3SystemOptionsAtomicMigrationDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := dbtest.RoutingContextDB(t, driver)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			repo := NewSystemOptionsRepoWithDriver(sqlDB, driver)
			repo.gdb = db
			legacy := context.Background()
			require.NoError(t, db.Table("channels").Create(map[string]any{"id": 71, "name": "source"}).Error)
			require.NoError(t, db.Table("subscription_accounts").Create(map[string]any{"id": 71, "name": "account", "platform": "codex"}).Error)
			raw := `{"channel:71:a":{"input_price":1},"subscription:71:a":{"input_price":2},"71:legacy":{"input_price":3}}`
			require.NoError(t, repo.Set(legacy, "UpstreamModelPrice", raw))
			channelID, err := repo.CostResourceID(legacy, "channel:71:a")
			require.NoError(t, err)
			accountID, err := repo.CostResourceID(legacy, "subscription:71:a")
			require.NoError(t, err)
			oldID, err := repo.CostResourceID(legacy, "71:legacy")
			require.NoError(t, err)
			require.NotEqual(t, channelID, accountID)
			policy := &authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{
				"system.option.read": authztest.All(), "system.option.update": authztest.All(), "system.option.pricing.update": authztest.All(),
				"billing.upstream_cost.read": authztest.Resources(channelID), "billing.upstream_cost.create": authztest.All(),
				"billing.upstream_cost.delete": authztest.Resources(oldID), "billing.upstream_cost.migrate": authztest.Resources(oldID),
			}}
			uc := biz.NewSystemOptionsUsecase(repo)
			uc.SetAuthorization(policy)
			ctx := biz.WithExpectedOptionRevision(authorization.WithWriteReason(authztest.Context(), "reviewed migration"), 1)
			visible, err := uc.Get(ctx, "UpstreamModelPrice")
			require.NoError(t, err)
			require.JSONEq(t, `{"channel:71:a":{"input_price":1}}`, visible)
			migration, err := uc.AuthorizeUpstreamCostMigration(ctx)
			require.NoError(t, err)
			migration = biz.WithUpstreamCostMigration(migration)
			migrate := func(old string) (string, error) {
				var m map[string]any
				if err := jsonx.Unmarshal([]byte(old), &m); err != nil {
					return "", err
				}
				m["channel:71:upstream"] = m["71:legacy"]
				delete(m, "71:legacy")
				out, err := jsonx.Marshal(m)
				return string(out), err
			}
			require.ErrorIs(t, uc.Mutate(migration, "UpstreamModelPrice", migrate), authorization.ErrDenied, "new target cannot invent migration authority")
			migration = biz.WithUpstreamCostMigrationBindings(migration, map[string]string{"channel:71:upstream": "71:legacy"})
			require.NoError(t, uc.Mutate(migration, "UpstreamModelPrice", migrate), "migration checks the real old cost resource, independently of create/delete")
			var success int64
			require.NoError(t, db.Table("resource_write_audits").Where("operation = ? AND result = ? AND resource_id = ?", "billing.upstream_cost.migrate", "success", strconv.FormatInt(oldID, 10)).Count(&success).Error)
			require.EqualValues(t, 2, success)
			require.NoError(t, repo.Set(legacy, "system_name", "before"))
			require.NoError(t, repo.Set(legacy, "SystemName", "before"))
			// Sorted order updates SystemName first; the stale second key must undo it.
			require.ErrorIs(t, uc.SetMany(ctx, map[string]string{"SystemName": "after", "system_name": "after"}, map[string]int64{"SystemName": 1, "system_name": 99}), biz.ErrSystemOptionConflict)
			for _, key := range []string{"SystemName", "system_name"} {
				v, err := repo.Get(legacy, key)
				require.NoError(t, err)
				require.Equal(t, "before", v)
				revision, err := repo.GetRevision(legacy, key)
				require.NoError(t, err)
				require.EqualValues(t, 1, revision)
			}
			var before int64
			require.NoError(t, db.Table("resource_write_audits").Where("operation = ? AND result = ?", "system.option.update", "success").Count(&before).Error)
			require.NoError(t, uc.SetMany(ctx, map[string]string{"SystemName": "after", "system_name": "after"}, map[string]int64{"SystemName": 1, "system_name": 1}))
			var after int64
			require.NoError(t, db.Table("resource_write_audits").Where("operation = ? AND result = ?", "system.option.update", "success").Count(&after).Error)
			require.EqualValues(t, before+2, after)
		})
	}
}
