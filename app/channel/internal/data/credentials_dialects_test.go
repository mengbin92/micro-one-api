package data

import (
	"context"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"micro-one-api/app/channel/internal/biz"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// These DSNs must point at disposable databases: this test owns its table.
func TestCredentialRevisionMigrationDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var dialector gorm.Dialector
			switch driver {
			case "sqlite":
				dialector = sqlite.Open(filepath.Join(t.TempDir(), "credentials.db"))
			case "mysql":
				dsn := os.Getenv("TEST_CREDENTIAL_MYSQL_DSN")
				if dsn == "" {
					t.Skip("isolated MySQL DSN not configured")
				}
				dialector = mysql.Open(dsn)
			case "postgres":
				dsn := os.Getenv("TEST_CREDENTIAL_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("isolated PostgreSQL DSN not configured")
				}
				dialector = postgres.Open(dsn)
			}
			db, err := gorm.Open(dialector, &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			defer sqlDB.Close()
			require.NoError(t, db.Exec(`CREATE TABLE subscription_accounts (id BIGINT PRIMARY KEY, access_token TEXT, refresh_token TEXT, expires_at BIGINT, account_id TEXT, updated_at BIGINT DEFAULT 0)`).Error)
			defer db.Exec("DROP TABLE subscription_accounts")
			require.NoError(t, db.Exec("INSERT INTO subscription_accounts (id,access_token,refresh_token,expires_at,account_id) VALUES (1,'historical-access','historical-refresh',1,'upstream')").Error)
			root := "../../../../migrations"
			if driver != "mysql" {
				root = filepath.Join(root, driver)
			}
			migration, err := os.ReadFile(filepath.Join(root, "108_add_credential_revision.sql"))
			require.NoError(t, err)
			require.NoError(t, db.Exec(string(migration)).Error)
			migration, err = os.ReadFile(filepath.Join(root, "109_add_credential_refresh_pending.sql"))
			require.NoError(t, err)
			require.NoError(t, db.Exec(string(migration)).Error)
			repo := &Repository{db: db, encKey: []byte("01234567890123456789012345678901")}
			old, err := repo.FindSubscriptionAccountByID(context.Background(), 1)
			require.NoError(t, err)
			require.Zero(t, old.CredentialRevision)
			old.AccessToken, old.RefreshToken = "rotation-access", "rotation-refresh"
			replay := *old
			require.NoError(t, repo.StoreSubscriptionCredentials(context.Background(), old))
			require.EqualValues(t, 1, old.CredentialRevision)
			require.NoError(t, repo.StoreSubscriptionCredentials(context.Background(), &replay))
			require.EqualValues(t, 1, replay.CredentialRevision)
			stale := replay
			stale.AccessToken = "stale"
			require.NoError(t, db.Exec("UPDATE subscription_accounts SET credential_revision=credential_revision+1, refresh_token='reauthorized' WHERE id=1").Error)
			require.ErrorIs(t, repo.StoreSubscriptionCredentials(context.Background(), &stale), biz.ErrCredentialConflict)
			got, err := repo.FindSubscriptionAccountByID(context.Background(), 1)
			require.NoError(t, err)
			require.Equal(t, "reauthorized", got.RefreshToken)
			// Two independent owners compete using the same historical revision.
			var wg sync.WaitGroup
			results := make(chan error, 2)
			for range 2 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					claim := *got
					results <- repo.ClaimSubscriptionCredentialRefresh(context.Background(), &claim)
				}()
			}
			wg.Wait()
			close(results)
			wins := 0
			for err := range results {
				if err == nil {
					wins++
				} else {
					require.ErrorIs(t, err, biz.ErrCredentialConflict)
				}
			}
			require.Equal(t, 1, wins)
			claimed, err := repo.FindSubscriptionAccountByID(context.Background(), 1)
			require.NoError(t, err)
			require.True(t, claimed.CredentialRefreshPending)
			require.ErrorIs(t, repo.ClaimSubscriptionCredentialRefresh(context.Background(), claimed), biz.ErrCredentialConflict)
			require.ErrorIs(t, repo.StoreSubscriptionCredentials(context.Background(), got), biz.ErrCredentialConflict)
			claimed.AccessToken, claimed.RefreshToken = "final-access", "final-refresh"
			completedReplay := *claimed
			require.NoError(t, repo.StoreSubscriptionCredentials(context.Background(), claimed))
			require.NoError(t, repo.StoreSubscriptionCredentials(context.Background(), &completedReplay))
			final, err := repo.FindSubscriptionAccountByID(context.Background(), 1)
			require.NoError(t, err)
			require.False(t, final.CredentialRefreshPending)
			require.Equal(t, "final-refresh", final.RefreshToken)
		})
	}
}
