package data

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/admin/internal/biz"
	"micro-one-api/domain/authorization"
	authztest "micro-one-api/domain/authorization/testutil"
	dbtest "micro-one-api/platform/database/testutil"
)

func TestModelPricingPublishesWithOnlyScopedPricePermission(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := dbtest.RoutingContextDB(t, driver)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			repo := NewSystemOptionsRepoWithDriver(sqlDB, driver)
			repo.gdb = db
			require.NoError(t, db.Exec("INSERT INTO models (id,model_id,display_name) VALUES (1001,'new-model','New'),(1002,'other-model','Other')").Error)
			require.NoError(t, repo.Set(context.Background(), "ModelPrice", `{"NEW-MODEL":{"input_price":0.000009,"cache_creation_5m_price":0},"other-model":{"input_price":0.000004}}`))
			uc := biz.NewSystemOptionsUsecase(repo)
			uc.SetAuthorization(&authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{"billing.pricing.update": authztest.Resources(1001)}})
			require.NoError(t, uc.PublishModelPricing(authztest.Context(), nil, biz.ModelPricing{ModelID: " New-Model ", InputPrice: 0.3, OutputPrice: 1.2, CacheReadPrice: 0.06}))
			raw, err := repo.Get(context.Background(), "ModelPrice")
			require.NoError(t, err)
			require.JSONEq(t, `{"new-model":{"input_price":0.0000003,"output_price":0.0000012,"cache_read_price":0.00000006,"cache_creation_5m_price":0},"other-model":{"input_price":0.000004}}`, raw)
			revision, err := repo.GetRevision(context.Background(), "ModelPrice")
			require.NoError(t, err)
			require.EqualValues(t, 2, revision)
			var audits int64
			require.NoError(t, db.Table("resource_write_audits").Where("operation = ? AND resource_id = ? AND result = ?", "billing.pricing.update", "1001", "success").Count(&audits).Error)
			require.EqualValues(t, 1, audits)
			require.ErrorIs(t, uc.PublishModelPricing(authztest.Context(), nil, biz.ModelPricing{ModelID: "other-model", InputPrice: 10}), authorization.ErrDenied)
			unchanged, err := repo.Get(context.Background(), "ModelPrice")
			require.NoError(t, err)
			require.Equal(t, raw, unchanged)
			// Full-map editors use the same revision, so an old open editor
			// cannot silently replace this newly published model price.
			require.ErrorIs(t, repo.SetMany(context.Background(), map[string]string{"ModelPrice": "{}"}, map[string]int64{"ModelPrice": 1}), biz.ErrSystemOptionConflict)
		})
	}
}

func TestModelPricingCreatesOptionAndPreservesLatestOtherModel(t *testing.T) {
	db := dbtest.RoutingContextDB(t, "sqlite")
	sqlDB, err := db.DB()
	require.NoError(t, err)
	repo := NewSystemOptionsRepoWithDriver(sqlDB, "sqlite")
	repo.gdb = db
	require.NoError(t, db.Exec("DELETE FROM system_options WHERE option_key = 'ModelPrice'").Error)
	require.NoError(t, db.Exec("INSERT INTO models (id,model_id,display_name) VALUES (1001,'a','A'),(1002,'b','B')").Error)
	uc := biz.NewSystemOptionsUsecase(repo)
	for _, model := range []string{"a", "b"} {
		require.NoError(t, uc.PublishModelPricing(context.Background(), nil, biz.ModelPricing{ModelID: model, InputPrice: 1}))
	}
	raw, err := repo.Get(context.Background(), "ModelPrice")
	require.NoError(t, err)
	require.JSONEq(t, `{"a":{"input_price":0.000001,"output_price":0},"b":{"input_price":0.000001,"output_price":0}}`, raw)
	before := biz.ModelPricing{ModelID: "a", InputPrice: 1}
	require.NoError(t, uc.PublishModelPricing(context.Background(), &before, before))
	revision, err := repo.GetRevision(context.Background(), "ModelPrice")
	require.NoError(t, err)
	require.EqualValues(t, 2, revision, "unchanged prices must not create a new revision")
}

func TestModelPricingConcurrentFirstCreationPreservesSuccessfulWriters(t *testing.T) {
	db := dbtest.RoutingContextDB(t, "sqlite")
	sqlDB, err := db.DB()
	require.NoError(t, err)
	repo := NewSystemOptionsRepoWithDriver(sqlDB, "sqlite")
	repo.gdb = db
	require.NoError(t, db.Exec("DELETE FROM system_options WHERE option_key = 'ModelPrice'").Error)
	require.NoError(t, db.Exec("INSERT INTO models (id,model_id,display_name) VALUES (1001,'a','A'),(1002,'b','B')").Error)
	uc := biz.NewSystemOptionsUsecase(repo)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, model := range []string{"a", "b"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- uc.PublishModelPricing(context.Background(), nil, biz.ModelPricing{ModelID: model, InputPrice: 1})
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	raw, err := repo.Get(context.Background(), "ModelPrice")
	require.NoError(t, err)
	require.JSONEq(t, `{"a":{"input_price":0.000001,"output_price":0},"b":{"input_price":0.000001,"output_price":0}}`, raw)
}

func TestModelPricingRejectsCorruptOptionWithoutReplacingOtherPrices(t *testing.T) {
	db := dbtest.RoutingContextDB(t, "sqlite")
	sqlDB, err := db.DB()
	require.NoError(t, err)
	repo := NewSystemOptionsRepoWithDriver(sqlDB, "sqlite")
	repo.gdb = db
	require.NoError(t, db.Exec("INSERT INTO models (id,model_id,display_name) VALUES (1001,'m','M')").Error)
	uc := biz.NewSystemOptionsUsecase(repo)
	for _, raw := range []string{`{`, `[]`, `{"m":3,"other":{"input_price":1}}`} {
		require.NoError(t, repo.Set(context.Background(), "ModelPrice", raw))
		require.ErrorIs(t, uc.PublishModelPricing(context.Background(), nil, biz.ModelPricing{ModelID: "m", InputPrice: 1}), biz.ErrPricingOptionsInvalid)
		unchanged, err := repo.Get(context.Background(), "ModelPrice")
		require.NoError(t, err)
		require.Equal(t, raw, unchanged)
	}
}
