package data

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/authorization"
	authztest "micro-one-api/domain/authorization/testutil"
	"micro-one-api/platform/database/migrate"
	dbtest "micro-one-api/platform/database/testutil"
)

func TestSubscriptionAccountStatusLegacyRevisionUpgrade(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := dbtest.RoutingContextDB(t, driver)
			repo := &Repository{db: db}
			account := &biz.SubscriptionAccount{Name: "legacy", Platform: "codex", Status: biz.ChannelStatusEnabled}
			require.NoError(t, repo.CreateSubscriptionAccount(context.Background(), account))
			// Migration 108 left existing accounts at zero until their first credential write.
			require.NoError(t, db.Model(&subscriptionAccountModel{}).Where("id = ?", account.ID).Update("credential_revision", 0).Error)
			current := &biz.SubscriptionAccount{Name: "current", Platform: "codex", Status: biz.ChannelStatusEnabled, CredentialRevision: 7}
			require.NoError(t, repo.CreateSubscriptionAccount(context.Background(), current))
			require.NoError(t, db.Exec("DELETE FROM schema_migrations WHERE version = ?", "124_backfill_subscription_account_revision").Error)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			dir := filepath.Join("../../../../migrations")
			if driver != "mysql" {
				dir = filepath.Join(dir, driver)
			}
			runner := migrate.NewWithDriver(sqlDB, dir, driver).WithOwnershipFilter("channel")
			_, err = runner.Apply(context.Background())
			require.NoError(t, err)
			stored, err := repo.FindSubscriptionAccountByID(context.Background(), account.ID)
			require.NoError(t, err)
			ctx := authorization.WithWriteReason(context.Background(), "上游到期")
			ctx = authorization.WithQueryScope(ctx, "channel.account.disable", authztest.Resources(account.ID))
			require.ErrorIs(t, repo.ChangeSubscriptionAccountStatus(ctx, account.ID, biz.ChannelStatusDisabled), authorization.ErrWritePrecondition)
			ctx = authorization.WithExpectedRevision(ctx, "account", account.ID, stored.CredentialRevision)
			require.ErrorIs(t, repo.ChangeSubscriptionAccountStatus(authorization.WithWriteReason(ctx, ""), account.ID, biz.ChannelStatusDisabled), authorization.ErrWritePrecondition)
			require.NoError(t, repo.ChangeSubscriptionAccountStatus(ctx, account.ID, biz.ChannelStatusDisabled))
			stored, err = repo.FindSubscriptionAccountByID(context.Background(), account.ID)
			require.NoError(t, err)
			require.EqualValues(t, biz.ChannelStatusDisabled, stored.Status)
			require.EqualValues(t, 2, stored.CredentialRevision)
			require.ErrorIs(t, repo.ChangeSubscriptionAccountStatus(ctx, account.ID, biz.ChannelStatusEnabled), authorization.ErrWriteConflict)
			enable := authorization.WithQueryScope(authorization.WithWriteReason(context.Background(), "重新启用"), "channel.account.enable", authztest.Resources(account.ID))
			enable = authorization.WithExpectedRevision(enable, "account", account.ID, stored.CredentialRevision)
			require.NoError(t, repo.ChangeSubscriptionAccountStatus(enable, account.ID, biz.ChannelStatusEnabled))
			stored, err = repo.FindSubscriptionAccountByID(context.Background(), account.ID)
			require.NoError(t, err)
			require.EqualValues(t, biz.ChannelStatusEnabled, stored.Status)
			require.EqualValues(t, 3, stored.CredentialRevision)
			storedCurrent, err := repo.FindSubscriptionAccountByID(context.Background(), current.ID)
			require.NoError(t, err)
			require.EqualValues(t, 7, storedCurrent.CredentialRevision)
			applied, err := runner.Apply(context.Background())
			require.NoError(t, err)
			require.Empty(t, applied)
		})
	}
}
