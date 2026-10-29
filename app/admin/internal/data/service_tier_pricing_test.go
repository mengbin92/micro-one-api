package data

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/admin/internal/biz"
	"micro-one-api/domain/authorization"
	authztest "micro-one-api/domain/authorization/testutil"
	dbtest "micro-one-api/platform/database/testutil"
)

func TestServiceTierPricingOwnerPermissionsAndRevision(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := dbtest.RoutingContextDB(t, driver)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			repo := NewSystemOptionsRepoWithDriver(sqlDB, driver)
			repo.gdb = db
			const initial = `{"enabled":false,"upstream_prices":{"private":"cost"}}`
			require.NoError(t, repo.Set(context.Background(), "ServiceTierPricing", initial))
			policy := &authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{
				"system.option.read": authztest.All(), "system.option.update": authztest.All(),
				"system.option.pricing.update": authztest.All(), "billing.pricing.read": authztest.All(),
				"billing.pricing.update": authztest.All(),
			}}
			uc := biz.NewSystemOptionsUsecase(repo)
			uc.SetAuthorization(policy)
			ctx := authorization.WithWriteReason(authztest.Context(), "reviewed tier prices")
			read, err := uc.Get(ctx, "ServiceTierPricing")
			require.NoError(t, err)
			require.Empty(t, read, "sale price access cannot disclose procurement tariffs")
			values := map[string]string{"ServiceTierPricing": `{"enabled":false}`}
			versions := map[string]int64{"ServiceTierPricing": 1}
			require.ErrorIs(t, uc.SetMany(ctx, values, versions), authorization.ErrDenied)
			policy.Scopes["billing.upstream_cost.read"] = authztest.All()
			policy.Scopes["billing.upstream_cost.update"] = authztest.Resources(71)
			require.ErrorIs(t, uc.SetMany(ctx, values, versions), authorization.ErrDenied, "one scoped cost grant cannot replace the entire config")
			read, err = uc.Get(ctx, "ServiceTierPricing")
			require.NoError(t, err)
			require.Equal(t, initial, read)
			policy.Scopes["billing.upstream_cost.update"] = authztest.All()
			require.NoError(t, uc.SetMany(ctx, values, versions))
			require.ErrorIs(t, uc.SetMany(ctx, map[string]string{"ServiceTierPricing": `{"enabled":false,"version":1}`}, versions), biz.ErrSystemOptionConflict)
			stored, err := repo.Get(context.Background(), "ServiceTierPricing")
			require.NoError(t, err)
			require.Equal(t, values["ServiceTierPricing"], stored)
			revision, err := repo.GetRevision(context.Background(), "ServiceTierPricing")
			require.NoError(t, err)
			require.EqualValues(t, 2, revision)
			// Both a direct Set and a batch enforce the same JSON contract.
			require.Error(t, uc.Set(biz.WithExpectedOptionRevision(ctx, 2), "ServiceTierPricing", `{"enabled":true}`))
			require.Error(t, uc.SetMany(ctx, map[string]string{"ServiceTierPricing": `{"enabled":true}`}, map[string]int64{"ServiceTierPricing": 2}))
		})
	}
}

func TestServiceTierPricingRejectsAmbiguousOptionKeys(t *testing.T) {
	// Case-insensitive SQL keys must not bypass whole-object tariff checks.
	db := dbtest.RoutingContextDB(t, "sqlite")
	require.NoError(t, db.Exec("DROP TABLE system_options").Error)
	require.NoError(t, db.Exec(`CREATE TABLE system_options (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		option_key TEXT NOT NULL COLLATE NOCASE UNIQUE,
		option_value TEXT NOT NULL,
		revision INTEGER NOT NULL DEFAULT 0,
		updated_at DATETIME
	)`).Error)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	repo := NewSystemOptionsRepoWithDriver(sqlDB, "sqlite")
	repo.gdb = db
	const initial = `{"enabled":false,"upstream_prices":{"private":"cost"}}`
	require.NoError(t, repo.Set(context.Background(), "ServiceTierPricing", initial))
	uc := biz.NewSystemOptionsUsecase(repo)
	uc.SetAuthorization(&authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{
		"system.option.read": authztest.All(), "system.option.update": authztest.All(),
		"system.option.pricing.update": authztest.All(), "billing.pricing.read": authztest.All(),
		"billing.pricing.update": authztest.All(),
	}})
	ctx := authorization.WithWriteReason(authztest.Context(), "tariff review")
	for _, key := range []string{"servicetierpricing", "SERVICETIERPRICING", "ServiceTierPricing ", "SérviceTierPricing", "ServiceTierPricing\u200b"} {
		t.Run(key, func(t *testing.T) {
			_, err := uc.Get(ctx, key)
			require.Error(t, err, "an alias must not disclose procurement prices")
			require.Error(t, uc.Set(ctx, key, `{"enabled":true}`))
			require.Error(t, uc.Mutate(biz.WithExpectedOptionRevision(ctx, 0), key, func(string) (string, error) { return `{"enabled":true}`, nil }))
			require.Error(t, uc.SetMany(ctx, map[string]string{key: `{"enabled":true}`}, map[string]int64{key: 0}))
		})
	}
	stored, err := repo.Get(context.Background(), "ServiceTierPricing")
	require.NoError(t, err)
	require.Equal(t, initial, stored)
}
