package data

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/authorization"
	authztest "micro-one-api/domain/authorization/testutil"
	dbtest "micro-one-api/platform/database/testutil"
)

func TestChannelActionStaleRevisionDoesNotCallProvider(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	db := dbtest.RoutingContextDB(t, "sqlite")
	repo := &Repository{db: db, routingGroupRelations: true, encKey: []byte("0123456789abcdef0123456789abcdef")}
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(http.StatusOK) }))
	defer upstream.Close()
	channel := &biz.Channel{Type: 1, Name: "revision", Group: "default", BaseURL: upstream.URL, Status: 1}
	require.NoError(t, repo.CreateChannel(context.Background(), channel))
	require.NoError(t, db.Table("channels").Where("id = ?", channel.ID).Update("authorization_revision", 7).Error)
	uc := biz.NewChannelUsecase(repo, nil)
	uc.SetAuthorization(&authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{"channel.channel.test": authztest.All()}})
	ctx := authorization.WithExpectedRevision(authztest.Context(), "channel", channel.ID, 6)
	_, err := uc.ExecuteChannelAction(ctx, channel.ID, "test")
	require.ErrorIs(t, err, authorization.ErrWriteConflict)
	require.Zero(t, calls, "a stale diagnostic must fail before provider IO")
	stored, err := repo.FindByID(context.Background(), channel.ID)
	require.NoError(t, err)
	require.Zero(t, stored.TestTime)
	require.EqualValues(t, 7, stored.AuthorizationRevision)
}

func TestChannelActionRevisionChangedDuringProviderIORejectsCommit(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	db := dbtest.RoutingContextDB(t, "sqlite")
	repo := &Repository{db: db, routingGroupRelations: true, encKey: []byte("0123456789abcdef0123456789abcdef")}
	channel := &biz.Channel{Type: 1, Name: "concurrent revision", Group: "default", Status: 1}
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.NoError(t, db.Table("channels").Where("id = ?", channel.ID).Update("authorization_revision", 8).Error)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	channel.BaseURL = upstream.URL
	require.NoError(t, repo.CreateChannel(context.Background(), channel))
	require.NoError(t, db.Table("channels").Where("id = ?", channel.ID).Update("authorization_revision", 7).Error)
	uc := biz.NewChannelUsecase(repo, nil)
	uc.SetAuthorization(&authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{"channel.channel.test": authztest.All()}})
	ctx := authorization.WithExpectedRevision(authztest.Context(), "channel", channel.ID, 7)
	_, err := uc.ExecuteChannelAction(ctx, channel.ID, "test")
	require.ErrorIs(t, err, authorization.ErrWriteConflict)
	require.Equal(t, 1, calls)
	stored, err := repo.FindByID(context.Background(), channel.ID)
	require.NoError(t, err)
	require.Zero(t, stored.TestTime, "late conflict must roll back the diagnostic result")
	require.EqualValues(t, 8, stored.AuthorizationRevision)
}

func TestChannelActionFreshRevisionCommitsDiagnosticWithoutHealthRecovery(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	db := dbtest.RoutingContextDB(t, "sqlite")
	repo := &Repository{db: db, routingGroupRelations: true, encKey: []byte("0123456789abcdef0123456789abcdef")}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer upstream.Close()
	channel := &biz.Channel{Type: 1, Name: "fresh revision", Group: "default", BaseURL: upstream.URL, Status: 1, HealthStatus: biz.ChannelHealthUnavailable}
	require.NoError(t, repo.CreateChannel(context.Background(), channel))
	require.NoError(t, db.Table("channels").Where("id = ?", channel.ID).Update("authorization_revision", 7).Error)
	uc := biz.NewChannelUsecase(repo, nil)
	uc.SetAuthorization(&authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{"channel.channel.test": authztest.All()}})
	ctx := authorization.WithExpectedRevision(authztest.Context(), "channel", channel.ID, 7)
	result, err := uc.ExecuteChannelAction(ctx, channel.ID, "test")
	require.NoError(t, err)
	require.True(t, result.Success)
	stored, err := repo.FindByID(context.Background(), channel.ID)
	require.NoError(t, err)
	require.Positive(t, stored.TestTime)
	require.EqualValues(t, 8, stored.AuthorizationRevision, "one diagnostic advances the revision once")
	require.Equal(t, biz.ChannelHealthUnavailable, stored.HealthStatus, "a models diagnostic cannot authorize traffic recovery")
}
