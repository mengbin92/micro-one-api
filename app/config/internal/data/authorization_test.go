package data

import (
	"context"
	"github.com/stretchr/testify/require"
	"micro-one-api/app/config/internal/biz"
	"micro-one-api/domain/authorization"
	authztest "micro-one-api/domain/authorization/testutil"
	dbtest "micro-one-api/platform/database/testutil"
	"testing"
)

func TestIAMB4ConfigOwnerDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres", "memory"} {
		t.Run(driver, func(t *testing.T) {
			r := newMemoryRepository()
			if driver != "memory" {
				r = &Repository{db: dbtest.RoutingContextDB(t, driver)}
			}
			require.NoError(t, r.Set(context.Background(), &biz.ConfigEntry{Namespace: "system", Key: "StripeSecret", Value: "private"}))
			policy := &authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{"system.option.read": authztest.All(), "system.option.update": authztest.All(), "system.content.notice.update": authztest.All()}}
			uc := biz.NewConfigUsecase(r)
			uc.SetAuthorization(policy)
			request := authztest.Context()
			entry, err := uc.GetConfig(request, "system", "StripeSecret")
			require.NoError(t, err)
			require.Empty(t, entry.Value)
			require.Error(t, uc.SetConfig(request, "system", "StripeSecret", "stolen", ""))
			require.Error(t, uc.DeleteConfig(request, "system", "StripeSecret"))
			stored, err := r.Get(context.Background(), "system", "StripeSecret")
			require.NoError(t, err)
			require.Equal(t, "private", stored.Value)
			require.Error(t, uc.SetConfig(request, "system", "ModelRatio", "{}", ""))
			require.Error(t, uc.SetConfig(request, "private", "theme", "x", ""))
			require.NoError(t, uc.SetConfig(request, "system", "notice", "public notice", ""))
			entry, err = uc.PublicContent(context.Background(), "notice")
			require.NoError(t, err)
			require.Equal(t, "public notice", entry.Value)
			_, err = uc.PublicContent(context.Background(), "StripeSecret")
			require.Error(t, err)
			// A write-only content actor gets a revision without a read-after-commit error.
			delete(policy.Scopes, "system.option.read")
			entry, err = uc.SetConfigEntry(request, "system", "notice", "updated", "")
			require.NoError(t, err)
			require.Positive(t, entry.Revision)
		})
	}
}
