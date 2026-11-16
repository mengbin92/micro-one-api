package data

import (
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"micro-one-api/platform/database/xdb"
)

// TestEnsureModelRegistryRowTxConcurrentCreate pins the v0.43.0 code-review
// fix: two transactions concurrently ensuring the same brand-new model must
// both succeed and share one registry row. The old catch-unique-then-reread
// shape aborted the PostgreSQL transaction (25P02) on the unique conflict and
// rolled back the whole caller transaction.
//
// The barrier forces both transactions to prove the initial miss before
// either inserts, so the unique-key race is exercised deterministically.
// DSNs must point at disposable databases; the test owns the models table it
// creates.
func TestEnsureModelRegistryRowTxConcurrentCreate(t *testing.T) {
	for _, tc := range []struct {
		driver string
		dsn    string
	}{
		{"postgres", os.Getenv("TEST_SUBSCRIPTION_POSTGRES_DSN")},
		{"mysql", os.Getenv("TEST_SUBSCRIPTION_MYSQL_DSN")},
	} {
		t.Run(tc.driver, func(t *testing.T) {
			if tc.dsn == "" {
				t.Skip("isolated DSN not configured")
			}
			// Use production connection semantics, including MySQL clientFoundRows.
			db, err := xdb.Open(xdb.DatabaseConfig{Driver: tc.driver, DSN: tc.dsn})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() {
				db.Exec("DROP TABLE models")
				_ = sqlDB.Close()
			})
			// The smoke databases are shared; remove leftovers from aborted runs.
			require.NoError(t, db.Exec("DROP TABLE IF EXISTS models").Error)

			modelID := fmt.Sprintf("concurrent-ensure-%d", os.Getpid())
			// Mirror the production models table (062 + 083 + 084 + 118) with
			// the columns the PO writes.
			switch tc.driver {
			case "postgres":
				require.NoError(t, db.Exec(`CREATE TABLE models (
					id BIGSERIAL PRIMARY KEY,
					model_id VARCHAR(255) NOT NULL,
					display_name VARCHAR(255) NOT NULL DEFAULT '',
					description TEXT,
					provider VARCHAR(100) NOT NULL DEFAULT '',
					model_type VARCHAR(50) NOT NULL DEFAULT 'chat',
					context_window INTEGER NOT NULL DEFAULT 0,
					pricing_input DOUBLE PRECISION NOT NULL DEFAULT 0,
					pricing_output DOUBLE PRECISION NOT NULL DEFAULT 0,
					pricing_cache_read DOUBLE PRECISION NOT NULL DEFAULT 0,
					status INTEGER NOT NULL DEFAULT 1,
					is_public BOOLEAN NOT NULL DEFAULT TRUE,
					capabilities TEXT,
					input_modalities VARCHAR(255) NOT NULL DEFAULT '[]',
					output_modalities VARCHAR(255) NOT NULL DEFAULT '[]',
					tags TEXT,
					category VARCHAR(100) NOT NULL DEFAULT '',
					tier VARCHAR(50) NOT NULL DEFAULT '',
					metadata TEXT,
					authorization_revision BIGINT NOT NULL DEFAULT 1,
					created_at BIGINT NOT NULL DEFAULT 0,
					updated_at BIGINT NOT NULL DEFAULT 0
				)`).Error)
				require.NoError(t, db.Exec(`CREATE UNIQUE INDEX uk_model_id ON models (model_id)`).Error)
			default:
				require.NoError(t, db.Exec(`CREATE TABLE models (
					id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
					model_id VARCHAR(255) NOT NULL,
					display_name VARCHAR(255) NOT NULL DEFAULT '',
					description TEXT,
					provider VARCHAR(100) NOT NULL DEFAULT '',
					model_type VARCHAR(50) NOT NULL DEFAULT 'chat',
					context_window INT NOT NULL DEFAULT 0,
					pricing_input DECIMAL(16,6) NOT NULL DEFAULT 0,
					pricing_output DECIMAL(16,6) NOT NULL DEFAULT 0,
					pricing_cache_read DECIMAL(16,6) NOT NULL DEFAULT 0,
					status INT NOT NULL DEFAULT 1,
					is_public TINYINT(1) NOT NULL DEFAULT 1,
					capabilities TEXT,
					input_modalities VARCHAR(255) NOT NULL DEFAULT '[]',
					output_modalities VARCHAR(255) NOT NULL DEFAULT '[]',
					tags TEXT,
					category VARCHAR(100) NOT NULL DEFAULT '',
					tier VARCHAR(50) NOT NULL DEFAULT '',
					metadata TEXT,
					authorization_revision BIGINT NOT NULL DEFAULT 1,
					created_at BIGINT NOT NULL DEFAULT 0,
					updated_at BIGINT NOT NULL DEFAULT 0,
					UNIQUE KEY uk_model_id (model_id)
				)`).Error)
			}
			barrier := make(chan struct{}, 2)
			goAhead := make(chan struct{}) // released once both are past the probe
			require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:model_registry_miss", func(tx *gorm.DB) {
				if tx.Statement.Table == "models" && isGormNotFound(tx.Error) {
					barrier <- struct{}{}
					<-goAhead
				}
			}))
			ids := make([]int64, 2)
			errs := make([]error, 2)
			var wg sync.WaitGroup
			for i := range ids {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					tx := db.Begin()
					defer tx.Rollback()
					ids[i], errs[i] = ensureModelRegistryRowTx(tx, modelID)
					if errs[i] != nil {
						return
					}
					errs[i] = tx.Commit().Error
				}(i)
			}
			<-barrier
			<-barrier
			close(goAhead)
			wg.Wait()

			require.NoError(t, errs[0])
			require.NoError(t, errs[1])
			require.NotZero(t, ids[0])
			require.Equal(t, ids[0], ids[1], "both transactions must resolve the same registry row")
			var count int64
			require.NoError(t, db.Table("models").Where("model_id = ?", modelID).Count(&count).Error)
			require.EqualValues(t, 1, count, "only one registry row may exist after the race")
		})
	}
}
