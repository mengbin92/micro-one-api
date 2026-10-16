package data

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"micro-one-api/app/admin/internal/biz"
	"micro-one-api/domain/authorization"
	authztest "micro-one-api/domain/authorization/testutil"
	dbtest "micro-one-api/platform/database/testutil"
)

func TestPricingBatchRejectsStaleLegacyAndConcurrentWriters(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := dbtest.RoutingContextDB(t, driver)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			repo := NewSystemOptionsRepoWithDriver(sqlDB, driver)
			repo.gdb = db
			require.NoError(t, db.Exec("INSERT INTO models (id,model_id,display_name) VALUES (1001,'a','A'),(1002,'b','B')").Error)
			keys := []string{"CompletionRatio", "ModelPrice", "ModelRatio"}
			for _, key := range keys {
				require.NoError(t, repo.Set(context.Background(), key, `{}`))
			}
			uc := biz.NewSystemOptionsUsecase(repo)
			values := map[string]string{"CompletionRatio": `{"a":2}`, "ModelPrice": `{"a":{"input_price":2}}`, "ModelRatio": `{"a":2}`}
			stale := map[string]int64{"CompletionRatio": 1, "ModelPrice": 1, "ModelRatio": 99}
			require.ErrorIs(t, uc.SetMany(context.Background(), values, stale), biz.ErrSystemOptionConflict)
			for _, key := range keys {
				value, err := repo.Get(context.Background(), key)
				require.NoError(t, err)
				require.Equal(t, `{}`, value, "a stale final key must roll back earlier keys")
			}
			versions := map[string]int64{"CompletionRatio": 1, "ModelPrice": 1, "ModelRatio": 1}
			start := make(chan struct{})
			results := make(chan error, 2)
			var wg sync.WaitGroup
			for i := 0; i < 2; i++ {
				wg.Add(1)
				go func() { defer wg.Done(); <-start; results <- uc.SetMany(context.Background(), values, versions) }()
			}
			close(start)
			wg.Wait()
			close(results)
			success, conflicts := 0, 0
			for err := range results {
				if err == nil {
					success++
				} else {
					require.ErrorIs(t, err, biz.ErrSystemOptionConflict)
					conflicts++
				}
			}
			require.Equal(t, 1, success)
			require.Equal(t, 1, conflicts)
			for _, key := range keys {
				value, err := repo.Get(context.Background(), key)
				require.NoError(t, err)
				require.Equal(t, values[key], value)
			}
			versions = map[string]int64{"CompletionRatio": 2, "ModelPrice": 2, "ModelRatio": 2}
			failedValues := map[string]string{"CompletionRatio": `{"a":3}`, "ModelPrice": `{"a":{"input_price":3}}`, "ModelRatio": `{"a":3}`}
			storageErr := errors.New("injected final-key failure")
			require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:pricing_failure", func(tx *gorm.DB) {
				if row, ok := tx.Statement.Dest.(map[string]any); ok && tx.Statement.Table == "system_options" && row["option_key"] == "ModelRatio" {
					tx.AddError(storageErr)
				}
			}))
			require.ErrorIs(t, uc.SetMany(context.Background(), failedValues, versions), storageErr)
			require.NoError(t, db.Callback().Update().Remove("test:pricing_failure"))
			uc.SetAuthorization(&authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{"system.option.update": authztest.All(), "system.option.pricing.update": authztest.All(), "billing.pricing.update": authztest.Resources(1001)}})
			failedValues["ModelPrice"] = `{"a":{"input_price":3},"b":{"input_price":3}}`
			ctx := authorization.WithWriteReason(authztest.Context(), "restricted model pricing")
			require.ErrorIs(t, uc.SetMany(ctx, failedValues, versions), authorization.ErrDenied)
			for _, key := range keys {
				value, err := repo.Get(context.Background(), key)
				require.NoError(t, err)
				require.Equal(t, values[key], value, "storage or authorization failure must roll back all keys")
				revision, err := repo.GetRevision(context.Background(), key)
				require.NoError(t, err)
				require.EqualValues(t, 2, revision)
			}
		})
	}
}

func TestPricingBatchConcurrentFirstCreation(t *testing.T) {
	for _, tc := range []struct {
		name, driver string
		single       bool
	}{{"sqlite", "sqlite", false}, {"mysql", "mysql", false}, {"postgres", "postgres", false}, {"postgres_single", "postgres", true}} {
		t.Run(tc.name, func(t *testing.T) {
			driver := tc.driver
			db := dbtest.RoutingContextDB(t, driver)
			require.NoError(t, db.Exec("DELETE FROM system_options WHERE option_key IN ('CompletionRatio','ModelPrice','ModelRatio')").Error)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			repo := NewSystemOptionsRepoWithDriver(sqlDB, driver)
			repo.gdb = db
			uc := biz.NewSystemOptionsUsecase(repo)
			firstKey := "CompletionRatio"
			if tc.single {
				firstKey = "ModelPrice"
			}
			var arrivals atomic.Int32
			bothReadMissing := make(chan struct{})
			require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:missing_price_barrier", func(tx *gorm.DB) {
				if tx.Statement.Table != "system_options" || !errors.Is(tx.Error, gorm.ErrRecordNotFound) || len(tx.Statement.Vars) == 0 || tx.Statement.Vars[0] != firstKey {
					return
				}
				n := arrivals.Add(1)
				if n == 2 {
					close(bothReadMissing)
				}
				if n <= 2 {
					<-bothReadMissing
				}
			}))
			results := make(chan error, 2)
			for _, amount := range []string{"2", "3"} {
				go func() {
					values := map[string]string{"CompletionRatio": `{"a":` + amount + `}`, "ModelPrice": `{"a":{"input_price":` + amount + `}}`, "ModelRatio": `{"a":` + amount + `}`}
					versions := map[string]int64{"CompletionRatio": 0, "ModelPrice": 0, "ModelRatio": 0}
					if tc.single {
						delete(values, "CompletionRatio")
						delete(values, "ModelRatio")
						delete(versions, "CompletionRatio")
						delete(versions, "ModelRatio")
					}
					results <- uc.SetMany(context.Background(), values, versions)
				}()
			}
			success := 0
			for i := 0; i < 2; i++ {
				if err := <-results; err == nil {
					success++
				} else {
					require.ErrorIs(t, err, biz.ErrSystemOptionConflict)
				}
			}
			require.Equal(t, 1, success, "only one administrator may create a pricing generation")
			ratio, err := repo.Get(context.Background(), "ModelRatio")
			require.NoError(t, err)
			completion, err := repo.Get(context.Background(), "CompletionRatio")
			require.NoError(t, err)
			require.Equal(t, ratio, completion)
		})
	}
}
